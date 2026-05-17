package main

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
	"time"

	"github.com/google/uuid"
	"github.com/lucaspose/fvc/internal"
	"github.com/lucaspose/fvc/proto"
	"google.golang.org/grpc"
	_ "modernc.org/sqlite"
)

type Server struct {
	proto.UnimplementedFvcServiceServer
	DB     *sql.DB
	Config DaemonConfig
	Store  *ImageStore
}

func sendFCConfig(socketPath, method, path, jsonBody string) error {
	client := http.Client{
		Transport: &http.Transport{
			DialContext: func(_ context.Context, _, _ string) (net.Conn, error) {
				return net.Dial("unix", socketPath)
			},
		},
	}

	req, err := http.NewRequest(method, "http://localhost"+path, bytes.NewBufferString(jsonBody))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusNoContent && resp.StatusCode != http.StatusOK {
		return fmt.Errorf("bad status code from firecracker API: %s", resp.Status)
	}
	return nil
}

func waitForSocket(socketPath string, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		conn, err := net.Dial("unix", socketPath)
		if err == nil {
			_ = conn.Close()
			return nil
		}
		time.Sleep(25 * time.Millisecond)
	}
	return fmt.Errorf("firecracker API socket not ready after %s", timeout)
}

func (s *Server) Run(ctx context.Context, req *proto.RunRequest) (*proto.RunResponse, error) {
	vmID := uuid.New().String()

	imageName, cpus, memoryMb, err := validateRunRequest(req)
	if err != nil {
		return runFailed("invalid run request: %v", err), nil
	}

	if _, err := s.Store.PullImageIfNeeded(imageName); err != nil {
		return runFailed("Storage error (pull image): %v", err), nil
	}

	kernelPath, err := s.Store.PullKernelIfNeeded()
	if err != nil {
		return runFailed("Storage error (pull kernel): %v", err), nil
	}

	vmDrivePath, err := s.Store.CloneImage(imageName, vmID)
	if err != nil {
		return runFailed("Storage error (clone): %v", err), nil
	}

	socketPath := filepath.Join(os.TempDir(), fmt.Sprintf("fvc-%s.socket", vmID))
	logPath := filepath.Join(s.Config.LogDir, vmID+".log")
	_ = os.Remove(socketPath)

	logFile, err := os.OpenFile(logPath, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0644)
	if err != nil {
		_ = os.Remove(vmDrivePath)
		return runFailed("Log error: %v", err), nil
	}
	defer logFile.Close()

	cmd := exec.Command(s.Config.FirecrackerPath, "--api-sock", socketPath)
	cmd.Stdout = logFile
	cmd.Stderr = logFile
	if err = cmd.Start(); err != nil {
		_ = os.Remove(vmDrivePath)
		return runFailed("Firecracker error: %v", err), nil
	}

	processPid := int32(cmd.Process.Pid)
	go s.watchVM(cmd, vmID, socketPath, vmDrivePath, processPid)

	if err := waitForSocket(socketPath, 3*time.Second); err != nil {
		_ = cmd.Process.Kill()
		return runFailed("Firecracker startup error: %v", err), nil
	}

	kernelJSON, err := json.Marshal(map[string]string{
		"kernel_image_path": kernelPath,
		"boot_args":         "console=ttyS0 reboot=k panic=1 pci=off",
	})
	if err != nil {
		_ = cmd.Process.Kill()
		return runFailed("failed to prepare boot source config: %v", err), nil
	}
	if err := sendFCConfig(socketPath, "PUT", "/boot-source", string(kernelJSON)); err != nil {
		_ = cmd.Process.Kill()
		return runFailed("failed to configure boot source: %v", err), nil
	}

	driveJSON, err := json.Marshal(map[string]any{
		"drive_id":       "rootfs",
		"path_on_host":   vmDrivePath,
		"is_root_device": true,
		"is_read_only":   false,
	})
	if err != nil {
		_ = cmd.Process.Kill()
		return runFailed("failed to prepare rootfs drive config: %v", err), nil
	}
	if err := sendFCConfig(socketPath, "PUT", "/drives/rootfs", string(driveJSON)); err != nil {
		_ = cmd.Process.Kill()
		return runFailed("failed to configure rootfs drive: %v", err), nil
	}

	machineJSON, err := json.Marshal(map[string]int32{
		"vcpu_count":   cpus,
		"mem_size_mib": memoryMb,
	})
	if err != nil {
		_ = cmd.Process.Kill()
		return runFailed("failed to prepare machine config: %v", err), nil
	}
	if err = sendFCConfig(socketPath, "PUT", "/machine-config", string(machineJSON)); err != nil {
		_ = cmd.Process.Kill()
		return runFailed("failed to configure machine resources: %v", err), nil
	}

	if err = sendFCConfig(socketPath, "PUT", "/actions", `{"action_type":"InstanceStart"}`); err != nil {
		_ = cmd.Process.Kill()
		return runFailed("failed to start microVM: %v", err), nil
	}

	query := `INSERT INTO vms (id, pid, status, image, cpus, memory_mb, log_path) VALUES (?, ?, ?, ?, ?, ?, ?)`
	_, err = s.DB.Exec(query, vmID, processPid, internal.VmRunning, imageName, cpus, memoryMb, logPath)
	if err != nil {
		_ = cmd.Process.Kill()
		return runFailed("failed to save VM state to database: %v", err), nil
	}
	return &proto.RunResponse{
		VmId:   vmID,
		Status: internal.VmRunning,
	}, nil
}

func validateRunRequest(req *proto.RunRequest) (string, int32, int32, error) {
	if req == nil {
		return "", 0, 0, fmt.Errorf("request is required")
	}

	imageName := req.Source
	if imageName == "" {
		imageName = "ubuntu"
	}
	if err := internal.ValidateImageRef(imageName); err != nil {
		return "", 0, 0, err
	}

	cpus := int32(1)
	memoryMb := int32(512)
	if req.Config != nil {
		cpus = req.Config.Cpus
		memoryMb = req.Config.MemoryMb
	}
	if err := internal.ValidateResources(cpus, memoryMb); err != nil {
		return "", 0, 0, err
	}
	return imageName, cpus, memoryMb, nil
}

func runFailed(format string, args ...any) *proto.RunResponse {
	return &proto.RunResponse{
		Status:       internal.VmFailed,
		ErrorMessage: fmt.Sprintf(format, args...),
	}
}

func (s *Server) watchVM(cmd *exec.Cmd, id string, sock string, drive string, pid int32) {
	_ = cmd.Wait()
	log.Printf("Info: Firecracker microVM %s (PID %d) stopped.", id, pid)
	_ = os.Remove(sock)
	_ = os.Remove(drive)

	query := `UPDATE vms SET status = ?, pid = 0 WHERE id = ? AND status = ?`
	_, _ = s.DB.Exec(query, internal.VmStopped, id, internal.VmRunning)
}

func (s *Server) Stop(ctx context.Context, req *proto.StopRequest) (*proto.StopResponse, error) {
	var pid int
	var status string
	querySelect := `SELECT pid, status FROM vms WHERE id = ?`
	err := s.DB.QueryRow(querySelect, req.VmId).Scan(&pid, &status)
	if err != nil {
		return &proto.StopResponse{
			Success: false,
			Message: fmt.Sprintf("VM introuvable ou erreur BDD : %v", err),
		}, nil
	}
	if status == internal.VmStopped || status == internal.VmDown || pid == 0 {
		return &proto.StopResponse{
			Success: true,
			Message: "La microVM est deja arretee.",
		}, nil
	}
	process, err := os.FindProcess(pid)
	if err != nil {
		return &proto.StopResponse{
			Success: false,
			Message: fmt.Sprintf("Impossible de trouver le processus systeme %d : %v", pid, err),
		}, nil
	}

	if err := process.Signal(syscall.SIGTERM); err != nil {
		log.Printf("Warning: impossible d'envoyer SIGTERM au PID %d: %v", pid, err)
	}

	timeout := time.Duration(req.TimeoutSeconds) * time.Second
	if timeout <= 0 {
		timeout = 10 * time.Second
	}

	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if !processExists(pid) {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	if processExists(pid) {
		_ = process.Kill()
	}

	queryUpdate := `UPDATE vms SET status = ?, pid = 0 WHERE id = ?`
	_, err = s.DB.Exec(queryUpdate, internal.VmStopped, req.VmId)
	if err != nil {
		return &proto.StopResponse{
			Success: false,
			Message: fmt.Sprintf("Processus arrete mais echec de la mise a jour BDD : %v", err),
		}, nil
	}
	return &proto.StopResponse{
		Success: true,
		Message: fmt.Sprintf("MicroVM %s arretee.", req.VmId),
	}, nil
}

func processExists(pid int) bool {
	return syscall.Kill(pid, 0) == nil
}

func (s *Server) Ps(ctx context.Context, req *proto.PsRequest) (*proto.PsResponse, error) {
	query := "SELECT id, pid, status, image, cpus, memory_mb FROM vms"
	if !req.All {
		query += " WHERE status = 'running'"
	}
	rows, err := s.DB.Query(query)
	if err != nil {
		return nil, fmt.Errorf("failed to query vms: %v", err)
	}
	defer rows.Close()
	var vms []*proto.VmDetails

	for rows.Next() {
		var id, status, image string
		var pid, cpus, memoryMb int32

		if err := rows.Scan(&id, &pid, &status, &image, &cpus, &memoryMb); err != nil {
			return nil, fmt.Errorf("failed to scan row: %v", err)
		}
		vms = append(vms, &proto.VmDetails{
			VmId:   id,
			Pid:    pid,
			Status: status,
			Image:  image,
			Config: &proto.VmConfig{
				Cpus:     cpus,
				MemoryMb: memoryMb,
			},
		})
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("failed to read vm rows: %v", err)
	}
	return &proto.PsResponse{Vms: vms}, nil
}

func ensureSchema(db *sql.DB) error {
	query := `CREATE TABLE IF NOT EXISTS vms (
		id TEXT PRIMARY KEY NOT NULL,
		pid INTEGER DEFAULT 0,
		status TEXT NOT NULL,
		image TEXT NOT NULL,
		cpus INTEGER DEFAULT 1,
		memory_mb INTEGER DEFAULT 512,
		log_path TEXT,
		created_at DATETIME DEFAULT CURRENT_TIMESTAMP
	);`
	if _, err := db.Exec(query); err != nil {
		return err
	}
	_, _ = db.Exec(`ALTER TABLE vms ADD COLUMN log_path TEXT`)
	return nil
}

func reconcileState(db *sql.DB) error {
	rows, err := db.Query(`SELECT id, pid FROM vms WHERE status = ? AND pid > 0`, internal.VmRunning)
	if err != nil {
		return err
	}
	defer rows.Close()

	for rows.Next() {
		var id string
		var pid int
		if err := rows.Scan(&id, &pid); err != nil {
			return err
		}
		if !processExists(pid) {
			_, _ = db.Exec(`UPDATE vms SET status = ?, pid = 0 WHERE id = ?`, internal.VmStopped, id)
		}
	}
	return rows.Err()
}

func main() {
	cfg := LoadConfig()
	store := NewImageStore(cfg)
	if err := store.Init(); err != nil {
		log.Fatalf("Storage initialization error: %v", err)
	}
	if err := os.MkdirAll(cfg.LogDir, 0755); err != nil {
		log.Fatalf("Log directory error: %v", err)
	}

	db, err := sql.Open("sqlite", cfg.DBPath)
	if err != nil {
		log.Fatalf("Db creation error: %v", err)
	}
	defer db.Close()
	if err := ensureSchema(db); err != nil {
		log.Fatalf("Table creation error: %v", err)
	}
	if err := reconcileState(db); err != nil {
		log.Fatalf("State reconciliation error: %v", err)
	}

	listener, err := net.Listen("tcp", cfg.GRPCAddr)
	if err != nil {
		log.Fatalf("Network error: failed to listen on %s: %v", cfg.GRPCAddr, err)
	}

	log.Printf("fvcd listening on %s with data dir %s", cfg.GRPCAddr, cfg.BaseDir)
	grpcServer := grpc.NewServer()
	proto.RegisterFvcServiceServer(grpcServer, &Server{DB: db, Config: cfg, Store: store})
	if err := grpcServer.Serve(listener); err != nil {
		log.Fatalf("gRPC server error: %v", err)
	}
}
