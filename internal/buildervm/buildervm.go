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
	BuilderRootfs       string
	KernelPath          string
	FirecrackerPath     string
	JailerPath          string
	JailerEnabled       bool
	JailerChrootBaseDir string
	JailerUID           int
	JailerGID           int
	AgentPort           int
	CPUs                int32
	MemoryMB            int32
	TargetDevice        string
	TargetRoot          string
	BootArgsExtra       string
	AgentToken          string
	RequestTimeout      time.Duration
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
	if strings.TrimSpace(cfg.AgentToken) == "" {
		cfg.AgentToken = uuid.NewString()
	}
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
	if cfg.JailerEnabled && strings.TrimSpace(cfg.JailerChrootBaseDir) == "" {
		cfg.JailerChrootBaseDir = filepath.Join(r.opts.BaseDir, "jailer")
	}
	socketPath := filepath.Join(buildDir, "fvc-"+buildID+".socket")
	logPath := filepath.Join(buildDir, "fvc-"+buildID+".log")
	_ = os.Remove(socketPath)
	fcCfg := cfg
	fcTargetImage := targetImage
	cleanupJailer := func() {}

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
	if cfg.JailerEnabled {
		jailerID := "fvc-" + buildID
		if err := validateJailerConfig(cfg); err != nil {
			return err
		}
		if err := ensureJailerChrootBaseDir(cfg.JailerChrootBaseDir); err != nil {
			return err
		}
		cleanupBuilderJailer(cfg.JailerChrootBaseDir, jailerID)
		jailerRoot := filepath.Join(cfg.JailerChrootBaseDir, "firecracker", jailerID, "root")
		socketPath = filepath.Join(jailerRoot, "run", "firecracker.socket")
		cmd = exec.Command(
			cfg.JailerPath,
			"--id", jailerID,
			"--exec-file", cfg.FirecrackerPath,
			"--uid", strconv.Itoa(cfg.JailerUID),
			"--gid", strconv.Itoa(cfg.JailerGID),
			"--chroot-base-dir", cfg.JailerChrootBaseDir,
			"--",
			"--api-sock", "/run/firecracker.socket",
		)
		cleanupJailer = func() {
			cleanupBuilderJailer(cfg.JailerChrootBaseDir, jailerID)
		}
		fcCfg.KernelPath = "/kernel.bin"
		fcCfg.BuilderRootfs = "/builder.ext4"
		fcTargetImage = "/target.ext4"
	}
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
		cleanupJailer()
	}()
	socketTimeout := 3 * time.Second
	if cfg.JailerEnabled {
		socketTimeout = 10 * time.Second
		jailerRoot := filepath.Join(cfg.JailerChrootBaseDir, "firecracker", "fvc-"+buildID, "root")
		if err := waitForJailerRoot(jailerRoot, socketTimeout); err != nil {
			return err
		}
		if err := os.MkdirAll(filepath.Join(jailerRoot, "run"), 0755); err != nil {
			return fmt.Errorf("builder jailer run directory setup failed: %w", err)
		}
		if err := bindMountFile(cfg.KernelPath, filepath.Join(jailerRoot, "kernel.bin"), cfg.JailerUID, cfg.JailerGID, 0440); err != nil {
			return fmt.Errorf("builder jailer kernel bind failed: %w", err)
		}
		if err := bindMountFile(cfg.BuilderRootfs, filepath.Join(jailerRoot, "builder.ext4"), cfg.JailerUID, cfg.JailerGID, 0440); err != nil {
			return fmt.Errorf("builder jailer rootfs bind failed: %w", err)
		}
		if err := bindMountFile(targetImage, filepath.Join(jailerRoot, "target.ext4"), cfg.JailerUID, cfg.JailerGID, 0660); err != nil {
			return fmt.Errorf("builder jailer target bind failed: %w", err)
		}
	}
	if err := fcapi.WaitForSocket(socketPath, socketTimeout); err != nil {
		return fmt.Errorf("builder firecracker API socket not ready at %s: %w", socketPath, err)
	}
	if err := ConfigureFirecracker(socketPath, fcCfg, fcTargetImage, netCfg); err != nil {
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
		BuilderRootfs:       strings.TrimSpace(env("FVC_BUILDER_ROOTFS_PATH")),
		KernelPath:          envOrDefault(env, "FVC_BUILDER_KERNEL_PATH", defaultKernel),
		FirecrackerPath:     envOrDefault(env, "FVC_FIRECRACKER_PATH", "/usr/local/bin/firecracker"),
		JailerPath:          envOrDefault(env, "FVC_JAILER_PATH", "/usr/local/bin/jailer"),
		JailerEnabled:       envBoolOrDefault(env, "FVC_JAILER_ENABLED", false),
		JailerChrootBaseDir: strings.TrimSpace(env("FVC_JAILER_CHROOT_BASE_DIR")),
		JailerUID:           envIntOrDefault(env, "FVC_JAILER_UID", 65534),
		JailerGID:           envIntOrDefault(env, "FVC_JAILER_GID", 65534),
		AgentPort:           envIntOrDefault(env, "FVC_BUILDER_AGENT_PORT", 9090),
		CPUs:                int32(envIntOrDefault(env, "FVC_BUILDER_CPUS", 1)),
		MemoryMB:            int32(envIntOrDefault(env, "FVC_BUILDER_MEMORY_MB", 512)),
		TargetDevice:        envOrDefault(env, "FVC_BUILDER_TARGET_DEVICE", "/dev/vdb"),
		TargetRoot:          envOrDefault(env, "FVC_BUILDER_TARGET_ROOT", "/mnt/fvc-target"),
		BootArgsExtra:       strings.TrimSpace(env("FVC_BUILDER_BOOT_ARGS_EXTRA")),
		AgentToken:          env("FVC_BUILD_AGENT_TOKEN"),
		RequestTimeout:      RequestTimeoutFromEnv(env),
	}
}

// ConfigureFirecracker attaches the builder rootfs, target image, network, and
// machine configuration, then starts the instance.
func ConfigureFirecracker(socketPath string, cfg Config, targetImage string, netCfg hostnet.NetworkConfig) error {
	if err := fcapi.ConfigureBootSource(socketPath, cfg.KernelPath, BootArgs(netCfg, cfg.AgentToken, cfg.BootArgsExtra)); err != nil {
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
func BootArgs(cfg hostnet.NetworkConfig, token, extra string) string {
	args := fmt.Sprintf("console=ttyS0 reboot=k panic=1 pci=off random.trust_cpu=on ip=%s::%s:%s::eth0:off root=/dev/vda rw init=/init", cfg.GuestIP, cfg.HostIP, cfg.Netmask)
	if token = strings.TrimSpace(token); token != "" {
		args += " fvc_build_agent_token=" + token
	}
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

func validateJailerConfig(cfg Config) error {
	if err := validateExecutable("jailer", cfg.JailerPath); err != nil {
		return err
	}
	if err := validateExecutable("firecracker", cfg.FirecrackerPath); err != nil {
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

func ensureJailerChrootBaseDir(path string) error {
	if err := os.MkdirAll(path, 0755); err != nil {
		return fmt.Errorf("jailer chroot base directory setup failed: %w", err)
	}
	return nil
}

func validateExecutable(label, path string) error {
	if strings.TrimSpace(path) == "" || !filepath.IsAbs(path) {
		return fmt.Errorf("%s path must be absolute", label)
	}
	info, err := os.Lstat(path)
	if err != nil {
		return fmt.Errorf("%s unavailable at %s: %w", label, path, err)
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("%s path must not be a symlink: %s", label, path)
	}
	if info.IsDir() || info.Mode()&0111 == 0 {
		return fmt.Errorf("%s path is not executable: %s", label, path)
	}
	return nil
}

func bindMountFile(source, target string, uid, gid int, mode os.FileMode) error {
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
	if err := prepareJailerFileAccess(source, uid, gid, mode); err != nil {
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
	output, err := exec.Command("mount", "--bind", source, target).CombinedOutput()
	if err != nil {
		return fmt.Errorf("mount --bind failed: %w: %s", err, strings.TrimSpace(string(output)))
	}
	return nil
}

func prepareJailerFileAccess(path string, uid, gid int, mode os.FileMode) error {
	info, err := os.Stat(path)
	if err != nil {
		return fmt.Errorf("source stat failed: %w", err)
	}
	if fileAccessibleByJailer(info, uid, gid, mode) {
		return nil
	}
	if err := os.Chmod(path, mode); err != nil {
		return fmt.Errorf("source chmod failed: %w", err)
	}
	if err := os.Chown(path, uid, gid); err != nil {
		return fmt.Errorf("source chown failed: %w", err)
	}
	return nil
}

func fileAccessibleByJailer(info os.FileInfo, uid, gid int, mode os.FileMode) bool {
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return false
	}
	perm := info.Mode().Perm()
	readNeeded := mode&0444 != 0
	writeNeeded := mode&0222 != 0
	canRead := stat.Uid == uint32(uid) && perm&0400 != 0 ||
		stat.Gid == uint32(gid) && perm&0040 != 0 ||
		perm&0004 != 0
	canWrite := stat.Uid == uint32(uid) && perm&0200 != 0 ||
		stat.Gid == uint32(gid) && perm&0020 != 0 ||
		perm&0002 != 0
	return (!readNeeded || canRead) && (!writeNeeded || canWrite)
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

func cleanupBuilderJailer(baseDir, jailerID string) {
	root := filepath.Join(baseDir, "firecracker", jailerID, "root")
	for _, name := range []string{"kernel.bin", "builder.ext4", "target.ext4"} {
		_ = exec.Command("umount", filepath.Join(root, name)).Run()
	}
	dir := filepath.Join(baseDir, "firecracker", jailerID)
	if safeJailerPath(baseDir, dir) {
		_ = os.RemoveAll(dir)
	}
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

func envBoolOrDefault(env EnvFunc, key string, fallback bool) bool {
	if env == nil {
		env = os.Getenv
	}
	value := strings.ToLower(strings.TrimSpace(env(key)))
	if value == "" {
		return fallback
	}
	return value == "1" || value == "true" || value == "yes" || value == "on"
}
