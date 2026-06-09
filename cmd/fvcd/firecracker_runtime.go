package main

import (
	"fmt"
	"os"
	"time"

	"github.com/lucaspose/fvc/internal/fcapi"
)

func (s *Server) launchFirecracker(vmID, kernelPath, drivePath, logPath, consolePath, vsockPath string, cpus, memoryMb int32, netCfg *NetworkConfig, ports []string, volumes []preparedVolume, useRuntimeInit bool, agentToken string) (int32, string, string, error) {
	_ = os.Remove(consolePath)
	_ = os.Remove(vsockPath)
	s.cleanupJailerRuntime(vmID)

	logFile, err := os.OpenFile(logPath, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0644)
	if err != nil {
		return 0, "", "", fmt.Errorf("log file setup failed: %v", err)
	}
	if err := applyRuntimePermissions(logPath, s.Config.RuntimeGroup, 0660); err != nil {
		logFile.Close()
		return 0, "", "", fmt.Errorf("log file permission setup failed: %v", err)
	}
	defer logFile.Close()
	consoleInput, err := openConsoleInput(consolePath, s.Config.RuntimeGroup)
	if err != nil {
		return 0, "", "", fmt.Errorf("console setup failed: %v", err)
	}
	defer consoleInput.Close()

	cmd, fcRuntime, err := s.firecrackerCommand(vmID)
	if err != nil {
		return 0, "", "", err
	}
	_ = os.Remove(fcRuntime.SocketPath)
	cmd.Stdin = consoleInput
	cmd.Stdout = logFile
	cmd.Stderr = logFile
	if err = cmd.Start(); err != nil {
		return 0, "", "", fmt.Errorf("firecracker launch failed: %v", err)
	}

	processPid := int32(cmd.Process.Pid)
	processStartTime := processStartTimeValue(int(processPid))
	useVsock := useRuntimeInit && shouldConfigureVsock(s.Config.GuestAgentMode)
	if err := s.prepareFirecrackerRuntime(&fcRuntime, kernelPath, drivePath, vsockPath, volumes, useVsock); err != nil {
		_ = cmd.Process.Kill()
		s.cleanupJailerRuntime(vmID)
		return 0, "", "", err
	}
	go s.watchVM(cmd, vmID, fcRuntime.SocketPath, logPath, drivePath, consolePath, fcRuntime.VSockHostPath, processPid, netCfg, ports, false)

	if err := waitForSocket(fcRuntime.SocketPath, 3*time.Second); err != nil {
		_ = cmd.Process.Kill()
		return 0, "", "", fmt.Errorf("firecracker API socket not ready: %v", err)
	}

	if err := fcapi.ConfigureBootSource(fcRuntime.SocketPath, fcRuntime.KernelPath, runtimeBootArgs(netCfg, s.Config.RuntimeRootDev, useRuntimeInit, agentToken, s.Config.GuestAgentMode)); err != nil {
		_ = cmd.Process.Kill()
		return 0, "", "", fmt.Errorf("boot source config failed: %v", err)
	}

	if err := fcapi.ConfigureDrive(fcRuntime.SocketPath, "rootfs", fcRuntime.RootDrivePath, true, false); err != nil {
		_ = cmd.Process.Kill()
		return 0, "", "", fmt.Errorf("rootfs drive config failed: %v", err)
	}
	for _, volume := range volumes {
		if err := fcapi.ConfigureDrive(fcRuntime.SocketPath, volume.DriveID, fcRuntime.VolumePaths[volume.DriveID], false, volume.Spec.ReadOnly); err != nil {
			_ = cmd.Process.Kill()
			return 0, "", "", fmt.Errorf("volume drive config failed: %v", err)
		}
	}

	if netCfg != nil {
		if err := configureFirecrackerNetwork(fcRuntime.SocketPath, *netCfg); err != nil {
			_ = cmd.Process.Kill()
			return 0, "", "", fmt.Errorf("network interface config failed: %v", err)
		}
	}
	if useVsock {
		if err := configureFirecrackerVsock(fcRuntime.SocketPath, fcRuntime.VSockConfigPath, guestAgentCID(vmID)); err != nil {
			_ = cmd.Process.Kill()
			return 0, "", "", fmt.Errorf("guest agent vsock config failed: %v", err)
		}
	}

	if err = fcapi.ConfigureMachine(fcRuntime.SocketPath, cpus, memoryMb); err != nil {
		_ = cmd.Process.Kill()
		return 0, "", "", fmt.Errorf("machine resource config failed: %v", err)
	}

	if err = fcapi.StartInstance(fcRuntime.SocketPath); err != nil {
		_ = cmd.Process.Kill()
		return 0, "", "", fmt.Errorf("instance start failed: %v", err)
	}
	storedVsockPath := ""
	if useVsock {
		storedVsockPath = fcRuntime.VSockHostPath
	}
	go s.monitorGuestExit(cmd.Process, vmID, fcRuntime.SocketPath, logPath, drivePath, consolePath, storedVsockPath, processPid, processStartTime, netCfg, ports, false)
	return processPid, processStartTime, storedVsockPath, nil
}
