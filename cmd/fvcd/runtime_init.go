package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/lucaspose/fvc/internal/guestruntime"
)

type GuestRuntimeConfig = guestruntime.Config

func runtimeConfigFromMetadata(metadata ImageMetadata) GuestRuntimeConfig {
	return GuestRuntimeConfig{
		Env:     append([]string(nil), metadata.Env...),
		Cmd:     append([]string(nil), metadata.Cmd...),
		Workdir: metadata.Workdir,
	}
}

func requiresRuntimeInit(config GuestRuntimeConfig) bool {
	return guestruntime.RequiresInit(config)
}

func (s *Server) imageRuntimeConfig(imageName string) (GuestRuntimeConfig, bool, error) {
	info, err := s.Store.InspectImage(imageName)
	if err != nil {
		return GuestRuntimeConfig{}, false, err
	}
	config := runtimeConfigFromMetadata(info.Metadata)
	return config, requiresRuntimeInit(config), nil
}

func (s *Server) prepareRuntimeRootfs(drivePath string, config GuestRuntimeConfig) error {
	if !requiresRuntimeInit(config) {
		return nil
	}
	if err := guestruntime.ValidateHostInit(s.Config.RuntimeInitPath); err != nil {
		return err
	}

	buildDir := filepath.Join(s.Config.BaseDir, "runtime")
	if err := os.MkdirAll(buildDir, 0755); err != nil {
		return fmt.Errorf("runtime preparation directory setup failed: %w", err)
	}
	mountDir, err := os.MkdirTemp(buildDir, "mnt-*")
	if err != nil {
		return fmt.Errorf("runtime mount directory create failed: %w", err)
	}
	defer func() {
		_ = os.RemoveAll(mountDir)
	}()

	runner := s.commandRunner()
	if err := runner.Run("mount", "-o", "loop", drivePath, mountDir); err != nil {
		return fmt.Errorf("runtime rootfs mount failed: %w", err)
	}
	mounted := true
	defer func() {
		if mounted {
			_ = runner.Run("umount", mountDir)
		}
	}()

	if err := guestruntime.Install(mountDir, s.Config.RuntimeInitPath, config); err != nil {
		return err
	}

	if err := runner.Run("umount", mountDir); err != nil {
		return fmt.Errorf("runtime rootfs unmount failed: %w", err)
	}
	mounted = false
	return nil
}

func runtimeBootArgs(cfg *NetworkConfig, rootDevice string, useRuntimeInit bool, agentToken, agentMode string) string {
	rootDevice = strings.TrimSpace(rootDevice)
	if rootDevice == "" || !strings.HasPrefix(rootDevice, "/dev/") || strings.ContainsAny(rootDevice, " \t\r\n") {
		rootDevice = "/dev/vda"
	}
	args := bootArgs(cfg) + " root=" + rootDevice + " rw"
	if useRuntimeInit {
		args += " init=" + guestruntime.InitPath
		if token := sanitizeKernelArg(agentToken); token != "" {
			args += " fvc_agent_token=" + token
		}
		if mode := sanitizeKernelArg(agentMode); mode != "" {
			args += " fvc_agent_mode=" + mode
		}
	}
	return args
}

func sanitizeKernelArg(value string) string {
	value = strings.TrimSpace(value)
	if value == "" || strings.ContainsAny(value, " \t\r\n\x00") {
		return ""
	}
	return value
}
