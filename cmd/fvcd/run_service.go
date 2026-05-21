package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/lucaspose/fvc/internal"
	"github.com/lucaspose/fvc/internal/fcapi"
	"github.com/lucaspose/fvc/internal/vmstore"
	"github.com/lucaspose/fvc/proto"
	"google.golang.org/grpc"
)

type runConfig struct {
	ImageName string
	Name      string
	CPUs      int32
	MemoryMB  int32
	Ports     []string
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
	sendEventProgress := func(stage, eventStatus, message, errorMessage string, current, total int64) error {
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
			Current:      current,
			Total:        total,
		})
	}
	sendEvent := func(stage, eventStatus, message, errorMessage string) error {
		return sendEventProgress(stage, eventStatus, message, errorMessage, 0, 0)
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

	runCfg, err := validateRunRequest(req)
	if err != nil {
		return fail("validate", "invalid request: %v", err)
	}
	if len(runCfg.Ports) == 0 && req.GetConfig().GetPublishAll() {
		ports, err := s.exposedPorts(runCfg.ImageName)
		if err != nil {
			return fail("validate", "image expose lookup failed: %v", err)
		}
		runCfg.Ports = ports
	}
	if runCfg.Name != "" {
		exists, err := s.vmNameExists(runCfg.Name, "")
		if err != nil {
			return fail("validate", "vm name check failed: %v", err)
		}
		if exists {
			return fail("validate", "vm name already exists: %s", runCfg.Name)
		}
	}
	if len(runCfg.Ports) > 0 && !s.Config.NetworkEnabled {
		return fail("validate", "port publishing requires FVC_NETWORK_ENABLED=true")
	}

	if err := step("image", fmt.Sprintf("Resolving image %s", runCfg.ImageName)); err != nil {
		return nil, err
	}
	if _, err := s.Store.PullImageIfNeededProgress(runCfg.ImageName, func(current, total int64) {
		_ = sendEventProgress("image", "running", fmt.Sprintf("Downloading image %s", runCfg.ImageName), "", current, total)
	}); err != nil {
		return fail("image", "image pull failed: %v", err)
	}
	if err := complete("image", "Image ready"); err != nil {
		return nil, err
	}
	runtimeConfig, useRuntimeInit, err := s.imageRuntimeConfig(runCfg.ImageName)
	if err != nil {
		return fail("image", "image metadata lookup failed: %v", err)
	}

	if err := step("kernel", "Resolving kernel"); err != nil {
		return nil, err
	}
	kernelPath, err := s.Store.PullKernelIfNeededProgress(func(current, total int64) {
		_ = sendEventProgress("kernel", "running", "Downloading kernel", "", current, total)
	})
	if err != nil {
		return fail("kernel", "kernel pull failed: %v", err)
	}
	if err := complete("kernel", "Kernel ready"); err != nil {
		return nil, err
	}

	if err := step("rootfs", "Cloning root filesystem"); err != nil {
		return nil, err
	}
	vmDrivePath, err := s.Store.CloneImageProgress(runCfg.ImageName, vmID, func(current, total int64) {
		_ = sendEventProgress("rootfs", "running", "Cloning root filesystem", "", current, total)
	})
	if err != nil {
		return fail("rootfs", "rootfs clone failed: %v", err)
	}
	if err := complete("rootfs", "Root filesystem ready"); err != nil {
		return nil, err
	}
	if useRuntimeInit {
		if err := step("runtime", "Injecting runtime init"); err != nil {
			return nil, err
		}
		if err := s.prepareRuntimeRootfs(vmDrivePath, runtimeConfig); err != nil {
			_ = os.Remove(vmDrivePath)
			return fail("runtime", "runtime rootfs preparation failed: %v", err)
		}
		if err := complete("runtime", "Runtime init ready"); err != nil {
			return nil, err
		}
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
		if len(runCfg.Ports) > 0 {
			if err := s.networkManager().PublishPorts(cfg, runCfg.Ports); err != nil {
				_ = os.Remove(vmDrivePath)
				s.cleanupNetwork(netCfg, runCfg.Ports)
				return fail("network", "port publish failed: %v", err)
			}
		}
		if err := complete("network", fmt.Sprintf("Network ready: %s %s", cfg.TapName, cfg.GuestIP)); err != nil {
			return nil, err
		}
	}

	socketPath := s.runtimePath(fmt.Sprintf("fvc-%s.socket", vmID))
	logPath := filepath.Join(s.Config.LogDir, vmID+".log")
	consolePath := s.runtimePath(fmt.Sprintf("fvc-%s.console.in", vmID))
	vsockPath := s.runtimePath(fmt.Sprintf("fvc-%s.vsock", vmID))
	_ = os.Remove(socketPath)
	_ = os.Remove(consolePath)
	_ = os.Remove(vsockPath)

	logFile, err := os.OpenFile(logPath, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0644)
	if err != nil {
		_ = os.Remove(vmDrivePath)
		s.cleanupNetwork(netCfg, runCfg.Ports)
		return fail("logs", "log file setup failed: %v", err)
	}
	if err := applyRuntimePermissions(logPath, s.Config.RuntimeGroup, 0660); err != nil {
		logFile.Close()
		_ = os.Remove(vmDrivePath)
		s.cleanupNetwork(netCfg, runCfg.Ports)
		return fail("logs", "log file permission setup failed: %v", err)
	}
	defer logFile.Close()
	consoleInput, err := openConsoleInput(consolePath, s.Config.RuntimeGroup)
	if err != nil {
		_ = os.Remove(vmDrivePath)
		s.cleanupNetwork(netCfg, runCfg.Ports)
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
		s.cleanupNetwork(netCfg, runCfg.Ports)
		return fail("firecracker", "firecracker launch failed: %v", err)
	}

	processPid := int32(cmd.Process.Pid)
	processStartTime := processStartTimeValue(int(processPid))
	go s.watchVM(cmd, vmID, socketPath, logPath, consolePath, vsockPath, processPid, netCfg, runCfg.Ports)

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
	agentToken := uuid.NewString()
	if err := fcapi.ConfigureBootSource(socketPath, kernelPath, runtimeBootArgs(netCfg, s.Config.RuntimeRootDev, useRuntimeInit, agentToken, s.Config.GuestAgentMode)); err != nil {
		_ = cmd.Process.Kill()
		return fail("boot", "boot source config failed: %v", err)
	}
	if err := complete("boot", "Boot source configured"); err != nil {
		return nil, err
	}

	if err := step("drive", "Attaching root filesystem"); err != nil {
		return nil, err
	}
	if err := fcapi.ConfigureDrive(socketPath, "rootfs", vmDrivePath, true, false); err != nil {
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
	if useRuntimeInit && shouldConfigureVsock(s.Config.GuestAgentMode) {
		if err := step("vsock", "Attaching guest agent vsock"); err != nil {
			return nil, err
		}
		if err := configureFirecrackerVsock(socketPath, vsockPath, guestAgentCID(vmID)); err != nil {
			_ = cmd.Process.Kill()
			return fail("vsock", "guest agent vsock config failed: %v", err)
		}
		if err := complete("vsock", "Guest agent vsock attached"); err != nil {
			return nil, err
		}
	}

	if err := step("resources", fmt.Sprintf("Configuring resources cpu=%d memory=%dMB", runCfg.CPUs, runCfg.MemoryMB)); err != nil {
		return nil, err
	}
	if err = fcapi.ConfigureMachine(socketPath, runCfg.CPUs, runCfg.MemoryMB); err != nil {
		_ = cmd.Process.Kill()
		return fail("resources", "machine resource config failed: %v", err)
	}
	if err := complete("resources", "Resources configured"); err != nil {
		return nil, err
	}

	if err := step("start", "Starting microVM"); err != nil {
		return nil, err
	}
	if err = fcapi.StartInstance(socketPath); err != nil {
		_ = cmd.Process.Kill()
		return fail("start", "instance start failed: %v", err)
	}

	tapName, guestIP, mac := networkFields(netCfg)
	storedVsockPath := ""
	if useRuntimeInit && shouldConfigureVsock(s.Config.GuestAgentMode) {
		storedVsockPath = vsockPath
	}
	if err := vmstore.InsertRunning(s.DB, vmstore.RunRecord{
		ID:               vmID,
		Name:             runCfg.Name,
		PID:              processPid,
		ProcessStartTime: processStartTime,
		Image:            runCfg.ImageName,
		CPUs:             runCfg.CPUs,
		MemoryMB:         runCfg.MemoryMB,
		Ports:            runCfg.Ports,
		LogPath:          logPath,
		DrivePath:        vmDrivePath,
		ConsolePath:      consolePath,
		Network:          vmstore.NetworkFields{TapName: tapName, GuestIP: guestIP, MAC: mac},
		AgentToken:       agentToken,
		VsockPath:        storedVsockPath,
	}); err != nil {
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

func validateRunRequest(req *proto.RunRequest) (runConfig, error) {
	if req == nil {
		return runConfig{}, fmt.Errorf("request is required")
	}

	imageName := req.Source
	if imageName == "" {
		imageName = "ubuntu"
	}
	if err := internal.ValidateImageRef(imageName); err != nil {
		return runConfig{}, err
	}
	name := strings.TrimSpace(req.Name)
	if err := internal.ValidateVMName(name); err != nil {
		return runConfig{}, err
	}

	cpus := int32(1)
	memoryMb := int32(512)
	var ports []string
	if req.Config != nil {
		cpus = req.Config.Cpus
		memoryMb = req.Config.MemoryMb
		for _, port := range req.Config.Ports {
			normalized, err := internal.NormalizePortSpec(port)
			if err != nil {
				return runConfig{}, fmt.Errorf("port %q: %w", port, err)
			}
			ports = append(ports, normalized)
		}
	}
	if err := internal.ValidateResources(cpus, memoryMb); err != nil {
		return runConfig{}, err
	}
	return runConfig{ImageName: imageName, Name: name, CPUs: cpus, MemoryMB: memoryMb, Ports: ports}, nil
}

func runFailed(format string, args ...any) *proto.RunResponse {
	return &proto.RunResponse{
		Status:       internal.VmFailed,
		ErrorMessage: fmt.Sprintf(format, args...),
	}
}

func exitCodeFromLog(logPath string) int32 {
	if strings.TrimSpace(logPath) == "" {
		return -1
	}
	data, err := os.ReadFile(logPath)
	if err != nil {
		return -1
	}
	lines := strings.Split(string(data), "\n")
	for i := len(lines) - 1; i >= 0; i-- {
		line := strings.TrimSpace(lines[i])
		idx := strings.LastIndex(line, "FVC_EXIT_CODE=")
		if idx == -1 {
			continue
		}
		value := strings.TrimSpace(line[idx+len("FVC_EXIT_CODE="):])
		parsed, err := strconv.ParseInt(value, 10, 32)
		if err != nil {
			continue
		}
		if parsed < 0 {
			return -1
		}
		return int32(parsed)
	}
	return -1
}
