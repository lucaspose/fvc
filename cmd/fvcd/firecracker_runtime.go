package main

import (
	"fmt"
	"os"
	"os/exec"
	"time"

	"github.com/lucaspose/fvc/internal/fcapi"
)

func (s *Server) launchFirecracker(vmID, kernelPath, drivePath, logPath, consolePath, vsockPath string, cpus, memoryMb int32, netCfg *NetworkConfig, ports []string, useRuntimeInit bool, agentToken string) (int32, string, error) {
	socketPath := s.runtimePath(fmt.Sprintf("fvc-%s.socket", vmID))
	_ = os.Remove(socketPath)
	_ = os.Remove(consolePath)
	_ = os.Remove(vsockPath)

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
	processStartTime := processStartTimeValue(int(processPid))
	go s.watchVM(cmd, vmID, socketPath, logPath, consolePath, vsockPath, processPid, netCfg, ports)

	if err := waitForSocket(socketPath, 3*time.Second); err != nil {
		_ = cmd.Process.Kill()
		return 0, "", fmt.Errorf("firecracker API socket not ready: %v", err)
	}

	if err := fcapi.ConfigureBootSource(socketPath, kernelPath, runtimeBootArgs(netCfg, s.Config.RuntimeRootDev, useRuntimeInit, agentToken, s.Config.GuestAgentMode)); err != nil {
		_ = cmd.Process.Kill()
		return 0, "", fmt.Errorf("boot source config failed: %v", err)
	}

	if err := fcapi.ConfigureDrive(socketPath, "rootfs", drivePath, true, false); err != nil {
		_ = cmd.Process.Kill()
		return 0, "", fmt.Errorf("rootfs drive config failed: %v", err)
	}

	if netCfg != nil {
		if err := configureFirecrackerNetwork(socketPath, *netCfg); err != nil {
			_ = cmd.Process.Kill()
			return 0, "", fmt.Errorf("network interface config failed: %v", err)
		}
	}
	if useRuntimeInit && shouldConfigureVsock(s.Config.GuestAgentMode) {
		if err := configureFirecrackerVsock(socketPath, vsockPath, guestAgentCID(vmID)); err != nil {
			_ = cmd.Process.Kill()
			return 0, "", fmt.Errorf("guest agent vsock config failed: %v", err)
		}
	}

	if err = fcapi.ConfigureMachine(socketPath, cpus, memoryMb); err != nil {
		_ = cmd.Process.Kill()
		return 0, "", fmt.Errorf("machine resource config failed: %v", err)
	}

	if err = fcapi.StartInstance(socketPath); err != nil {
		_ = cmd.Process.Kill()
		return 0, "", fmt.Errorf("instance start failed: %v", err)
	}
	return processPid, processStartTime, nil
}
