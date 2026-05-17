package main

import (
	"bufio"
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/google/uuid"
	"github.com/lucaspose/fvc/internal"
	"github.com/lucaspose/fvc/proto"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/timestamppb"
	_ "modernc.org/sqlite"
)

type Server struct {
	proto.UnimplementedFvcServiceServer
	DB     *sql.DB
	Config DaemonConfig
	Store  *ImageStore
	Net    *NetworkManager
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
	return s.runMicroVM(ctx, req, nil)
}

func (s *Server) RunStream(req *proto.RunRequest, stream grpc.ServerStreamingServer[proto.RunEvent]) error {
	_, err := s.runMicroVM(stream.Context(), req, func(event *proto.RunEvent) error {
		return stream.Send(event)
	})
	return err
}

func (s *Server) runMicroVM(ctx context.Context, req *proto.RunRequest, emit func(*proto.RunEvent) error) (*proto.RunResponse, error) {
	vmID := uuid.New().String()
	sendEvent := func(stage, eventStatus, message, errorMessage string) error {
		if ctx != nil && ctx.Err() != nil {
			return ctx.Err()
		}
		if emit == nil {
			return nil
		}
		return emit(&proto.RunEvent{
			Stage:        stage,
			Status:       eventStatus,
			Message:      message,
			VmId:         vmID,
			ErrorMessage: errorMessage,
		})
	}
	step := func(stage, message string) error {
		log.Printf("run %s: %s", vmID, message)
		return sendEvent(stage, "running", message, "")
	}
	complete := func(stage, message string) error {
		return sendEvent(stage, "complete", message, "")
	}
	fail := func(stage, format string, args ...any) (*proto.RunResponse, error) {
		message := fmt.Sprintf(format, args...)
		_ = sendEvent(stage, "error", message, message)
		return runFailed("%s", message), nil
	}

	imageName, cpus, memoryMb, err := validateRunRequest(req)
	if err != nil {
		return fail("validate", "invalid request: %v", err)
	}

	if err := step("image", fmt.Sprintf("Resolving image %s", imageName)); err != nil {
		return nil, err
	}
	if _, err := s.Store.PullImageIfNeeded(imageName); err != nil {
		return fail("image", "image pull failed: %v", err)
	}
	if err := complete("image", "Image ready"); err != nil {
		return nil, err
	}

	if err := step("kernel", "Resolving kernel"); err != nil {
		return nil, err
	}
	kernelPath, err := s.Store.PullKernelIfNeeded()
	if err != nil {
		return fail("kernel", "kernel pull failed: %v", err)
	}
	if err := complete("kernel", "Kernel ready"); err != nil {
		return nil, err
	}

	if err := step("rootfs", "Cloning root filesystem"); err != nil {
		return nil, err
	}
	vmDrivePath, err := s.Store.CloneImage(imageName, vmID)
	if err != nil {
		return fail("rootfs", "rootfs clone failed: %v", err)
	}
	if err := complete("rootfs", "Root filesystem ready"); err != nil {
		return nil, err
	}

	var netCfg *NetworkConfig
	if s.Config.NetworkEnabled {
		if err := step("network", "Preparing network"); err != nil {
			return nil, err
		}
		cfg, err := s.networkManager().Setup(vmID)
		if err != nil {
			_ = os.Remove(vmDrivePath)
			return fail("network", "network setup failed: %v", err)
		}
		netCfg = &cfg
		if err := complete("network", fmt.Sprintf("Network ready: %s %s", cfg.TapName, cfg.GuestIP)); err != nil {
			return nil, err
		}
	}

	socketPath := filepath.Join(os.TempDir(), fmt.Sprintf("fvc-%s.socket", vmID))
	logPath := filepath.Join(s.Config.LogDir, vmID+".log")
	consolePath := filepath.Join(os.TempDir(), fmt.Sprintf("fvc-%s.console.in", vmID))
	_ = os.Remove(socketPath)
	_ = os.Remove(consolePath)

	logFile, err := os.OpenFile(logPath, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0644)
	if err != nil {
		_ = os.Remove(vmDrivePath)
		s.cleanupNetwork(netCfg)
		return fail("logs", "log file setup failed: %v", err)
	}
	if err := applyRuntimePermissions(logPath, s.Config.RuntimeGroup, 0660); err != nil {
		logFile.Close()
		_ = os.Remove(vmDrivePath)
		s.cleanupNetwork(netCfg)
		return fail("logs", "log file permission setup failed: %v", err)
	}
	defer logFile.Close()
	consoleInput, err := openConsoleInput(consolePath, s.Config.RuntimeGroup)
	if err != nil {
		_ = os.Remove(vmDrivePath)
		s.cleanupNetwork(netCfg)
		return fail("console", "console setup failed: %v", err)
	}
	defer consoleInput.Close()

	if err := step("firecracker", "Launching Firecracker"); err != nil {
		return nil, err
	}
	cmd := exec.Command(s.Config.FirecrackerPath, "--api-sock", socketPath)
	cmd.Stdin = consoleInput
	cmd.Stdout = logFile
	cmd.Stderr = logFile
	if err = cmd.Start(); err != nil {
		_ = os.Remove(vmDrivePath)
		s.cleanupNetwork(netCfg)
		return fail("firecracker", "firecracker launch failed: %v", err)
	}

	processPid := int32(cmd.Process.Pid)
	go s.watchVM(cmd, vmID, socketPath, vmDrivePath, consolePath, processPid, netCfg)

	if err := waitForSocket(socketPath, 3*time.Second); err != nil {
		_ = cmd.Process.Kill()
		return fail("firecracker", "firecracker API socket not ready: %v", err)
	}
	if err := complete("firecracker", "Firecracker API ready"); err != nil {
		return nil, err
	}

	if err := step("boot", "Configuring boot source"); err != nil {
		return nil, err
	}
	kernelJSON, err := json.Marshal(map[string]string{
		"kernel_image_path": kernelPath,
		"boot_args":         bootArgs(netCfg),
	})
	if err != nil {
		_ = cmd.Process.Kill()
		return fail("boot", "boot source config build failed: %v", err)
	}
	if err := sendFCConfig(socketPath, "PUT", "/boot-source", string(kernelJSON)); err != nil {
		_ = cmd.Process.Kill()
		return fail("boot", "boot source config failed: %v", err)
	}
	if err := complete("boot", "Boot source configured"); err != nil {
		return nil, err
	}

	if err := step("drive", "Attaching root filesystem"); err != nil {
		return nil, err
	}
	driveJSON, err := json.Marshal(map[string]any{
		"drive_id":       "rootfs",
		"path_on_host":   vmDrivePath,
		"is_root_device": true,
		"is_read_only":   false,
	})
	if err != nil {
		_ = cmd.Process.Kill()
		return fail("drive", "rootfs drive config build failed: %v", err)
	}
	if err := sendFCConfig(socketPath, "PUT", "/drives/rootfs", string(driveJSON)); err != nil {
		_ = cmd.Process.Kill()
		return fail("drive", "rootfs drive config failed: %v", err)
	}
	if err := complete("drive", "Root filesystem attached"); err != nil {
		return nil, err
	}

	if netCfg != nil {
		if err := step("netif", "Attaching network interface"); err != nil {
			return nil, err
		}
		if err := configureFirecrackerNetwork(socketPath, *netCfg); err != nil {
			_ = cmd.Process.Kill()
			return fail("netif", "network interface config failed: %v", err)
		}
		if err := complete("netif", "Network interface attached"); err != nil {
			return nil, err
		}
	}

	if err := step("resources", fmt.Sprintf("Configuring resources cpu=%d memory=%dMB", cpus, memoryMb)); err != nil {
		return nil, err
	}
	machineJSON, err := json.Marshal(map[string]int32{
		"vcpu_count":   cpus,
		"mem_size_mib": memoryMb,
	})
	if err != nil {
		_ = cmd.Process.Kill()
		return fail("resources", "machine config build failed: %v", err)
	}
	if err = sendFCConfig(socketPath, "PUT", "/machine-config", string(machineJSON)); err != nil {
		_ = cmd.Process.Kill()
		return fail("resources", "machine resource config failed: %v", err)
	}
	if err := complete("resources", "Resources configured"); err != nil {
		return nil, err
	}

	if err := step("start", "Starting microVM"); err != nil {
		return nil, err
	}
	if err = sendFCConfig(socketPath, "PUT", "/actions", `{"action_type":"InstanceStart"}`); err != nil {
		_ = cmd.Process.Kill()
		return fail("start", "instance start failed: %v", err)
	}

	tapName, guestIP, mac := networkFields(netCfg)
	query := `INSERT INTO vms (id, pid, status, image, cpus, memory_mb, log_path, drive_path, console_path, tap_name, guest_ip, mac_address) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`
	_, err = s.DB.Exec(query, vmID, processPid, internal.VmRunning, imageName, cpus, memoryMb, logPath, vmDrivePath, consolePath, tapName, guestIP, mac)
	if err != nil {
		_ = cmd.Process.Kill()
		return fail("state", "state save failed: %v", err)
	}
	log.Printf("run %s: started successfully", vmID)
	if err := sendEvent("done", "complete", "MicroVM started", ""); err != nil {
		return nil, err
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

func (s *Server) watchVM(cmd *exec.Cmd, id string, sock string, drive string, consolePath string, pid int32, netCfg *NetworkConfig) {
	_ = cmd.Wait()
	log.Printf("vm %s: firecracker process %d stopped", id, pid)
	_ = os.Remove(sock)
	_ = os.Remove(consolePath)
	s.cleanupNetwork(netCfg)

	query := `UPDATE vms SET status = ?, pid = 0 WHERE id = ? AND status = ?`
	_, _ = s.DB.Exec(query, internal.VmStopped, id, internal.VmRunning)
}

func (s *Server) Stop(ctx context.Context, req *proto.StopRequest) (*proto.StopResponse, error) {
	var pid int
	var statusValue, tapName, guestIP, mac string
	querySelect := `SELECT pid, status, COALESCE(tap_name, ''), COALESCE(guest_ip, ''), COALESCE(mac_address, '') FROM vms WHERE id = ?`
	err := s.DB.QueryRow(querySelect, req.VmId).Scan(&pid, &statusValue, &tapName, &guestIP, &mac)
	if err != nil {
		return &proto.StopResponse{
			Success: false,
			Message: fmt.Sprintf("vm not found: %v", err),
		}, nil
	}
	if statusValue == internal.VmStopped || statusValue == internal.VmDown || pid == 0 {
		s.cleanupNetwork(&NetworkConfig{TapName: tapName, GuestIP: guestIP, MAC: mac})
		return &proto.StopResponse{
			Success: true,
			Message: "microVM is already stopped",
		}, nil
	}
	process, err := os.FindProcess(pid)
	if err != nil {
		return &proto.StopResponse{
			Success: false,
			Message: fmt.Sprintf("process lookup failed for pid %d: %v", pid, err),
		}, nil
	}

	if err := process.Signal(syscall.SIGTERM); err != nil {
		log.Printf("stop %s: SIGTERM failed for pid %d: %v", req.VmId, pid, err)
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
	s.cleanupNetwork(&NetworkConfig{TapName: tapName, GuestIP: guestIP, MAC: mac})

	queryUpdate := `UPDATE vms SET status = ?, pid = 0 WHERE id = ?`
	_, err = s.DB.Exec(queryUpdate, internal.VmStopped, req.VmId)
	if err != nil {
		return &proto.StopResponse{
			Success: false,
			Message: fmt.Sprintf("process stopped but state update failed: %v", err),
		}, nil
	}
	return &proto.StopResponse{
		Success: true,
		Message: fmt.Sprintf("microVM stopped: %s", req.VmId),
	}, nil
}

func (s *Server) Start(ctx context.Context, req *proto.StartRequest) (*proto.StartResponse, error) {
	if req == nil || req.VmId == "" {
		return &proto.StartResponse{Success: false, Message: "vm id is required"}, nil
	}

	var statusValue, imageName, logPath, drivePath string
	var cpus, memoryMb int32
	err := s.DB.QueryRow(
		`SELECT status, image, cpus, memory_mb, COALESCE(log_path, ''), COALESCE(drive_path, '') FROM vms WHERE id = ?`,
		req.VmId,
	).Scan(&statusValue, &imageName, &cpus, &memoryMb, &logPath, &drivePath)
	if err == sql.ErrNoRows {
		return &proto.StartResponse{Success: false, Message: fmt.Sprintf("vm not found: %s", req.VmId)}, nil
	}
	if err != nil {
		return &proto.StartResponse{Success: false, Message: fmt.Sprintf("state lookup failed: %v", err)}, nil
	}
	if statusValue == internal.VmRunning {
		return &proto.StartResponse{Success: true, Message: fmt.Sprintf("microVM already running: %s", req.VmId)}, nil
	}
	if drivePath == "" {
		return &proto.StartResponse{Success: false, Message: "microVM cannot be restarted: missing drive path"}, nil
	}
	if _, err := os.Stat(drivePath); err != nil {
		return &proto.StartResponse{Success: false, Message: fmt.Sprintf("microVM cannot be restarted: drive unavailable: %v", err)}, nil
	}

	kernelPath, err := s.Store.PullKernelIfNeeded()
	if err != nil {
		return &proto.StartResponse{Success: false, Message: fmt.Sprintf("kernel pull failed: %v", err)}, nil
	}
	var netCfg *NetworkConfig
	if s.Config.NetworkEnabled {
		cfg, err := s.networkManager().Setup(req.VmId)
		if err != nil {
			return &proto.StartResponse{Success: false, Message: fmt.Sprintf("network setup failed: %v", err)}, nil
		}
		netCfg = &cfg
		_, _ = s.DB.Exec(`UPDATE vms SET tap_name = ?, guest_ip = ?, mac_address = ? WHERE id = ?`, cfg.TapName, cfg.GuestIP, cfg.MAC, req.VmId)
	}
	consolePath := filepath.Join(os.TempDir(), fmt.Sprintf("fvc-%s.console.in", req.VmId))
	pid, _, err := s.launchFirecracker(req.VmId, kernelPath, drivePath, logPath, consolePath, cpus, memoryMb, netCfg)
	if err != nil {
		s.cleanupNetwork(netCfg)
		return &proto.StartResponse{Success: false, Message: err.Error()}, nil
	}

	_, err = s.DB.Exec(`UPDATE vms SET status = ?, pid = ?, console_path = ? WHERE id = ?`, internal.VmRunning, pid, consolePath, req.VmId)
	if err != nil {
		process, findErr := os.FindProcess(int(pid))
		if findErr == nil {
			_ = process.Kill()
		}
		return &proto.StartResponse{Success: false, Message: fmt.Sprintf("state update failed: %v", err)}, nil
	}
	log.Printf("start %s: restarted successfully", req.VmId)
	return &proto.StartResponse{Success: true, Message: fmt.Sprintf("microVM started: %s", req.VmId)}, nil
}

func (s *Server) Rm(ctx context.Context, req *proto.RmRequest) (*proto.RmResponse, error) {
	if req == nil || req.VmId == "" {
		return &proto.RmResponse{Success: false, Message: "vm id is required"}, nil
	}

	var statusValue, drivePath, logPath, consolePath, tapName, guestIP, mac string
	var pid int
	err := s.DB.QueryRow(`SELECT status, pid, COALESCE(drive_path, ''), COALESCE(log_path, ''), COALESCE(console_path, ''), COALESCE(tap_name, ''), COALESCE(guest_ip, ''), COALESCE(mac_address, '') FROM vms WHERE id = ?`, req.VmId).Scan(&statusValue, &pid, &drivePath, &logPath, &consolePath, &tapName, &guestIP, &mac)
	if err == sql.ErrNoRows {
		return &proto.RmResponse{Success: false, Message: fmt.Sprintf("vm not found: %s", req.VmId)}, nil
	}
	if err != nil {
		return &proto.RmResponse{Success: false, Message: fmt.Sprintf("state lookup failed: %v", err)}, nil
	}
	if statusValue == internal.VmRunning && pid > 0 {
		return &proto.RmResponse{Success: false, Message: "cannot remove a running microVM; stop it first"}, nil
	}

	s.cleanupNetwork(&NetworkConfig{TapName: tapName, GuestIP: guestIP, MAC: mac})
	if drivePath != "" {
		_ = os.Remove(drivePath)
	}
	if logPath != "" {
		_ = os.Remove(logPath)
	}
	if consolePath != "" {
		_ = os.Remove(consolePath)
	}
	_, err = s.DB.Exec(`DELETE FROM vms WHERE id = ?`, req.VmId)
	if err != nil {
		return &proto.RmResponse{Success: false, Message: fmt.Sprintf("state delete failed: %v", err)}, nil
	}
	return &proto.RmResponse{Success: true, Message: fmt.Sprintf("microVM removed: %s", req.VmId)}, nil
}

func (s *Server) ConsoleInfo(ctx context.Context, req *proto.ConsoleInfoRequest) (*proto.ConsoleInfoResponse, error) {
	if req == nil || req.VmId == "" {
		return &proto.ConsoleInfoResponse{Success: false, Message: "vm id is required"}, nil
	}

	var statusValue, logPath, inputPath string
	err := s.DB.QueryRow(`SELECT status, COALESCE(log_path, ''), COALESCE(console_path, '') FROM vms WHERE id = ?`, req.VmId).Scan(&statusValue, &logPath, &inputPath)
	if err == sql.ErrNoRows {
		return &proto.ConsoleInfoResponse{Success: false, Message: fmt.Sprintf("vm not found: %s", req.VmId)}, nil
	}
	if err != nil {
		return &proto.ConsoleInfoResponse{Success: false, Message: fmt.Sprintf("state lookup failed: %v", err)}, nil
	}
	if statusValue != internal.VmRunning {
		return &proto.ConsoleInfoResponse{Success: false, Message: fmt.Sprintf("microVM is not running: %s", statusValue)}, nil
	}
	if logPath == "" || inputPath == "" {
		return &proto.ConsoleInfoResponse{Success: false, Message: "console is not available for this microVM"}, nil
	}
	if err := applyRuntimePermissions(logPath, s.Config.RuntimeGroup, 0660); err != nil {
		return &proto.ConsoleInfoResponse{Success: false, Message: fmt.Sprintf("console log permission repair failed: %v", err)}, nil
	}
	if err := applyRuntimePermissions(inputPath, s.Config.RuntimeGroup, 0660); err != nil {
		return &proto.ConsoleInfoResponse{Success: false, Message: fmt.Sprintf("console input permission repair failed: %v", err)}, nil
	}
	return &proto.ConsoleInfoResponse{
		Success:   true,
		Message:   "console ready",
		VmId:      req.VmId,
		LogPath:   logPath,
		InputPath: inputPath,
	}, nil
}

func (s *Server) launchFirecracker(vmID, kernelPath, drivePath, logPath, consolePath string, cpus, memoryMb int32, netCfg *NetworkConfig) (int32, string, error) {
	socketPath := filepath.Join(os.TempDir(), fmt.Sprintf("fvc-%s.socket", vmID))
	_ = os.Remove(socketPath)
	_ = os.Remove(consolePath)

	logFile, err := os.OpenFile(logPath, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0644)
	if err != nil {
		return 0, "", fmt.Errorf("log file setup failed: %v", err)
	}
	if err := applyRuntimePermissions(logPath, s.Config.RuntimeGroup, 0660); err != nil {
		logFile.Close()
		return 0, "", fmt.Errorf("log file permission setup failed: %v", err)
	}
	defer logFile.Close()
	consoleInput, err := openConsoleInput(consolePath, s.Config.RuntimeGroup)
	if err != nil {
		return 0, "", fmt.Errorf("console setup failed: %v", err)
	}
	defer consoleInput.Close()

	cmd := exec.Command(s.Config.FirecrackerPath, "--api-sock", socketPath)
	cmd.Stdin = consoleInput
	cmd.Stdout = logFile
	cmd.Stderr = logFile
	if err = cmd.Start(); err != nil {
		return 0, "", fmt.Errorf("firecracker launch failed: %v", err)
	}

	processPid := int32(cmd.Process.Pid)
	go s.watchVM(cmd, vmID, socketPath, drivePath, consolePath, processPid, netCfg)

	if err := waitForSocket(socketPath, 3*time.Second); err != nil {
		_ = cmd.Process.Kill()
		return 0, "", fmt.Errorf("firecracker API socket not ready: %v", err)
	}

	kernelJSON, err := json.Marshal(map[string]string{
		"kernel_image_path": kernelPath,
		"boot_args":         bootArgs(netCfg),
	})
	if err != nil {
		_ = cmd.Process.Kill()
		return 0, "", fmt.Errorf("boot source config build failed: %v", err)
	}
	if err := sendFCConfig(socketPath, "PUT", "/boot-source", string(kernelJSON)); err != nil {
		_ = cmd.Process.Kill()
		return 0, "", fmt.Errorf("boot source config failed: %v", err)
	}

	driveJSON, err := json.Marshal(map[string]any{
		"drive_id":       "rootfs",
		"path_on_host":   drivePath,
		"is_root_device": true,
		"is_read_only":   false,
	})
	if err != nil {
		_ = cmd.Process.Kill()
		return 0, "", fmt.Errorf("rootfs drive config build failed: %v", err)
	}
	if err := sendFCConfig(socketPath, "PUT", "/drives/rootfs", string(driveJSON)); err != nil {
		_ = cmd.Process.Kill()
		return 0, "", fmt.Errorf("rootfs drive config failed: %v", err)
	}

	if netCfg != nil {
		if err := configureFirecrackerNetwork(socketPath, *netCfg); err != nil {
			_ = cmd.Process.Kill()
			return 0, "", fmt.Errorf("network interface config failed: %v", err)
		}
	}

	machineJSON, err := json.Marshal(map[string]int32{
		"vcpu_count":   cpus,
		"mem_size_mib": memoryMb,
	})
	if err != nil {
		_ = cmd.Process.Kill()
		return 0, "", fmt.Errorf("machine config build failed: %v", err)
	}
	if err = sendFCConfig(socketPath, "PUT", "/machine-config", string(machineJSON)); err != nil {
		_ = cmd.Process.Kill()
		return 0, "", fmt.Errorf("machine resource config failed: %v", err)
	}

	if err = sendFCConfig(socketPath, "PUT", "/actions", `{"action_type":"InstanceStart"}`); err != nil {
		_ = cmd.Process.Kill()
		return 0, "", fmt.Errorf("instance start failed: %v", err)
	}
	return processPid, socketPath, nil
}

func (s *Server) networkManager() *NetworkManager {
	if s.Net != nil {
		return s.Net
	}
	return NewNetworkManager(nil)
}

func (s *Server) cleanupNetwork(cfg *NetworkConfig) {
	if cfg == nil {
		return
	}
	_ = s.networkManager().Cleanup(*cfg)
}

func networkFields(cfg *NetworkConfig) (string, string, string) {
	if cfg == nil {
		return "", "", ""
	}
	return cfg.TapName, cfg.GuestIP, cfg.MAC
}

func bootArgs(cfg *NetworkConfig) string {
	args := "console=ttyS0 reboot=k panic=1 pci=off"
	if cfg == nil {
		return args
	}
	return fmt.Sprintf("%s ip=%s::%s:%s::eth0:off", args, cfg.GuestIP, cfg.HostIP, cfg.Netmask)
}

func configureFirecrackerNetwork(socketPath string, cfg NetworkConfig) error {
	networkJSON, err := json.Marshal(map[string]any{
		"iface_id":      "eth0",
		"guest_mac":     cfg.MAC,
		"host_dev_name": cfg.TapName,
	})
	if err != nil {
		return fmt.Errorf("network config build failed: %v", err)
	}
	return sendFCConfig(socketPath, "PUT", "/network-interfaces/eth0", string(networkJSON))
}

func processExists(pid int) bool {
	return syscall.Kill(pid, 0) == nil
}

func (s *Server) Ps(ctx context.Context, req *proto.PsRequest) (*proto.PsResponse, error) {
	query := "SELECT id, pid, status, image, cpus, memory_mb, COALESCE(guest_ip, ''), COALESCE(mac_address, ''), COALESCE(tap_name, '') FROM vms"
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
		var guestIP, macAddress, tapName string
		var pid, cpus, memoryMb int32

		if err := rows.Scan(&id, &pid, &status, &image, &cpus, &memoryMb, &guestIP, &macAddress, &tapName); err != nil {
			return nil, fmt.Errorf("failed to scan row: %v", err)
		}
		vms = append(vms, &proto.VmDetails{
			VmId:       id,
			Pid:        pid,
			Status:     status,
			Image:      image,
			GuestIp:    guestIP,
			MacAddress: macAddress,
			TapName:    tapName,
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

func (s *Server) StreamLogs(req *proto.LogsRequest, stream grpc.ServerStreamingServer[proto.LogsResponse]) error {
	if req == nil || req.VmId == "" {
		return status.Error(codes.InvalidArgument, "vm id is required")
	}
	if req.Tail < 0 {
		return status.Error(codes.InvalidArgument, "tail must be zero or greater")
	}

	logPath, err := s.logPathForVM(req.VmId)
	if err != nil {
		return err
	}

	tail := int(req.Tail)
	if tail == 0 && !req.Follow {
		tail = 100
	}

	offset, err := sendTail(logPath, tail, stream)
	if err != nil {
		return err
	}
	if !req.Follow {
		return nil
	}
	return followLog(logPath, offset, stream)
}

func (s *Server) logPathForVM(vmID string) (string, error) {
	var logPath string
	err := s.DB.QueryRow(`SELECT log_path FROM vms WHERE id = ?`, vmID).Scan(&logPath)
	if err == sql.ErrNoRows {
		return "", status.Errorf(codes.NotFound, "vm %s not found", vmID)
	}
	if err != nil {
		return "", status.Errorf(codes.Internal, "failed to read vm log path: %v", err)
	}
	if logPath == "" {
		return "", status.Errorf(codes.NotFound, "vm %s has no log file registered", vmID)
	}
	return logPath, nil
}

func sendTail(path string, tail int, stream grpc.ServerStreamingServer[proto.LogsResponse]) (int64, error) {
	lines, offset, err := readTailLines(path, tail)
	if err != nil {
		if os.IsNotExist(err) {
			return 0, status.Errorf(codes.NotFound, "log file not found: %s", path)
		}
		return 0, status.Errorf(codes.Internal, "failed to read log file: %v", err)
	}
	for _, line := range lines {
		if err := sendLogLine(stream, line); err != nil {
			return offset, err
		}
	}
	return offset, nil
}

func readTailLines(path string, tail int) ([]string, int64, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, 0, err
	}
	offset := int64(len(data))
	lines := splitLogLines(string(data))
	if tail > 0 && len(lines) > tail {
		lines = lines[len(lines)-tail:]
	}
	return lines, offset, nil
}

func splitLogLines(data string) []string {
	scanner := bufio.NewScanner(bytes.NewBufferString(data))
	lines := make([]string, 0)
	for scanner.Scan() {
		lines = append(lines, scanner.Text())
	}
	return lines
}

func followLog(path string, offset int64, stream grpc.ServerStreamingServer[proto.LogsResponse]) error {
	ticker := time.NewTicker(250 * time.Millisecond)
	defer ticker.Stop()

	for {
		select {
		case <-stream.Context().Done():
			return stream.Context().Err()
		case <-ticker.C:
			nextOffset, err := sendNewLogLines(path, offset, stream)
			if err != nil {
				return err
			}
			offset = nextOffset
		}
	}
}

func sendNewLogLines(path string, offset int64, stream grpc.ServerStreamingServer[proto.LogsResponse]) (int64, error) {
	file, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			return offset, status.Errorf(codes.NotFound, "log file not found: %s", path)
		}
		return offset, status.Errorf(codes.Internal, "failed to open log file: %v", err)
	}
	defer file.Close()

	info, err := file.Stat()
	if err != nil {
		return offset, status.Errorf(codes.Internal, "failed to stat log file: %v", err)
	}
	if info.Size() < offset {
		offset = 0
	}
	if _, err := file.Seek(offset, io.SeekStart); err != nil {
		return offset, status.Errorf(codes.Internal, "failed to seek log file: %v", err)
	}

	reader := bufio.NewReader(file)
	for {
		line, err := reader.ReadString('\n')
		if line != "" {
			line = strings.TrimRight(line, "\r\n")
			if err := sendLogLine(stream, line); err != nil {
				return offset, err
			}
		}
		current, seekErr := file.Seek(0, io.SeekCurrent)
		if seekErr == nil {
			offset = current
		}
		if err == io.EOF {
			break
		}
		if err != nil {
			return offset, status.Errorf(codes.Internal, "failed to read log file: %v", err)
		}
	}
	return offset, nil
}

func sendLogLine(stream grpc.ServerStreamingServer[proto.LogsResponse], line string) error {
	return stream.Send(&proto.LogsResponse{
		Line:      line,
		Timestamp: timestamppb.Now(),
	})
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
		drive_path TEXT,
		console_path TEXT,
		tap_name TEXT,
		guest_ip TEXT,
		mac_address TEXT,
		created_at DATETIME DEFAULT CURRENT_TIMESTAMP
	);`
	if _, err := db.Exec(query); err != nil {
		return err
	}
	_, _ = db.Exec(`ALTER TABLE vms ADD COLUMN log_path TEXT`)
	_, _ = db.Exec(`ALTER TABLE vms ADD COLUMN drive_path TEXT`)
	_, _ = db.Exec(`ALTER TABLE vms ADD COLUMN console_path TEXT`)
	_, _ = db.Exec(`ALTER TABLE vms ADD COLUMN tap_name TEXT`)
	_, _ = db.Exec(`ALTER TABLE vms ADD COLUMN guest_ip TEXT`)
	_, _ = db.Exec(`ALTER TABLE vms ADD COLUMN mac_address TEXT`)
	return nil
}

func reconcileState(db *sql.DB, network *NetworkManager) error {
	rows, err := db.Query(`SELECT id, pid, COALESCE(tap_name, ''), COALESCE(guest_ip, ''), COALESCE(mac_address, '') FROM vms WHERE status = ? AND pid > 0`, internal.VmRunning)
	if err != nil {
		return err
	}
	defer rows.Close()

	for rows.Next() {
		var id string
		var pid int
		var tapName, guestIP, mac string
		if err := rows.Scan(&id, &pid, &tapName, &guestIP, &mac); err != nil {
			return err
		}
		if !processExists(pid) {
			if network != nil {
				_ = network.Cleanup(NetworkConfig{TapName: tapName, GuestIP: guestIP, MAC: mac})
			}
			_, _ = db.Exec(`UPDATE vms SET status = ?, pid = 0 WHERE id = ?`, internal.VmStopped, id)
		}
	}
	return rows.Err()
}

func main() {
	cfg := LoadConfig()
	store := NewImageStore(cfg)
	network := NewNetworkManager(nil)
	if err := store.Init(); err != nil {
		log.Fatalf("Storage initialization error: %v", err)
	}
	if err := os.MkdirAll(cfg.LogDir, 0755); err != nil {
		log.Fatalf("Log directory error: %v", err)
	}
	if err := applyRuntimePermissions(cfg.LogDir, cfg.RuntimeGroup, 0770); err != nil {
		log.Fatalf("Log directory permission error: %v", err)
	}

	db, err := sql.Open("sqlite", cfg.DBPath)
	if err != nil {
		log.Fatalf("Db creation error: %v", err)
	}
	defer db.Close()
	if err := ensureSchema(db); err != nil {
		log.Fatalf("Table creation error: %v", err)
	}
	if err := reconcileState(db, network); err != nil {
		log.Fatalf("State reconciliation error: %v", err)
	}

	listener, err := net.Listen("tcp", cfg.GRPCAddr)
	if err != nil {
		log.Fatalf("Network error: failed to listen on %s: %v", cfg.GRPCAddr, err)
	}

	log.Printf("fvcd listening on %s with data dir %s", cfg.GRPCAddr, cfg.BaseDir)
	grpcServer := grpc.NewServer()
	proto.RegisterFvcServiceServer(grpcServer, &Server{DB: db, Config: cfg, Store: store, Net: network})
	if err := grpcServer.Serve(listener); err != nil {
		log.Fatalf("gRPC server error: %v", err)
	}
}
