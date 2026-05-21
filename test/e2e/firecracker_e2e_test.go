package e2e

import (
	"bytes"
	"context"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

type e2eEnv struct {
	repoRoot      string
	fvc           string
	fvcd          string
	firecracker   string
	kernel        string
	baseImage     string
	builderRootfs string
	runtimeInit   string
	home          string
	runtimeDir    string
	grpcAddr      string
}

func TestFirecrackerBuildRunExecLifecycle(t *testing.T) {
	env := loadE2EEnv(t)
	ctx, cancel := context.WithTimeout(context.Background(), 12*time.Minute)
	defer cancel()

	daemon := startDaemon(t, ctx, env)
	defer stopProcess(t, daemon)

	runCLI(t, ctx, env, "doctor")
	runCLI(t, ctx, env, "image", "import", env.baseImage, "e2e-base")

	buildContext := filepath.Join(t.TempDir(), "context")
	if err := os.MkdirAll(buildContext, 0755); err != nil {
		t.Fatalf("create build context: %v", err)
	}
	writeFile(t, filepath.Join(buildContext, "index.txt"), "hello from fvc e2e\n", 0644)
	writeFile(t, filepath.Join(buildContext, "Fvcfile"), `[image]
from = "e2e-base"
tag = "e2e-web"

[config]
workdir = "/srv"
cmd = ["/bin/sh", "-lc", "echo FVC_E2E_READY; while true; do sleep 1; done"]
env = ["FVC_E2E=1"]
expose = [8080]

[labels]
test = "firecracker-e2e"

[[copy]]
src = "index.txt"
dest = "/srv/index.txt"

[[run]]
command = "test -f /srv/index.txt && printf built >/srv/built.txt"
workdir = "/srv"
timeout_seconds = 60
`, 0644)

	runCLI(t, ctx, env, "build", buildContext)
	runCLI(t, ctx, env, "run", "--image", "e2e-web", "--name", "e2e-vm", "--cpu", "1", "--ram", "512")
	defer runCLIAllowFailure(t, context.Background(), env, "rm", "e2e-vm")
	defer runCLIAllowFailure(t, context.Background(), env, "stop", "e2e-vm")

	waitForExec(t, ctx, env, "e2e-vm")
	out := runCLI(t, ctx, env, "exec", "e2e-vm", "--", "/bin/sh", "-lc", "cat /srv/index.txt && cat /srv/built.txt")
	if !strings.Contains(out, "hello from fvc e2e") || !strings.Contains(out, "built") {
		t.Fatalf("unexpected exec output:\n%s", out)
	}

	logs := waitForLogs(t, ctx, env, "e2e-vm", "FVC_E2E_READY")
	if !strings.Contains(logs, "FVC_E2E_READY") {
		t.Fatalf("expected readiness marker in logs, got:\n%s", logs)
	}

	runCLI(t, ctx, env, "stop", "e2e-vm")
	runCLI(t, ctx, env, "rm", "e2e-vm")
}

func loadE2EEnv(t *testing.T) e2eEnv {
	t.Helper()
	if os.Getenv("FVC_E2E") != "1" {
		t.Skip("set FVC_E2E=1 to run Firecracker e2e tests")
	}
	requirePath(t, "/dev/kvm")
	requirePath(t, "/dev/net/tun")

	repoRoot := repoRoot(t)
	env := e2eEnv{
		repoRoot:      repoRoot,
		fvc:           envOrPath("FVC_E2E_FVC", filepath.Join(repoRoot, "fvc")),
		fvcd:          envOrPath("FVC_E2E_FVCD", filepath.Join(repoRoot, "fvcd")),
		firecracker:   envOrPath("FVC_E2E_FIRECRACKER", "/usr/bin/firecracker"),
		kernel:        envOrPath("FVC_E2E_KERNEL", filepath.Join(repoRoot, "dist", "vmlinux.bin")),
		baseImage:     strings.TrimSpace(os.Getenv("FVC_E2E_BASE_IMAGE")),
		builderRootfs: envOrPath("FVC_E2E_BUILDER_ROOTFS", filepath.Join(repoRoot, "dist", "builder.ext4")),
		runtimeInit:   envOrPath("FVC_E2E_RUNTIME_INIT", filepath.Join(repoRoot, "fvc-init")),
		home:          filepath.Join(t.TempDir(), "home"),
		runtimeDir:    filepath.Join(t.TempDir(), "run"),
	}
	if env.baseImage == "" {
		t.Fatal("FVC_E2E_BASE_IMAGE is required and must point to a Firecracker-compatible ext4 rootfs")
	}
	for label, path := range map[string]string{
		"fvc":            env.fvc,
		"fvcd":           env.fvcd,
		"firecracker":    env.firecracker,
		"kernel":         env.kernel,
		"base image":     env.baseImage,
		"builder rootfs": env.builderRootfs,
		"runtime init":   env.runtimeInit,
	} {
		if _, err := os.Stat(path); err != nil {
			t.Fatalf("%s unavailable at %s: %v", label, path, err)
		}
	}
	addr, err := freeTCPAddr()
	if err != nil {
		t.Fatalf("allocate grpc port: %v", err)
	}
	env.grpcAddr = addr
	return env
}

func startDaemon(t *testing.T, ctx context.Context, env e2eEnv) *exec.Cmd {
	t.Helper()
	if err := os.MkdirAll(env.runtimeDir, 0770); err != nil {
		t.Fatalf("create runtime dir: %v", err)
	}
	cmd := exec.CommandContext(ctx, env.fvcd)
	cmd.Env = append(os.Environ(),
		"FVC_HOME="+env.home,
		"FVC_RUNTIME_DIR="+env.runtimeDir,
		"FVC_GRPC_NETWORK=tcp",
		"FVC_GRPC_ADDR="+env.grpcAddr,
		"FVC_FIRECRACKER_PATH="+env.firecracker,
		"FVC_KERNEL_PATH="+env.kernel,
		"FVC_RUNTIME_INIT_PATH="+env.runtimeInit,
		"FVC_BUILD_BACKEND=microvm",
		"FVC_BUILDER_ROOTFS_PATH="+env.builderRootfs,
		"FVC_BUILDER_KERNEL_PATH="+env.kernel,
		"FVC_NETWORK_ENABLED=true",
		"FVC_STRICT_RUNTIME_CHECKS=true",
	)
	var output bytes.Buffer
	cmd.Stdout = &output
	cmd.Stderr = &output
	if err := cmd.Start(); err != nil {
		t.Fatalf("start fvcd: %v", err)
	}
	t.Cleanup(func() {
		if t.Failed() && output.Len() > 0 {
			t.Logf("fvcd output:\n%s", output.String())
		}
	})
	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		conn, err := net.DialTimeout("tcp", env.grpcAddr, 250*time.Millisecond)
		if err == nil {
			_ = conn.Close()
			return cmd
		}
		if cmd.ProcessState != nil && cmd.ProcessState.Exited() {
			t.Fatalf("fvcd exited early:\n%s", output.String())
		}
		time.Sleep(100 * time.Millisecond)
	}
	stopProcess(t, cmd)
	t.Fatalf("fvcd did not listen on %s:\n%s", env.grpcAddr, output.String())
	return nil
}

func waitForExec(t *testing.T, ctx context.Context, env e2eEnv, vm string) {
	t.Helper()
	deadline := time.Now().Add(90 * time.Second)
	var last string
	for time.Now().Before(deadline) {
		out, err := runCLIResult(ctx, env, "exec", vm, "--", "/bin/sh", "-lc", "printf ok")
		if err == nil && strings.Contains(out, "ok") {
			return
		}
		last = out
		time.Sleep(time.Second)
	}
	t.Fatalf("guest exec did not become ready; last output:\n%s", last)
}

func waitForLogs(t *testing.T, ctx context.Context, env e2eEnv, vm, marker string) string {
	t.Helper()
	deadline := time.Now().Add(60 * time.Second)
	var out string
	for time.Now().Before(deadline) {
		out = runCLIAllowFailure(t, ctx, env, "logs", "--tail", "50", vm)
		if strings.Contains(out, marker) {
			return out
		}
		time.Sleep(time.Second)
	}
	return out
}

func runCLI(t *testing.T, ctx context.Context, env e2eEnv, args ...string) string {
	t.Helper()
	out, err := runCLIResult(ctx, env, args...)
	if err != nil {
		t.Fatalf("fvc %s failed: %v\n%s", strings.Join(args, " "), err, out)
	}
	return out
}

func runCLIAllowFailure(t *testing.T, ctx context.Context, env e2eEnv, args ...string) string {
	t.Helper()
	out, _ := runCLIResult(ctx, env, args...)
	return out
}

func runCLIResult(ctx context.Context, env e2eEnv, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, env.fvc, args...)
	cmd.Env = append(os.Environ(),
		"FVC_GRPC_NETWORK=tcp",
		"FVC_GRPC_ADDR="+env.grpcAddr,
		"NO_COLOR=1",
	)
	var output bytes.Buffer
	cmd.Stdout = &output
	cmd.Stderr = &output
	err := cmd.Run()
	return output.String(), err
}

func stopProcess(t *testing.T, cmd *exec.Cmd) {
	t.Helper()
	if cmd == nil || cmd.Process == nil {
		return
	}
	_ = cmd.Process.Signal(os.Interrupt)
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		_ = cmd.Process.Kill()
		<-done
	}
}

func repoRoot(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatal("repository root not found")
		}
		dir = parent
	}
}

func freeTCPAddr() (string, error) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return "", err
	}
	defer listener.Close()
	return listener.Addr().String(), nil
}

func writeFile(t *testing.T, path, content string, mode os.FileMode) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), mode); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

func requirePath(t *testing.T, path string) {
	t.Helper()
	if _, err := os.Stat(path); err != nil {
		t.Skipf("%s unavailable: %v", path, err)
	}
}

func envOrPath(key, fallback string) string {
	if value := strings.TrimSpace(os.Getenv(key)); value != "" {
		return value
	}
	return fallback
}

func TestE2EPrerequisitesAreDocumented(t *testing.T) {
	if os.Getenv("FVC_E2E") == "1" {
		t.Skip("covered by TestFirecrackerBuildRunExecLifecycle")
	}
	msg := fmt.Sprintf("disabled by default; run with %s", "FVC_E2E=1")
	if !strings.Contains(msg, "FVC_E2E=1") {
		t.Fatal("bad prerequisite message")
	}
}
