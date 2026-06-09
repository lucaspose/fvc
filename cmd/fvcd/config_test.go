package main

import (
	"path/filepath"
	"testing"
)

func TestLoadConfigUsesFVCHome(t *testing.T) {
	t.Setenv("FVC_HOME", "/tmp/fvc-test")
	t.Setenv("FVC_GRPC_ADDR", "")
	t.Setenv("FVC_FIRECRACKER_PATH", "")
	t.Setenv("FVC_JAILER_PATH", "")
	t.Setenv("FVC_JAILER_ENABLED", "")
	t.Setenv("FVC_JAILER_CHROOT_BASE_DIR", "")
	t.Setenv("FVC_JAILER_UID", "")
	t.Setenv("FVC_JAILER_GID", "")
	t.Setenv("FVC_KERNEL_PATH", "")
	t.Setenv("FVC_IMAGE_BASE_URL", "")
	t.Setenv("FVC_NETWORK_ENABLED", "")
	t.Setenv("FVC_RUNTIME_GROUP", "")
	t.Setenv("FVC_RUNTIME_DIR", "")
	t.Setenv("FVC_GUEST_AGENT_MODE", "")
	t.Setenv("FVC_STRICT_RUNTIME_CHECKS", "")
	t.Setenv("FVC_ALLOW_REMOTE_TCP", "")
	t.Setenv("FVC_ALLOW_INSECURE_TCP", "")
	t.Setenv("FVC_GRPC_TOKEN", "")
	t.Setenv("FVC_ALLOW_HOST_IMAGE_PATHS", "")
	t.Setenv("FVC_REQUIRE_IMAGE_CHECKSUMS", "")
	t.Setenv("FVC_ALLOW_INSECURE_DOWNLOADS", "")

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
	if cfg.SnapshotDir != filepath.Join("/tmp/fvc-test", "snapshots") {
		t.Fatalf("expected snapshots inside base dir, got %q", cfg.SnapshotDir)
	}
	if cfg.RuntimeDir != "/run/fvc" {
		t.Fatalf("expected runtime dir default, got %q", cfg.RuntimeDir)
	}
	if cfg.GRPCNetwork != "unix" {
		t.Fatalf("expected unix grpc network default, got %q", cfg.GRPCNetwork)
	}
	if cfg.GRPCAddr != filepath.Join("/run/fvc", "fvcd.sock") {
		t.Fatalf("expected unix grpc addr default, got %q", cfg.GRPCAddr)
	}
	if !cfg.NetworkEnabled {
		t.Fatal("expected network to be enabled by default")
	}
	if cfg.RuntimeGroup != "" {
		t.Fatalf("expected empty runtime group by default, got %q", cfg.RuntimeGroup)
	}
	if cfg.GuestAgentMode != "vsock" {
		t.Fatalf("expected vsock guest agent mode by default, got %q", cfg.GuestAgentMode)
	}
	if cfg.JailerEnabled {
		t.Fatal("expected jailer to be disabled by default")
	}
	if cfg.JailerPath != "/usr/local/bin/jailer" {
		t.Fatalf("unexpected jailer path: %q", cfg.JailerPath)
	}
	if cfg.JailerChrootBaseDir != filepath.Join("/tmp/fvc-test", "jailer") {
		t.Fatalf("unexpected jailer chroot base dir: %q", cfg.JailerChrootBaseDir)
	}
	if cfg.JailerUID != 65534 || cfg.JailerGID != 65534 {
		t.Fatalf("unexpected jailer uid/gid: %d/%d", cfg.JailerUID, cfg.JailerGID)
	}
	if cfg.StrictChecks {
		t.Fatal("expected strict runtime checks to be disabled by default")
	}
	if cfg.AllowRemoteTCP {
		t.Fatal("expected remote tcp to be disabled by default")
	}
	if cfg.AllowInsecureTCP {
		t.Fatal("expected insecure tcp to be disabled by default")
	}
	if cfg.GRPCToken != "" {
		t.Fatalf("expected empty grpc token by default, got %q", cfg.GRPCToken)
	}
	if cfg.AllowHostImagePaths {
		t.Fatal("expected arbitrary host image paths to be disabled by default")
	}
	if !cfg.RequireImageChecksums {
		t.Fatal("expected image checksums to be required by default")
	}
	if cfg.AllowInsecureDownloads {
		t.Fatal("expected insecure downloads to be disabled by default")
	}
}

func TestValidateListenConfigRejectsWildcardTCPByDefault(t *testing.T) {
	cfg := DaemonConfig{GRPCNetwork: "tcp", GRPCAddr: "0.0.0.0:50051"}
	if err := validateListenConfig(cfg); err == nil {
		t.Fatal("expected wildcard tcp bind to require explicit opt-in")
	}
}

func TestValidateListenConfigAllowsLoopbackTCP(t *testing.T) {
	cfg := DaemonConfig{GRPCNetwork: "tcp", GRPCAddr: "127.0.0.1:50051", GRPCToken: "secret"}
	if err := validateListenConfig(cfg); err != nil {
		t.Fatalf("expected loopback tcp bind to be allowed: %v", err)
	}
}

func TestValidateListenConfigRejectsTCPWithoutToken(t *testing.T) {
	cfg := DaemonConfig{GRPCNetwork: "tcp", GRPCAddr: "127.0.0.1:50051"}
	if err := validateListenConfig(cfg); err == nil {
		t.Fatal("expected tcp bind to require grpc token")
	}
}
