package main

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

type DaemonConfig struct {
	BaseDir                string
	CacheDir               string
	ActiveDir              string
	SnapshotDir            string
	RuntimeDir             string
	LogDir                 string
	DBPath                 string
	GRPCNetwork            string
	GRPCAddr               string
	FirecrackerPath        string
	JailerPath             string
	JailerEnabled          bool
	JailerChrootBaseDir    string
	JailerUID              int
	JailerGID              int
	KernelPath             string
	ImageBaseURL           string
	NetworkEnabled         bool
	RuntimeGroup           string
	RuntimeInitPath        string
	RuntimeRootDev         string
	GuestAgentMode         string
	StrictChecks           bool
	AllowRemoteTCP         bool
	AllowInsecureTCP       bool
	GRPCToken              string
	AllowHostImagePaths    bool
	RequireImageChecksums  bool
	AllowInsecureDownloads bool
}

func LoadConfig() DaemonConfig {
	baseDir := envOrDefault("FVC_HOME", "/var/lib/fvc")

	cfg := DaemonConfig{
		BaseDir:                baseDir,
		CacheDir:               filepath.Join(baseDir, "cache"),
		ActiveDir:              filepath.Join(baseDir, "active"),
		SnapshotDir:            filepath.Join(baseDir, "snapshots"),
		RuntimeDir:             envOrDefault("FVC_RUNTIME_DIR", "/run/fvc"),
		LogDir:                 filepath.Join(baseDir, "logs"),
		DBPath:                 filepath.Join(baseDir, "fvc.db"),
		GRPCNetwork:            envOrDefault("FVC_GRPC_NETWORK", "unix"),
		GRPCAddr:               envOrDefault("FVC_GRPC_ADDR", filepath.Join(envOrDefault("FVC_RUNTIME_DIR", "/run/fvc"), "fvcd.sock")),
		FirecrackerPath:        envOrDefault("FVC_FIRECRACKER_PATH", "/usr/local/bin/firecracker"),
		JailerPath:             envOrDefault("FVC_JAILER_PATH", "/usr/local/bin/jailer"),
		JailerEnabled:          envBoolOrDefault("FVC_JAILER_ENABLED", false),
		JailerChrootBaseDir:    envOrDefault("FVC_JAILER_CHROOT_BASE_DIR", filepath.Join(baseDir, "jailer")),
		JailerUID:              envConfigIntOrDefault("FVC_JAILER_UID", 65534),
		JailerGID:              envConfigIntOrDefault("FVC_JAILER_GID", 65534),
		ImageBaseURL:           envOrDefault("FVC_IMAGE_BASE_URL", "https://fvchubstorage.blob.core.windows.net/images"),
		NetworkEnabled:         envBoolOrDefault("FVC_NETWORK_ENABLED", true),
		RuntimeGroup:           envOrDefault("FVC_RUNTIME_GROUP", ""),
		RuntimeInitPath:        envOrDefault("FVC_RUNTIME_INIT_PATH", "/usr/local/bin/fvc-init"),
		RuntimeRootDev:         envOrDefault("FVC_RUNTIME_ROOT_DEVICE", "/dev/vda"),
		GuestAgentMode:         envOrDefault("FVC_GUEST_AGENT_MODE", "vsock"),
		StrictChecks:           envBoolOrDefault("FVC_STRICT_RUNTIME_CHECKS", false),
		AllowRemoteTCP:         envBoolOrDefault("FVC_ALLOW_REMOTE_TCP", false),
		AllowInsecureTCP:       envBoolOrDefault("FVC_ALLOW_INSECURE_TCP", false),
		GRPCToken:              envOrDefault("FVC_GRPC_TOKEN", ""),
		AllowHostImagePaths:    envBoolOrDefault("FVC_ALLOW_HOST_IMAGE_PATHS", false),
		RequireImageChecksums:  envBoolOrDefault("FVC_REQUIRE_IMAGE_CHECKSUMS", true),
		AllowInsecureDownloads: envBoolOrDefault("FVC_ALLOW_INSECURE_DOWNLOADS", false),
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

func envConfigIntOrDefault(key string, fallback int) int {
	value := strings.TrimSpace(os.Getenv(key))
	if value == "" {
		return fallback
	}
	parsed, err := strconv.Atoi(value)
	if err != nil {
		return fallback
	}
	return parsed
}
