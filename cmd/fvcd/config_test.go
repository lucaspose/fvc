package main

import (
	"path/filepath"
	"testing"
)

func TestLoadConfigUsesFVCHome(t *testing.T) {
	t.Setenv("FVC_HOME", "/tmp/fvc-test")
	t.Setenv("FVC_GRPC_ADDR", "")
	t.Setenv("FVC_FIRECRACKER_PATH", "")
	t.Setenv("FVC_KERNEL_PATH", "")
	t.Setenv("FVC_IMAGE_BASE_URL", "")

	cfg := LoadConfig()

	if cfg.BaseDir != "/tmp/fvc-test" {
		t.Fatalf("expected base dir from FVC_HOME, got %q", cfg.BaseDir)
	}
	if cfg.DBPath != filepath.Join("/tmp/fvc-test", "fvc.db") {
		t.Fatalf("expected DB inside base dir, got %q", cfg.DBPath)
	}
	if cfg.KernelPath != filepath.Join("/tmp/fvc-test", "vmlinux.bin") {
		t.Fatalf("expected kernel inside base dir, got %q", cfg.KernelPath)
	}
}
