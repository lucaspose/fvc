package main

import (
	"context"
	"database/sql"
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/google/uuid"
	"github.com/lucaspose/fvc/internal"
	"github.com/lucaspose/fvc/internal/vmstore"
	"github.com/lucaspose/fvc/proto"
)

type runtimeRefreshResult struct {
	Changed     bool
	Status      string
	ExitCode    int32
	AutoRemoved bool
}

func (s *Server) watchVM(cmd *exec.Cmd, id string, sock string, logPath string, drivePath string, consolePath string, vsockPath string, pid int32, netCfg *NetworkConfig, ports []string, autoRemove bool) {
	_ = cmd.Wait()
	log.Printf("vm %s: firecracker process %d stopped", id, pid)
	exitCode := exitCodeFromLog(logPath)
	s.cleanupRuntimeFiles(sock, consolePath, vsockPath)
	s.cleanupJailerRuntime(id)
	s.cleanupNetwork(netCfg, ports)
	_ = vmstore.MarkStoppedIfRunning(s.DB, id, exitCode)
	if autoRemove {
		s.removeAutoRemovedVM(id, drivePath, logPath, consolePath, vsockPath)
	}
}

func (s *Server) monitorGuestExit(process *os.Process, id string, sock string, logPath string, drivePath string, consolePath string, vsockPath string, pid int32, processStartTime string, netCfg *NetworkConfig, ports []string, autoRemove bool) {
	ticker := time.NewTicker(250 * time.Millisecond)
	defer ticker.Stop()
	for range ticker.C {
		exitCode := exitCodeFromLog(logPath)
		if exitCode < 0 {
			if !processMatches(int(pid), processStartTime) {
				return
			}
			continue
		}
		log.Printf("vm %s: guest process exited with code %d", id, exitCode)
		_ = vmstore.MarkStoppedIfRunning(s.DB, id, exitCode)
		if process != nil {
			_ = process.Kill()
		}
		s.cleanupRuntimeFiles(sock, consolePath, vsockPath)
		s.cleanupJailerRuntime(id)
		s.cleanupNetwork(netCfg, ports)
		if autoRemove {
			s.removeAutoRemovedVM(id, drivePath, logPath, consolePath, vsockPath)
		}
		return
	}
}

func (s *Server) refreshVMRuntimeState(id string) (runtimeRefreshResult, error) {
	state, err := vmstore.GetRuntimeState(s.DB, id)
	if err == sql.ErrNoRows {
		return runtimeRefreshResult{}, err
	}
	if err != nil {
		return runtimeRefreshResult{}, fmt.Errorf("runtime state lookup failed: %w", err)
	}
	if state.Status != internal.VmRunning {
		return runtimeRefreshResult{}, nil
	}
	exitCode := exitCodeFromLog(state.LogPath)
	processAlive := state.PID > 0 && processMatches(state.PID, state.ProcessStartTime)
	if exitCode < 0 && processAlive {
		return runtimeRefreshResult{}, nil
	}
	if processAlive && exitCode >= 0 {
		if process, findErr := os.FindProcess(state.PID); findErr == nil {
			_ = process.Kill()
		}
	}
	s.cleanupRuntimeFiles(s.runtimePath(fmt.Sprintf("fvc-%s.socket", id)), state.ConsolePath, state.VsockPath)
	s.cleanupJailerRuntime(id)
	s.cleanupNetwork(networkConfigFromStore(state.Network), state.Network.Ports)
	if err := vmstore.MarkStoppedIfRunning(s.DB, id, exitCode); err != nil {
		return runtimeRefreshResult{}, err
	}
	status := internal.VmStopped
	if exitCode >= 0 {
		status = internal.VmExited
	}
	result := runtimeRefreshResult{Changed: true, Status: status, ExitCode: exitCode}
	if state.AutoRemove {
		s.removeAutoRemovedVM(id, state.DrivePath, state.LogPath, state.ConsolePath, state.VsockPath)
		result.AutoRemoved = true
	}
	return result, nil
}

func (s *Server) cleanupRuntimeFiles(socketPath, consolePath, vsockPath string) {
	if socketPath != "" {
		_ = os.Remove(socketPath)
	}
	if consolePath != "" {
		_ = os.Remove(consolePath)
	}
	if vsockPath != "" {
		_ = os.Remove(vsockPath)
	}
}

func (s *Server) removeAutoRemovedVM(id, drivePath, logPath, consolePath, vsockPath string) {
	if drivePath != "" {
		_ = os.Remove(drivePath)
	}
	if logPath != "" {
		_ = os.Remove(logPath)
	}
	s.cleanupRuntimeFiles(s.runtimePath(fmt.Sprintf("fvc-%s.socket", id)), consolePath, vsockPath)
	s.cleanupJailerRuntime(id)
	if err := vmstore.Delete(s.DB, id); err != nil {
		log.Printf("vm %s: auto-remove state cleanup failed: %v", id, err)
	}
}

func (s *Server) Stop(ctx context.Context, req *proto.StopRequest) (*proto.StopResponse, error) {
	if req == nil || req.VmId == "" {
		return &proto.StopResponse{Success: false, Message: "vm id is required"}, nil
	}
	vmID, err := s.resolveVMRef(req.VmId)
	if err == sql.ErrNoRows {
		return &proto.StopResponse{Success: false, Message: fmt.Sprintf("vm not found: %s", req.VmId)}, nil
	}
	if err != nil {
		return &proto.StopResponse{Success: false, Message: fmt.Sprintf("vm lookup failed: %v", err)}, nil
	}
	if _, err := s.refreshVMRuntimeState(vmID); err != nil && err != sql.ErrNoRows {
		return &proto.StopResponse{Success: false, Message: err.Error()}, nil
	}
	state, err := vmstore.GetStopState(s.DB, vmID)
	if err != nil {
		return &proto.StopResponse{
			Success: false,
			Message: fmt.Sprintf("vm not found: %v", err),
		}, nil
	}
	if state.Status == internal.VmStopped || state.Status == internal.VmDown || state.PID == 0 || !processMatches(state.PID, state.ProcessStartTime) {
		s.cleanupNetwork(networkConfigFromStore(state.Network), state.Network.Ports)
		s.cleanupJailerRuntime(vmID)
		if state.VsockPath != "" {
			_ = os.Remove(state.VsockPath)
		}
		_ = vmstore.MarkStaleStopped(s.DB, vmID)
		if state.AutoRemove {
			s.removeAutoRemovedVM(vmID, state.DrivePath, state.LogPath, state.ConsolePath, state.VsockPath)
		}
		return &proto.StopResponse{
			Success: true,
			Message: "microVM is already stopped",
		}, nil
	}
	process, err := os.FindProcess(state.PID)
	if err != nil {
		return &proto.StopResponse{
			Success: false,
			Message: fmt.Sprintf("process lookup failed for pid %d: %v", state.PID, err),
		}, nil
	}

	if err := process.Signal(syscall.SIGTERM); err != nil {
		log.Printf("stop %s: SIGTERM failed for pid %d: %v", req.VmId, state.PID, err)
	}

	timeout := time.Duration(req.TimeoutSeconds) * time.Second
	if timeout <= 0 {
		timeout = 10 * time.Second
	}

	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if !processMatches(state.PID, state.ProcessStartTime) {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	if processMatches(state.PID, state.ProcessStartTime) {
		_ = process.Kill()
	}
	s.cleanupNetwork(networkConfigFromStore(state.Network), state.Network.Ports)
	s.cleanupJailerRuntime(vmID)
	if state.VsockPath != "" {
		_ = os.Remove(state.VsockPath)
	}

	if err := vmstore.MarkStopped(s.DB, vmID); err != nil {
		return &proto.StopResponse{
			Success: false,
			Message: fmt.Sprintf("process stopped but state update failed: %v", err),
		}, nil
	}
	if state.AutoRemove {
		s.removeAutoRemovedVM(vmID, state.DrivePath, state.LogPath, state.ConsolePath, state.VsockPath)
	}
	return &proto.StopResponse{
		Success: true,
		Message: fmt.Sprintf("microVM stopped: %s", vmID),
	}, nil
}

func (s *Server) Kill(ctx context.Context, req *proto.KillRequest) (*proto.KillResponse, error) {
	if req == nil || req.VmId == "" {
		return &proto.KillResponse{Success: false, Message: "vm id is required"}, nil
	}
	vmID, err := s.resolveVMRef(req.VmId)
	if err == sql.ErrNoRows {
		return &proto.KillResponse{Success: false, Message: fmt.Sprintf("vm not found: %s", req.VmId)}, nil
	}
	if err != nil {
		return &proto.KillResponse{Success: false, Message: fmt.Sprintf("vm lookup failed: %v", err)}, nil
	}
	if _, err := s.refreshVMRuntimeState(vmID); err != nil && err != sql.ErrNoRows {
		return &proto.KillResponse{Success: false, Message: err.Error()}, nil
	}

	state, err := vmstore.GetKillState(s.DB, vmID)
	if err == sql.ErrNoRows {
		return &proto.KillResponse{Success: false, Message: fmt.Sprintf("vm not found: %s", req.VmId)}, nil
	}
	if err != nil {
		return &proto.KillResponse{Success: false, Message: fmt.Sprintf("state lookup failed: %v", err)}, nil
	}
	if state.Status != internal.VmRunning || state.PID == 0 || !processMatches(state.PID, state.ProcessStartTime) {
		s.cleanupNetwork(networkConfigFromStore(state.Network), state.Network.Ports)
		s.cleanupJailerRuntime(vmID)
		_ = vmstore.MarkStaleStopped(s.DB, vmID)
		if state.AutoRemove {
			s.removeAutoRemovedVM(vmID, state.DrivePath, state.LogPath, state.ConsolePath, state.VsockPath)
		}
		return &proto.KillResponse{Success: true, Message: "microVM is already stopped"}, nil
	}

	process, err := os.FindProcess(state.PID)
	if err != nil {
		return &proto.KillResponse{Success: false, Message: fmt.Sprintf("process lookup failed for pid %d: %v", state.PID, err)}, nil
	}
	if err := process.Kill(); err != nil {
		log.Printf("kill %s: SIGKILL failed for pid %d: %v", vmID, state.PID, err)
	}
	for i := 0; i < 20 && processMatches(state.PID, state.ProcessStartTime); i++ {
		time.Sleep(50 * time.Millisecond)
	}

	s.cleanupNetwork(networkConfigFromStore(state.Network), state.Network.Ports)
	s.cleanupJailerRuntime(vmID)
	_ = os.Remove(s.runtimePath(fmt.Sprintf("fvc-%s.socket", vmID)))
	if state.ConsolePath != "" {
		_ = os.Remove(state.ConsolePath)
	}
	if state.VsockPath != "" {
		_ = os.Remove(state.VsockPath)
	}
	if err := vmstore.MarkStopped(s.DB, vmID); err != nil {
		return &proto.KillResponse{Success: false, Message: fmt.Sprintf("process killed but state update failed: %v", err)}, nil
	}
	if state.AutoRemove {
		s.removeAutoRemovedVM(vmID, state.DrivePath, state.LogPath, state.ConsolePath, state.VsockPath)
	}
	return &proto.KillResponse{Success: true, Message: fmt.Sprintf("microVM killed: %s", vmID)}, nil
}

func (s *Server) Start(ctx context.Context, req *proto.StartRequest) (*proto.StartResponse, error) {
	if req == nil || req.VmId == "" {
		return &proto.StartResponse{Success: false, Message: "vm id is required"}, nil
	}
	vmID, err := s.resolveVMRef(req.VmId)
	if err == sql.ErrNoRows {
		return &proto.StartResponse{Success: false, Message: fmt.Sprintf("vm not found: %s", req.VmId)}, nil
	}
	if err != nil {
		return &proto.StartResponse{Success: false, Message: fmt.Sprintf("vm lookup failed: %v", err)}, nil
	}
	if _, err := s.refreshVMRuntimeState(vmID); err != nil && err != sql.ErrNoRows {
		return &proto.StartResponse{Success: false, Message: err.Error()}, nil
	}

	state, err := vmstore.GetStartState(s.DB, vmID)
	if err == sql.ErrNoRows {
		return &proto.StartResponse{Success: false, Message: fmt.Sprintf("vm not found: %s", req.VmId)}, nil
	}
	if err != nil {
		return &proto.StartResponse{Success: false, Message: fmt.Sprintf("state lookup failed: %v", err)}, nil
	}
	if state.Status == internal.VmRunning {
		return &proto.StartResponse{Success: true, Message: fmt.Sprintf("microVM already running: %s", vmID)}, nil
	}
	if state.DrivePath == "" {
		return &proto.StartResponse{Success: false, Message: "microVM cannot be restarted: missing drive path"}, nil
	}
	if _, err := os.Stat(state.DrivePath); err != nil {
		return &proto.StartResponse{Success: false, Message: fmt.Sprintf("microVM cannot be restarted: drive unavailable: %v", err)}, nil
	}
	networkMode := effectiveNetworkMode(state.NetworkMode, s.Config.NetworkEnabled)
	if len(state.Ports) > 0 && networkMode != internal.NetworkModeNAT {
		return &proto.StartResponse{Success: false, Message: "port publishing requires network mode nat"}, nil
	}
	if networkMode == internal.NetworkModeNAT && !s.Config.NetworkEnabled {
		return &proto.StartResponse{Success: false, Message: "network mode nat requires FVC_NETWORK_ENABLED=true"}, nil
	}
	if strings.TrimSpace(state.AgentToken) == "" {
		state.AgentToken = uuid.NewString()
	}

	kernelPath, err := s.Store.PullKernelIfNeeded()
	if err != nil {
		return &proto.StartResponse{Success: false, Message: fmt.Sprintf("kernel pull failed: %v", err)}, nil
	}
	volumeSpecs, err := parseVolumeSpecs(state.Volumes)
	if err != nil {
		return &proto.StartResponse{Success: false, Message: fmt.Sprintf("volume config failed: %v", err)}, nil
	}
	preparedVolumes, err := s.prepareVolumeDrives(volumeSpecs)
	if err != nil {
		return &proto.StartResponse{Success: false, Message: fmt.Sprintf("volume setup failed: %v", err)}, nil
	}
	runtimeConfig, useRuntimeInit, err := s.imageRuntimeConfig(state.Image)
	if err != nil {
		return &proto.StartResponse{Success: false, Message: fmt.Sprintf("image metadata lookup failed: %v", err)}, nil
	}
	if len(preparedVolumes) > 0 {
		runtimeConfig.Volumes = runtimeVolumes(preparedVolumes)
		useRuntimeInit = true
		if err := s.prepareRuntimeRootfs(state.DrivePath, runtimeConfig); err != nil {
			return &proto.StartResponse{Success: false, Message: fmt.Sprintf("runtime rootfs preparation failed: %v", err)}, nil
		}
	}
	var netCfg *NetworkConfig
	if networkMode == internal.NetworkModeNAT {
		cfg, err := s.networkManager().Setup(vmID)
		if err != nil {
			return &proto.StartResponse{Success: false, Message: fmt.Sprintf("network setup failed: %v", err)}, nil
		}
		netCfg = &cfg
		if err := s.networkManager().PublishPorts(cfg, state.Ports); err != nil {
			s.cleanupNetwork(netCfg, state.Ports)
			return &proto.StartResponse{Success: false, Message: fmt.Sprintf("port publish failed: %v", err)}, nil
		}
	}
	consolePath := s.runtimePath(fmt.Sprintf("fvc-%s.console.in", vmID))
	vsockPath := s.runtimePath(fmt.Sprintf("fvc-%s.vsock", vmID))
	pid, processStartTime, storedVsockPath, err := s.launchFirecracker(vmID, kernelPath, state.DrivePath, state.LogPath, consolePath, vsockPath, state.CPUs, state.MemoryMB, netCfg, state.Ports, preparedVolumes, useRuntimeInit, state.AgentToken)
	if err != nil {
		s.cleanupNetwork(netCfg, state.Ports)
		return &proto.StartResponse{Success: false, Message: err.Error()}, nil
	}

	tapName, guestIP, mac := networkFields(netCfg)
	if err := vmstore.MarkRunning(s.DB, vmstore.RunningUpdate{
		ID:               vmID,
		PID:              pid,
		ProcessStartTime: processStartTime,
		ConsolePath:      consolePath,
		Network:          vmstore.NetworkFields{TapName: tapName, GuestIP: guestIP, MAC: mac},
		AgentToken:       state.AgentToken,
		VsockPath:        storedVsockPath,
	}); err != nil {
		process, findErr := os.FindProcess(int(pid))
		if findErr == nil {
			_ = process.Kill()
		}
		return &proto.StartResponse{Success: false, Message: fmt.Sprintf("state update failed: %v", err)}, nil
	}
	log.Printf("start %s: restarted successfully", vmID)
	return &proto.StartResponse{Success: true, Message: fmt.Sprintf("microVM started: %s", vmID)}, nil
}

func (s *Server) Rm(ctx context.Context, req *proto.RmRequest) (*proto.RmResponse, error) {
	if req == nil || req.VmId == "" {
		return &proto.RmResponse{Success: false, Message: "vm id is required"}, nil
	}
	vmID, err := s.resolveVMRef(req.VmId)
	if err == sql.ErrNoRows {
		return &proto.RmResponse{Success: false, Message: fmt.Sprintf("vm not found: %s", req.VmId)}, nil
	}
	if err != nil {
		return &proto.RmResponse{Success: false, Message: fmt.Sprintf("vm lookup failed: %v", err)}, nil
	}
	if _, err := s.refreshVMRuntimeState(vmID); err != nil && err != sql.ErrNoRows {
		return &proto.RmResponse{Success: false, Message: err.Error()}, nil
	}

	state, err := vmstore.GetRemoveState(s.DB, vmID)
	if err == sql.ErrNoRows {
		return &proto.RmResponse{Success: false, Message: fmt.Sprintf("vm not found: %s", req.VmId)}, nil
	}
	if err != nil {
		return &proto.RmResponse{Success: false, Message: fmt.Sprintf("state lookup failed: %v", err)}, nil
	}
	if state.Status == internal.VmRunning && state.PID > 0 {
		return &proto.RmResponse{Success: false, Message: "cannot remove a running microVM; stop it first"}, nil
	}

	s.cleanupNetwork(networkConfigFromStore(state.Network), state.Network.Ports)
	s.cleanupJailerRuntime(vmID)
	if state.DrivePath != "" {
		_ = os.Remove(state.DrivePath)
	}
	if state.LogPath != "" {
		_ = os.Remove(state.LogPath)
	}
	if state.ConsolePath != "" {
		_ = os.Remove(state.ConsolePath)
	}
	if state.VsockPath != "" {
		_ = os.Remove(state.VsockPath)
	}
	if err := vmstore.Delete(s.DB, vmID); err != nil {
		return &proto.RmResponse{Success: false, Message: fmt.Sprintf("state delete failed: %v", err)}, nil
	}
	return &proto.RmResponse{Success: true, Message: fmt.Sprintf("microVM removed: %s", vmID)}, nil
}

func (s *Server) Rename(ctx context.Context, req *proto.RenameRequest) (*proto.RenameResponse, error) {
	if req == nil || req.VmId == "" {
		return &proto.RenameResponse{Success: false, Message: "vm id is required"}, nil
	}
	name := strings.TrimSpace(req.Name)
	if name == "" {
		return &proto.RenameResponse{Success: false, Message: "new name is required"}, nil
	}
	if err := internal.ValidateVMName(name); err != nil {
		return &proto.RenameResponse{Success: false, Message: fmt.Sprintf("invalid vm name: %v", err)}, nil
	}
	vmID, err := s.resolveVMRef(req.VmId)
	if err == sql.ErrNoRows {
		return &proto.RenameResponse{Success: false, Message: fmt.Sprintf("vm not found: %s", req.VmId)}, nil
	}
	if err != nil {
		return &proto.RenameResponse{Success: false, Message: fmt.Sprintf("vm lookup failed: %v", err)}, nil
	}
	if exists, err := s.vmNameExists(name, vmID); err != nil {
		return &proto.RenameResponse{Success: false, Message: fmt.Sprintf("vm name check failed: %v", err)}, nil
	} else if exists {
		return &proto.RenameResponse{Success: false, Message: fmt.Sprintf("vm name already exists: %s", name)}, nil
	}
	if err := vmstore.Rename(s.DB, vmID, name); err != nil {
		return &proto.RenameResponse{Success: false, Message: fmt.Sprintf("rename failed: %v", err)}, nil
	}
	return &proto.RenameResponse{Success: true, Message: fmt.Sprintf("microVM renamed: %s -> %s", vmID, name)}, nil
}

func (s *Server) Update(ctx context.Context, req *proto.UpdateRequest) (*proto.UpdateResponse, error) {
	if req == nil || req.VmId == "" {
		return &proto.UpdateResponse{Success: false, Message: "vm id is required"}, nil
	}
	if req.Cpus == 0 && req.MemoryMb == 0 {
		return &proto.UpdateResponse{Success: false, Message: "nothing to update; set --cpu or --ram"}, nil
	}
	vmID, err := s.resolveVMRef(req.VmId)
	if err == sql.ErrNoRows {
		return &proto.UpdateResponse{Success: false, Message: fmt.Sprintf("vm not found: %s", req.VmId)}, nil
	}
	if err != nil {
		return &proto.UpdateResponse{Success: false, Message: fmt.Sprintf("vm lookup failed: %v", err)}, nil
	}
	if _, err := s.refreshVMRuntimeState(vmID); err != nil && err != sql.ErrNoRows {
		return &proto.UpdateResponse{Success: false, Message: err.Error()}, nil
	}
	details, err := vmstore.GetDetails(s.DB, vmID)
	if err == sql.ErrNoRows {
		return &proto.UpdateResponse{Success: false, Message: fmt.Sprintf("vm not found: %s", req.VmId)}, nil
	}
	if err != nil {
		return &proto.UpdateResponse{Success: false, Message: fmt.Sprintf("state lookup failed: %v", err)}, nil
	}
	if details.Status == internal.VmRunning {
		return &proto.UpdateResponse{Success: false, Message: "cannot update resources for a running microVM; stop it first"}, nil
	}
	cpus := details.CPUs
	memoryMB := details.MemoryMB
	if req.Cpus != 0 {
		cpus = req.Cpus
	}
	if req.MemoryMb != 0 {
		memoryMB = req.MemoryMb
	}
	if err := internal.ValidateResources(cpus, memoryMB); err != nil {
		return &proto.UpdateResponse{Success: false, Message: fmt.Sprintf("invalid resources: %v", err)}, nil
	}
	if err := vmstore.UpdateResources(s.DB, vmID, cpus, memoryMB); err != nil {
		return &proto.UpdateResponse{Success: false, Message: fmt.Sprintf("resource update failed: %v", err)}, nil
	}
	return &proto.UpdateResponse{Success: true, Message: fmt.Sprintf("microVM updated: %s cpu=%d ram=%dMB", vmID, cpus, memoryMB)}, nil
}

func (s *Server) ConsoleInfo(ctx context.Context, req *proto.ConsoleInfoRequest) (*proto.ConsoleInfoResponse, error) {
	if req == nil || req.VmId == "" {
		return &proto.ConsoleInfoResponse{Success: false, Message: "vm id is required"}, nil
	}
	vmID, err := s.resolveVMRef(req.VmId)
	if err == sql.ErrNoRows {
		return &proto.ConsoleInfoResponse{Success: false, Message: fmt.Sprintf("vm not found: %s", req.VmId)}, nil
	}
	if err != nil {
		return &proto.ConsoleInfoResponse{Success: false, Message: fmt.Sprintf("vm lookup failed: %v", err)}, nil
	}
	if _, err := s.refreshVMRuntimeState(vmID); err != nil && err != sql.ErrNoRows {
		return &proto.ConsoleInfoResponse{Success: false, Message: err.Error()}, nil
	}

	state, err := vmstore.GetConsoleState(s.DB, vmID)
	if err == sql.ErrNoRows {
		return &proto.ConsoleInfoResponse{Success: false, Message: fmt.Sprintf("vm not found: %s", req.VmId)}, nil
	}
	if err != nil {
		return &proto.ConsoleInfoResponse{Success: false, Message: fmt.Sprintf("state lookup failed: %v", err)}, nil
	}
	if state.Status != internal.VmRunning {
		return &proto.ConsoleInfoResponse{Success: false, Message: fmt.Sprintf("microVM is not running: %s", state.Status)}, nil
	}
	if state.LogPath == "" || state.ConsolePath == "" {
		return &proto.ConsoleInfoResponse{Success: false, Message: "console is not available for this microVM"}, nil
	}
	if err := applyRuntimePermissions(state.LogPath, s.Config.RuntimeGroup, 0660); err != nil {
		return &proto.ConsoleInfoResponse{Success: false, Message: fmt.Sprintf("console log permission repair failed: %v", err)}, nil
	}
	if err := applyRuntimePermissions(state.ConsolePath, s.Config.RuntimeGroup, 0660); err != nil {
		return &proto.ConsoleInfoResponse{Success: false, Message: fmt.Sprintf("console input permission repair failed: %v", err)}, nil
	}
	return &proto.ConsoleInfoResponse{
		Success:   true,
		Message:   "console ready",
		VmId:      vmID,
		LogPath:   state.LogPath,
		InputPath: state.ConsolePath,
	}, nil
}

func (s *Server) networkManager() *NetworkManager {
	if s.Net != nil {
		return s.Net
	}
	return NewNetworkManager(nil)
}

func (s *Server) commandRunner() CommandRunner {
	if s.Runner != nil {
		return s.Runner
	}
	return realCommandRunner{}
}

func (s *Server) runtimePath(name string) string {
	if s.Config.RuntimeDir == "" {
		return filepath.Join(os.TempDir(), name)
	}
	return filepath.Join(s.Config.RuntimeDir, name)
}

func (s *Server) cleanupNetwork(cfg *NetworkConfig, ports ...[]string) {
	if cfg == nil {
		return
	}
	if len(ports) > 0 {
		_ = s.networkManager().CleanupPublishedPorts(*cfg, ports[0])
	}
	_ = s.networkManager().Cleanup(*cfg)
}

func networkFields(cfg *NetworkConfig) (string, string, string) {
	if cfg == nil {
		return "", "", ""
	}
	return cfg.TapName, cfg.GuestIP, cfg.MAC
}

func networkConfigFromStore(fields vmstore.NetworkFields) *NetworkConfig {
	return &NetworkConfig{TapName: fields.TapName, GuestIP: fields.GuestIP, MAC: fields.MAC}
}

func (s *Server) resolveVMRef(ref string) (string, error) {
	return vmstore.ResolveRef(s.DB, ref)
}

func (s *Server) vmNameExists(name, ignoreID string) (bool, error) {
	return vmstore.NameExists(s.DB, name, ignoreID)
}

func (s *Server) exposedPorts(imageName string) ([]string, error) {
	info, err := s.Store.InspectImage(imageName)
	if err != nil {
		return nil, err
	}
	ports := make([]string, 0, len(info.Metadata.ExposedPorts))
	for _, port := range info.Metadata.ExposedPorts {
		if port > 0 && port <= 65535 {
			ports = append(ports, fmt.Sprintf("%d:%d", port, port))
		}
	}
	return ports, nil
}
