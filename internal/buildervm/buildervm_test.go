package buildervm

import (
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
	}, "extra=1")
	for _, want := range []string{"console=ttyS0", "ip=172.16.0.2::172.16.0.1:255.255.255.252::eth0:off", "root=/dev/vda", "init=/init", "extra=1"} {
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
