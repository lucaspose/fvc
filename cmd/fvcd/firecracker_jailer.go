package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/lucaspose/fvc/internal/jailfs"
)

const (
	jailerAPIPath    = "/run/firecracker.socket"
	jailerKernelPath = "/kernel.bin"
	jailerRootfsPath = "/rootfs.ext4"
	jailerVsockPath  = "/run/guest-agent.vsock"
)

type firecrackerRuntime struct {
	SocketPath      string
	KernelPath      string
	RootDrivePath   string
	VSockConfigPath string
	VSockHostPath   string
	VolumePaths     map[string]string
	JailerID        string
	JailerRoot      string
	MountTargets    []string
}

func (s *Server) firecrackerCommand(vmID string) (*exec.Cmd, firecrackerRuntime, error) {
	runtime := firecrackerRuntime{
		SocketPath:   s.runtimePath(fmt.Sprintf("fvc-%s.socket", vmID)),
		VolumePaths:  map[string]string{},
		MountTargets: nil,
	}
	if !s.Config.JailerEnabled {
		if err := validateExecutablePath("firecracker", s.Config.FirecrackerPath); err != nil {
			return nil, firecrackerRuntime{}, err
		}
		return exec.Command(s.Config.FirecrackerPath, "--api-sock", runtime.SocketPath), runtime, nil
	}
	if err := validateJailerConfig(s.Config); err != nil {
		return nil, firecrackerRuntime{}, err
	}
	if err := jailfs.EnsureChrootBaseDir(s.Config.JailerChrootBaseDir); err != nil {
		return nil, firecrackerRuntime{}, err
	}
	jailerID := jailerID(vmID)
	jailerRoot := jailerRootPath(s.Config, jailerID)
	runtime.JailerID = jailerID
	runtime.JailerRoot = jailerRoot
	runtime.SocketPath = filepath.Join(jailerRoot, strings.TrimPrefix(jailerAPIPath, "/"))
	cmd := exec.Command(
		s.Config.JailerPath,
		"--id", jailerID,
		"--exec-file", s.Config.FirecrackerPath,
		"--uid", strconv.Itoa(s.Config.JailerUID),
		"--gid", strconv.Itoa(s.Config.JailerGID),
		"--chroot-base-dir", s.Config.JailerChrootBaseDir,
		"--",
		"--api-sock", jailerAPIPath,
	)
	return cmd, runtime, nil
}

func (s *Server) prepareFirecrackerRuntime(runtime *firecrackerRuntime, kernelPath, drivePath, vsockPath string, volumes []preparedVolume, useVsock bool) error {
	if runtime == nil {
		return fmt.Errorf("firecracker runtime is required")
	}
	if runtime.JailerID == "" {
		runtime.KernelPath = kernelPath
		runtime.RootDrivePath = drivePath
		runtime.VSockConfigPath = vsockPath
		runtime.VSockHostPath = vsockPath
		for _, volume := range volumes {
			runtime.VolumePaths[volume.DriveID] = volume.HostPath
		}
		return nil
	}
	if err := waitForJailerRoot(runtime.JailerRoot, 3*time.Second); err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Join(runtime.JailerRoot, "run"), 0755); err != nil {
		return fmt.Errorf("jailer run directory setup failed: %w", err)
	}
	kernelTarget := filepath.Join(runtime.JailerRoot, strings.TrimPrefix(jailerKernelPath, "/"))
	if err := bindMountFile(s.commandRunner(), kernelPath, kernelTarget, s.Config.JailerUID, s.Config.JailerGID, 0440); err != nil {
		return fmt.Errorf("jailer kernel bind failed: %w", err)
	}
	runtime.MountTargets = append(runtime.MountTargets, kernelTarget)
	runtime.KernelPath = jailerKernelPath

	rootTarget := filepath.Join(runtime.JailerRoot, strings.TrimPrefix(jailerRootfsPath, "/"))
	if err := bindMountFile(s.commandRunner(), drivePath, rootTarget, s.Config.JailerUID, s.Config.JailerGID, 0660); err != nil {
		return fmt.Errorf("jailer rootfs bind failed: %w", err)
	}
	runtime.MountTargets = append(runtime.MountTargets, rootTarget)
	runtime.RootDrivePath = jailerRootfsPath

	for i, volume := range volumes {
		guestPath := fmt.Sprintf("/volume-%d.ext4", i)
		target := filepath.Join(runtime.JailerRoot, strings.TrimPrefix(guestPath, "/"))
		mode := os.FileMode(0660)
		if volume.Spec.ReadOnly {
			mode = 0440
		}
		if err := bindMountFile(s.commandRunner(), volume.HostPath, target, s.Config.JailerUID, s.Config.JailerGID, mode); err != nil {
			return fmt.Errorf("jailer volume bind failed for %s: %w", volume.Spec.Name, err)
		}
		runtime.MountTargets = append(runtime.MountTargets, target)
		runtime.VolumePaths[volume.DriveID] = guestPath
	}

	if useVsock {
		runtime.VSockConfigPath = jailerVsockPath
		runtime.VSockHostPath = filepath.Join(runtime.JailerRoot, strings.TrimPrefix(jailerVsockPath, "/"))
		_ = os.Remove(runtime.VSockHostPath)
	} else {
		runtime.VSockConfigPath = ""
		runtime.VSockHostPath = ""
		_ = os.Remove(vsockPath)
	}
	return nil
}

func validateJailerConfig(cfg DaemonConfig) error {
	if err := validateExecutablePath("jailer", cfg.JailerPath); err != nil {
		return err
	}
	if err := validateExecutablePath("firecracker", cfg.FirecrackerPath); err != nil {
		return err
	}
	if strings.TrimSpace(cfg.JailerChrootBaseDir) == "" || !filepath.IsAbs(cfg.JailerChrootBaseDir) {
		return fmt.Errorf("FVC_JAILER_CHROOT_BASE_DIR must be an absolute path")
	}
	if cfg.JailerUID < 0 || cfg.JailerGID < 0 {
		return fmt.Errorf("FVC_JAILER_UID and FVC_JAILER_GID must be zero or greater")
	}
	return nil
}

func bindMountFile(runner CommandRunner, source, target string, uid, gid int, mode os.FileMode) error {
	if runner == nil {
		return fmt.Errorf("command runner is required")
	}
	info, err := os.Lstat(source)
	if err != nil {
		return fmt.Errorf("source stat failed: %w", err)
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("refusing symlink source: %s", source)
	}
	if !info.Mode().IsRegular() {
		return fmt.Errorf("source must be a regular file: %s", source)
	}
	if err := jailfs.PrepareFileAccess(source, uid, gid, mode); err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(target), 0755); err != nil {
		return fmt.Errorf("target parent setup failed: %w", err)
	}
	file, err := os.OpenFile(target, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, mode)
	if err != nil {
		return fmt.Errorf("target placeholder create failed: %w", err)
	}
	if err := file.Close(); err != nil {
		return fmt.Errorf("target placeholder close failed: %w", err)
	}
	if err := os.Chmod(target, mode); err != nil {
		return fmt.Errorf("target chmod failed: %w", err)
	}
	if err := os.Chown(target, uid, gid); err != nil {
		return fmt.Errorf("target chown failed: %w", err)
	}
	if err := runner.Run("mount", "--bind", source, target); err != nil {
		return err
	}
	return nil
}

func waitForJailerRoot(path string, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		info, err := os.Stat(path)
		if err == nil && info.IsDir() {
			return nil
		}
		time.Sleep(25 * time.Millisecond)
	}
	return fmt.Errorf("jailer root not ready after %s: %s", timeout, path)
}

func (s *Server) cleanupJailerRuntime(vmID string) {
	if !s.Config.JailerEnabled {
		return
	}
	jailerID := jailerID(vmID)
	root := jailerRootPath(s.Config, jailerID)
	for _, target := range jailerMountTargets(root) {
		_ = s.commandRunner().Run("umount", target)
	}
	base := filepath.Join(s.Config.JailerChrootBaseDir, "firecracker", jailerID)
	if safeJailerPath(s.Config.JailerChrootBaseDir, base) {
		_ = os.RemoveAll(base)
	}
}

func jailerMountTargets(root string) []string {
	return []string{
		filepath.Join(root, strings.TrimPrefix(jailerKernelPath, "/")),
		filepath.Join(root, strings.TrimPrefix(jailerRootfsPath, "/")),
		filepath.Join(root, "volume-0.ext4"),
		filepath.Join(root, "volume-1.ext4"),
		filepath.Join(root, "volume-2.ext4"),
		filepath.Join(root, "volume-3.ext4"),
		filepath.Join(root, "volume-4.ext4"),
		filepath.Join(root, "volume-5.ext4"),
		filepath.Join(root, "volume-6.ext4"),
		filepath.Join(root, "volume-7.ext4"),
		filepath.Join(root, "volume-8.ext4"),
		filepath.Join(root, "volume-9.ext4"),
		filepath.Join(root, "volume-10.ext4"),
		filepath.Join(root, "volume-11.ext4"),
		filepath.Join(root, "volume-12.ext4"),
		filepath.Join(root, "volume-13.ext4"),
		filepath.Join(root, "volume-14.ext4"),
		filepath.Join(root, "volume-15.ext4"),
		filepath.Join(root, "volume-16.ext4"),
		filepath.Join(root, "volume-17.ext4"),
		filepath.Join(root, "volume-18.ext4"),
		filepath.Join(root, "volume-19.ext4"),
		filepath.Join(root, "volume-20.ext4"),
		filepath.Join(root, "volume-21.ext4"),
		filepath.Join(root, "volume-22.ext4"),
		filepath.Join(root, "volume-23.ext4"),
		filepath.Join(root, "volume-24.ext4"),
	}
}

func jailerID(vmID string) string {
	id := strings.Map(func(r rune) rune {
		if r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '-' || r == '_' {
			return r
		}
		return '-'
	}, vmID)
	return "fvc-" + strings.Trim(id, "-_")
}

func jailerRootPath(cfg DaemonConfig, jailerID string) string {
	return filepath.Join(cfg.JailerChrootBaseDir, "firecracker", jailerID, "root")
}

func safeJailerPath(base, path string) bool {
	base = filepath.Clean(base)
	path = filepath.Clean(path)
	rel, err := filepath.Rel(base, path)
	if err != nil {
		return false
	}
	return rel != "." && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

func (s *Server) findJailedFirecrackerProcess(jailerID string) (*os.Process, int32, string, error) {
	pid, err := jailfs.FindFirecrackerPID("/proc", jailerID, s.Config.JailerUID, jailerRootPath(s.Config, jailerID))
	if err != nil {
		return nil, 0, "", err
	}
	process, err := os.FindProcess(pid)
	if err != nil {
		return nil, 0, "", fmt.Errorf("jailed firecracker process lookup failed for pid %d: %w", pid, err)
	}
	return process, int32(pid), processStartTimeValue(pid), nil
}
