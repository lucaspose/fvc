package buildervm

import (
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/google/uuid"
	"github.com/lucaspose/fvc/internal/buildagent"
	"github.com/lucaspose/fvc/internal/buildplan"
	"github.com/lucaspose/fvc/internal/fcapi"
	"github.com/lucaspose/fvc/internal/hostnet"
)

// EnvFunc reads environment-like configuration values.
type EnvFunc func(key string) string

// NetworkSetupFunc allocates a host network for a builder VM.
type NetworkSetupFunc func(id string) (hostnet.NetworkConfig, error)

// NetworkCleanupFunc releases a host network allocated for a builder VM.
type NetworkCleanupFunc func(hostnet.NetworkConfig)

// Options wires daemon-specific dependencies into the builder VM backend.
type Options struct {
	BaseDir        string
	DefaultKernel  string
	SetupNetwork   NetworkSetupFunc
	CleanupNetwork NetworkCleanupFunc
	Env            EnvFunc
	Output         io.Writer
}

// Config is the resolved builder VM runtime configuration.
type Config struct {
	BuilderRootfs   string
	KernelPath      string
	FirecrackerPath string
	AgentPort       int
	CPUs            int32
	MemoryMB        int32
	TargetDevice    string
	TargetRoot      string
	BootArgsExtra   string
	AgentToken      string
	RequestTimeout  time.Duration
}

// Runner executes build plans in a Firecracker-backed builder VM.
type Runner struct {
	opts Options
}

// New creates a builder VM runner.
func New(opts Options) *Runner {
	return &Runner{opts: opts}
}

// Run starts a builder VM and asks its agent to apply plan to targetImage.
func (r *Runner) Run(targetImage string, plan buildplan.Plan) error {
	cfg := r.Config()
	if cfg.BuilderRootfs == "" {
		return fmt.Errorf("FVC_BUILDER_ROOTFS_PATH is required for FVC_BUILD_BACKEND=microvm")
	}
	for label, path := range map[string]string{"builder rootfs": cfg.BuilderRootfs, "builder kernel": cfg.KernelPath, "target image": targetImage, "firecracker": cfg.FirecrackerPath} {
		if _, err := os.Stat(path); err != nil {
			return fmt.Errorf("%s unavailable at %s: %w", label, path, err)
		}
	}
	if r.opts.SetupNetwork == nil {
		return fmt.Errorf("builder network setup is not configured")
	}

	buildID := "build-" + uuid.New().String()
	buildDir := filepath.Join(r.opts.BaseDir, "build")
	if err := os.MkdirAll(buildDir, 0755); err != nil {
		return fmt.Errorf("build directory setup failed: %w", err)
	}
	socketPath := filepath.Join(buildDir, "fvc-"+buildID+".socket")
	logPath := filepath.Join(buildDir, "fvc-"+buildID+".log")
	_ = os.Remove(socketPath)

	netCfg, err := r.opts.SetupNetwork(buildID)
	if err != nil {
		return fmt.Errorf("builder network setup failed: %w", err)
	}
	defer func() {
		if r.opts.CleanupNetwork != nil {
			r.opts.CleanupNetwork(netCfg)
		}
	}()

	logFile, err := os.OpenFile(logPath, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0644)
	if err != nil {
		return fmt.Errorf("builder log setup failed: %w", err)
	}
	defer logFile.Close()

	cmd := exec.Command(cfg.FirecrackerPath, "--api-sock", socketPath)
	cmd.Stdout = logFile
	cmd.Stderr = logFile
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("builder firecracker launch failed: %w", err)
	}
	defer func() {
		_ = cmd.Process.Signal(syscall.SIGTERM)
		time.Sleep(100 * time.Millisecond)
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
		_ = os.Remove(socketPath)
	}()
	if err := fcapi.WaitForSocket(socketPath, 3*time.Second); err != nil {
		return fmt.Errorf("builder firecracker API socket not ready: %w", err)
	}
	if err := ConfigureFirecracker(socketPath, cfg, targetImage, netCfg); err != nil {
		return err
	}
	agentURL := fmt.Sprintf("http://%s:%d", netCfg.GuestIP, cfg.AgentPort)
	if err := buildagent.WaitHealthy(agentURL, 60*time.Second); err != nil {
		return fmt.Errorf("builder agent unavailable: %w; see %s", err, logPath)
	}
	agentPlan := buildagent.FromBuildPlan(cfg.TargetRoot, plan)
	agentPlan.TargetDevice = cfg.TargetDevice
	agentPlan.MountFSType = "ext4"
	if err := buildagent.PostPlan(agentURL, agentPlan, buildagent.PostOptions{
		Token:   cfg.AgentToken,
		Timeout: cfg.RequestTimeout,
		Output:  r.opts.Output,
	}); err != nil {
		return fmt.Errorf("builder agent run failed: %w; see %s", err, logPath)
	}
	return nil
}

// Config resolves builder VM settings from options and environment.
func (r *Runner) Config() Config {
	return ConfigFromEnv(r.env, r.opts.DefaultKernel)
}

// ConfigFromEnv resolves builder VM settings from env.
func ConfigFromEnv(env EnvFunc, defaultKernel string) Config {
	if env == nil {
		env = os.Getenv
	}
	return Config{
		BuilderRootfs:   strings.TrimSpace(env("FVC_BUILDER_ROOTFS_PATH")),
		KernelPath:      envOrDefault(env, "FVC_BUILDER_KERNEL_PATH", defaultKernel),
		FirecrackerPath: envOrDefault(env, "FVC_FIRECRACKER_PATH", "/usr/local/bin/firecracker"),
		AgentPort:       envIntOrDefault(env, "FVC_BUILDER_AGENT_PORT", 9090),
		CPUs:            int32(envIntOrDefault(env, "FVC_BUILDER_CPUS", 1)),
		MemoryMB:        int32(envIntOrDefault(env, "FVC_BUILDER_MEMORY_MB", 512)),
		TargetDevice:    envOrDefault(env, "FVC_BUILDER_TARGET_DEVICE", "/dev/vdb"),
		TargetRoot:      envOrDefault(env, "FVC_BUILDER_TARGET_ROOT", "/mnt/fvc-target"),
		BootArgsExtra:   strings.TrimSpace(env("FVC_BUILDER_BOOT_ARGS_EXTRA")),
		AgentToken:      env("FVC_BUILD_AGENT_TOKEN"),
		RequestTimeout:  RequestTimeoutFromEnv(env),
	}
}

// ConfigureFirecracker attaches the builder rootfs, target image, network, and
// machine configuration, then starts the instance.
func ConfigureFirecracker(socketPath string, cfg Config, targetImage string, netCfg hostnet.NetworkConfig) error {
	if err := fcapi.ConfigureBootSource(socketPath, cfg.KernelPath, BootArgs(netCfg, cfg.BootArgsExtra)); err != nil {
		return fmt.Errorf("builder boot source config failed: %w", err)
	}
	if err := fcapi.ConfigureDrive(socketPath, "rootfs", cfg.BuilderRootfs, true, false); err != nil {
		return fmt.Errorf("builder rootfs config failed: %w", err)
	}
	if err := fcapi.ConfigureDrive(socketPath, "targetfs", targetImage, false, false); err != nil {
		return fmt.Errorf("builder targetfs config failed: %w", err)
	}
	if err := fcapi.ConfigureNetwork(socketPath, "eth0", netCfg.MAC, netCfg.TapName); err != nil {
		return fmt.Errorf("builder network config failed: %w", err)
	}
	if err := fcapi.ConfigureMachine(socketPath, cfg.CPUs, cfg.MemoryMB); err != nil {
		return fmt.Errorf("builder machine config failed: %w", err)
	}
	if err := fcapi.StartInstance(socketPath); err != nil {
		return fmt.Errorf("builder start failed: %w", err)
	}
	return nil
}

// BootArgs returns the Linux kernel command line for builder VMs.
func BootArgs(cfg hostnet.NetworkConfig, extra string) string {
	args := fmt.Sprintf("console=ttyS0 reboot=k panic=1 pci=off random.trust_cpu=on ip=%s::%s:%s::eth0:off root=/dev/vda rw init=/init", cfg.GuestIP, cfg.HostIP, cfg.Netmask)
	if extra = strings.TrimSpace(extra); extra != "" {
		args += " " + extra
	}
	return args
}

// RequestTimeoutFromEnv returns the builder agent request timeout.
func RequestTimeoutFromEnv(env EnvFunc) time.Duration {
	seconds := envIntOrDefault(env, "FVC_BUILD_AGENT_REQUEST_TIMEOUT_SECONDS", 3600)
	if seconds < 1 {
		seconds = 3600
	}
	return time.Duration(seconds) * time.Second
}

func (r *Runner) env(key string) string {
	if r.opts.Env != nil {
		return r.opts.Env(key)
	}
	return os.Getenv(key)
}

func envOrDefault(env EnvFunc, key, fallback string) string {
	if value := strings.TrimSpace(env(key)); value != "" {
		return value
	}
	return fallback
}

func envIntOrDefault(env EnvFunc, key string, fallback int) int {
	if env == nil {
		env = os.Getenv
	}
	value := strings.TrimSpace(env(key))
	if value == "" {
		return fallback
	}
	parsed, err := strconv.Atoi(value)
	if err != nil {
		return fallback
	}
	return parsed
}
