package main

import (
	"os"
	"path/filepath"
	"strings"
)

type DaemonConfig struct {
	BaseDir         string
	CacheDir        string
	ActiveDir       string
	SnapshotDir     string
	RuntimeDir      string
	LogDir          string
	DBPath          string
	GRPCAddr        string
	FirecrackerPath string
	KernelPath      string
	ImageBaseURL    string
	NetworkEnabled  bool
	RuntimeGroup    string
}

func LoadConfig() DaemonConfig {
	baseDir := envOrDefault("FVC_HOME", "/var/lib/fvc")

	cfg := DaemonConfig{
		BaseDir:         baseDir,
		CacheDir:        filepath.Join(baseDir, "cache"),
		ActiveDir:       filepath.Join(baseDir, "active"),
		SnapshotDir:     filepath.Join(baseDir, "snapshots"),
		RuntimeDir:      envOrDefault("FVC_RUNTIME_DIR", "/run/fvc"),
		LogDir:          filepath.Join(baseDir, "logs"),
		DBPath:          filepath.Join(baseDir, "fvc.db"),
		GRPCAddr:        envOrDefault("FVC_GRPC_ADDR", "127.0.0.1:50051"),
		FirecrackerPath: envOrDefault("FVC_FIRECRACKER_PATH", "/usr/local/bin/firecracker"),
		ImageBaseURL:    envOrDefault("FVC_IMAGE_BASE_URL", "https://fvchubstorage.blob.core.windows.net/images"),
		NetworkEnabled:  envBoolOrDefault("FVC_NETWORK_ENABLED", true),
		RuntimeGroup:    envOrDefault("FVC_RUNTIME_GROUP", ""),
	}
	cfg.KernelPath = envOrDefault("FVC_KERNEL_PATH", filepath.Join(baseDir, "vmlinux.bin"))

	return cfg
}

func envOrDefault(key, fallback string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return fallback
}

func envBoolOrDefault(key string, fallback bool) bool {
	value := strings.ToLower(strings.TrimSpace(os.Getenv(key)))
	if value == "" {
		return fallback
	}
	return value == "1" || value == "true" || value == "yes" || value == "on"
}
