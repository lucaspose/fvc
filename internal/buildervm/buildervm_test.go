package buildervm

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/lucaspose/fvc/internal/buildplan"
	"github.com/lucaspose/fvc/internal/hostnet"
)

func env(values map[string]string) EnvFunc {
	return func(key string) string {
		return values[key]
	}
}

func TestConfigFromEnvDefaultsAndOverrides(t *testing.T) {
	cfg := ConfigFromEnv(env(map[string]string{
		"FVC_BUILDER_ROOTFS_PATH":                 "/builder.ext4",
		"FVC_FIRECRACKER_PATH":                    "/bin/firecracker",
		"FVC_JAILER_PATH":                         "/bin/jailer",
		"FVC_JAILER_ENABLED":                      "true",
		"FVC_JAILER_CHROOT_BASE_DIR":              "/jailer-root",
		"FVC_JAILER_UID":                          "123",
		"FVC_JAILER_GID":                          "456",
		"FVC_BUILDER_AGENT_PORT":                  "9091",
		"FVC_BUILDER_CPUS":                        "2",
		"FVC_BUILDER_MEMORY_MB":                   "1024",
		"FVC_BUILDER_TARGET_DEVICE":               "/dev/vdc",
		"FVC_BUILDER_TARGET_ROOT":                 "/target",
		"FVC_BUILDER_BOOT_ARGS_EXTRA":             "quiet",
		"FVC_BUILD_AGENT_TOKEN":                   "token",
		"FVC_BUILD_AGENT_REQUEST_TIMEOUT_SECONDS": "7",
	}), "/default-kernel")
	if cfg.BuilderRootfs != "/builder.ext4" || cfg.KernelPath != "/default-kernel" || cfg.FirecrackerPath != "/bin/firecracker" {
		t.Fatalf("unexpected paths: %#v", cfg)
	}
	if cfg.AgentPort != 9091 || cfg.CPUs != 2 || cfg.MemoryMB != 1024 {
		t.Fatalf("unexpected resources: %#v", cfg)
	}
	if !cfg.JailerEnabled || cfg.JailerPath != "/bin/jailer" || cfg.JailerChrootBaseDir != "/jailer-root" || cfg.JailerUID != 123 || cfg.JailerGID != 456 {
		t.Fatalf("unexpected jailer config: %#v", cfg)
	}
	if cfg.TargetDevice != "/dev/vdc" || cfg.TargetRoot != "/target" || cfg.BootArgsExtra != "quiet" || cfg.AgentToken != "token" {
		t.Fatalf("unexpected target/token config: %#v", cfg)
	}
	if cfg.RequestTimeout != 7*time.Second {
		t.Fatalf("unexpected timeout: %s", cfg.RequestTimeout)
	}
}

func TestRequestTimeoutFromEnvFallback(t *testing.T) {
	if got := RequestTimeoutFromEnv(env(map[string]string{"FVC_BUILD_AGENT_REQUEST_TIMEOUT_SECONDS": "0"})); got != time.Hour {
		t.Fatalf("unexpected zero fallback: %s", got)
	}
	if got := RequestTimeoutFromEnv(env(map[string]string{"FVC_BUILD_AGENT_REQUEST_TIMEOUT_SECONDS": "bad"})); got != time.Hour {
		t.Fatalf("unexpected invalid fallback: %s", got)
	}
}

func TestBootArgs(t *testing.T) {
	args := BootArgs(hostnet.NetworkConfig{
		GuestIP: "172.16.0.2",
		HostIP:  "172.16.0.1",
		Netmask: "255.255.255.252",
	}, "token", "extra=1")
	for _, want := range []string{"console=ttyS0", "ip=172.16.0.2::172.16.0.1:255.255.255.252::eth0:off", "root=/dev/vda", "init=/init", "fvc_build_agent_token=token", "extra=1"} {
		if !strings.Contains(args, want) {
			t.Fatalf("boot args missing %q: %s", want, args)
		}
	}
}

func TestRunRequiresBuilderRootfsBeforeNetwork(t *testing.T) {
	called := false
	runner := New(Options{
		BaseDir:       t.TempDir(),
		DefaultKernel: "/missing-kernel",
		Env:           env(map[string]string{}),
		SetupNetwork: func(id string) (hostnet.NetworkConfig, error) {
			called = true
			return hostnet.NetworkConfig{}, nil
		},
	})
	err := runner.Run("/target.ext4", buildplan.Plan{})
	if err == nil || !strings.Contains(err.Error(), "FVC_BUILDER_ROOTFS_PATH") {
		t.Fatalf("expected builder rootfs error, got %v", err)
	}
	if called {
		t.Fatal("network setup should not be called before config validation")
	}
}

func TestEnsureJailerChrootBaseDirCreatesMissingDirectory(t *testing.T) {
	path := filepath.Join(t.TempDir(), "missing", "jailer")
	if err := ensureJailerChrootBaseDir(path); err != nil {
		t.Fatalf("ensureJailerChrootBaseDir failed: %v", err)
	}
	if info, err := os.Stat(path); err != nil || !info.IsDir() {
		t.Fatalf("expected jailer chroot base directory to be created, info=%v err=%v", info, err)
	}
}

func TestPrepareJailerFileAccessUpdatesSourceMode(t *testing.T) {
	path := filepath.Join(t.TempDir(), "rootfs.ext4")
	if err := os.WriteFile(path, []byte("rootfs"), 0600); err != nil {
		t.Fatalf("write source failed: %v", err)
	}
	if err := prepareJailerFileAccess(path, os.Getuid(), os.Getgid(), 0660); err != nil {
		t.Fatalf("prepareJailerFileAccess failed: %v", err)
	}
	if err := prepareJailerFileAccess(path, os.Getuid(), os.Getgid(), 0660); err != nil {
		t.Fatalf("second prepareJailerFileAccess failed: %v", err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat source failed: %v", err)
	}
	if !fileAccessibleByJailer(info, os.Getuid(), os.Getgid(), 0660) {
		t.Fatalf("source is not accessible by jailer uid/gid: mode=%o", info.Mode().Perm())
	}
}
