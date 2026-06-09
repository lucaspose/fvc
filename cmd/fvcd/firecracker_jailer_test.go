package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestJailerIDSanitizesVMID(t *testing.T) {
	got := jailerID("vm/../bad id")
	if strings.ContainsAny(got, `/ .`) || !strings.HasPrefix(got, "fvc-") {
		t.Fatalf("unexpected jailer id: %q", got)
	}
}

func TestFirecrackerCommandUsesJailerWhenEnabled(t *testing.T) {
	dir := t.TempDir()
	firecracker := writeExecutable(t, filepath.Join(dir, "firecracker"))
	jailer := writeExecutable(t, filepath.Join(dir, "jailer"))
	server := Server{Config: DaemonConfig{
		RuntimeDir:          filepath.Join(dir, "run"),
		FirecrackerPath:     firecracker,
		JailerPath:          jailer,
		JailerEnabled:       true,
		JailerChrootBaseDir: filepath.Join(dir, "jailer-root"),
		JailerUID:           123,
		JailerGID:           456,
	}}

	cmd, runtime, err := server.firecrackerCommand("vm-1")
	if err != nil {
		t.Fatalf("firecrackerCommand failed: %v", err)
	}
	if cmd.Path != jailer {
		t.Fatalf("expected jailer command, got %s", cmd.Path)
	}
	args := strings.Join(cmd.Args, " ")
	for _, want := range []string{"--id fvc-vm-1", "--exec-file " + firecracker, "--uid 123", "--gid 456", "--chroot-base-dir " + filepath.Join(dir, "jailer-root"), "--api-sock /run/firecracker.socket"} {
		if !strings.Contains(args, want) {
			t.Fatalf("missing jailer arg %q in %q", want, args)
		}
	}
	if !strings.Contains(runtime.SocketPath, filepath.Join("firecracker", "fvc-vm-1", "root", "run", "firecracker.socket")) {
		t.Fatalf("unexpected socket path: %s", runtime.SocketPath)
	}
}

func TestPrepareFirecrackerRuntimeDirectKeepsHostPaths(t *testing.T) {
	server := Server{}
	runtime := firecrackerRuntime{VolumePaths: map[string]string{}}
	volumes := []preparedVolume{{DriveID: "vol0", HostPath: "/host/vol.ext4"}}
	if err := server.prepareFirecrackerRuntime(&runtime, "/host/kernel", "/host/rootfs", "/host/vsock", volumes, true); err != nil {
		t.Fatalf("prepareFirecrackerRuntime failed: %v", err)
	}
	if runtime.KernelPath != "/host/kernel" || runtime.RootDrivePath != "/host/rootfs" || runtime.VSockConfigPath != "/host/vsock" || runtime.VSockHostPath != "/host/vsock" {
		t.Fatalf("unexpected direct runtime paths: %#v", runtime)
	}
	if runtime.VolumePaths["vol0"] != "/host/vol.ext4" {
		t.Fatalf("unexpected volume mapping: %#v", runtime.VolumePaths)
	}
}

func writeExecutable(t *testing.T, path string) string {
	t.Helper()
	if err := os.WriteFile(path, []byte("#!/bin/sh\nexit 0\n"), 0755); err != nil {
		t.Fatalf("write executable failed: %v", err)
	}
	return path
}
