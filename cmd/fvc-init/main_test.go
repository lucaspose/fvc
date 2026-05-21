package main

import (
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
)

func TestLoadRuntimeConfig(t *testing.T) {
	path := filepath.Join(t.TempDir(), "runtime.json")
	data := `{"env":["PORT=80"],"cmd":["/bin/server","--port","80"],"workdir":"/srv","random_seed":"c2VlZA=="}`
	if err := os.WriteFile(path, []byte(data), 0644); err != nil {
		t.Fatalf("failed to write runtime config: %v", err)
	}
	config, err := loadRuntimeConfig(path)
	if err != nil {
		t.Fatalf("loadRuntimeConfig failed: %v", err)
	}
	if config.Workdir != "/srv" || config.Cmd[0] != "/bin/server" || config.Env[0] != "PORT=80" || config.RandomSeed != "c2VlZA==" {
		t.Fatalf("unexpected config: %#v", config)
	}
}

func TestSeedKernelRandomRejectsInvalidSeed(t *testing.T) {
	err := seedKernelRandom("not-base64!")
	if err == nil || !strings.Contains(err.Error(), "decode") {
		t.Fatalf("expected decode error, got %v", err)
	}
}

func TestLoadRuntimeConfigRejectsInvalidEnv(t *testing.T) {
	path := filepath.Join(t.TempDir(), "runtime.json")
	if err := os.WriteFile(path, []byte(`{"env":["=bad"],"cmd":["/bin/server"]}`), 0644); err != nil {
		t.Fatalf("failed to write runtime config: %v", err)
	}
	_, err := loadRuntimeConfig(path)
	if err == nil || !strings.Contains(err.Error(), "invalid runtime env") {
		t.Fatalf("expected invalid env error, got %v", err)
	}
}

func TestLoadRuntimeConfigRejectsEmptyCommandArg(t *testing.T) {
	path := filepath.Join(t.TempDir(), "runtime.json")
	if err := os.WriteFile(path, []byte(`{"cmd":["/bin/server"," "]}`), 0644); err != nil {
		t.Fatalf("failed to write runtime config: %v", err)
	}
	_, err := loadRuntimeConfig(path)
	if err == nil || !strings.Contains(err.Error(), "empty argument") {
		t.Fatalf("expected empty argument error, got %v", err)
	}
}

func TestValidateGuestExecRequest(t *testing.T) {
	err := validateGuestExecRequest(guestExecRequest{
		Command: []string{"/bin/echo", "hello"},
		Env:     []string{"A=B"},
		Workdir: "/tmp",
	})
	if err != nil {
		t.Fatalf("validateGuestExecRequest failed: %v", err)
	}
}

func TestValidateGuestExecRequestRejectsBadInput(t *testing.T) {
	tests := []struct {
		name string
		req  guestExecRequest
		want string
	}{
		{name: "missing command", req: guestExecRequest{}, want: "required"},
		{name: "empty arg", req: guestExecRequest{Command: []string{" "}}, want: "empty argument"},
		{name: "bad env", req: guestExecRequest{Command: []string{"true"}, Env: []string{"=bad"}}, want: "invalid exec env"},
		{name: "relative workdir", req: guestExecRequest{Command: []string{"true"}, Workdir: "tmp"}, want: "absolute"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validateGuestExecRequest(tt.req)
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("expected %q error, got %v", tt.want, err)
			}
		})
	}
}

func TestAuthorized(t *testing.T) {
	req, err := http.NewRequest(http.MethodPost, "http://example.test/exec", nil)
	if err != nil {
		t.Fatalf("request build failed: %v", err)
	}
	req.Header.Set("Authorization", "Bearer token")
	if !authorized(req, "token") {
		t.Fatal("expected request to be authorized")
	}
	if authorized(req, "other") {
		t.Fatal("unexpected authorization with wrong token")
	}
}

func TestNormalizeAgentMode(t *testing.T) {
	if got := normalizeAgentMode(""); got != "vsock" {
		t.Fatalf("expected default vsock, got %s", got)
	}
	if got := normalizeAgentMode("tcp"); got != "tcp" {
		t.Fatalf("expected tcp, got %s", got)
	}
	if got := normalizeAgentMode("auto"); got != "auto" {
		t.Fatalf("expected auto, got %s", got)
	}
	if got := normalizeAgentMode("bad"); got != "vsock" {
		t.Fatalf("expected invalid mode to fall back to vsock, got %s", got)
	}
}

func TestEnsureDefaultPath(t *testing.T) {
	t.Setenv("PATH", "")
	ensureDefaultPath()
	if os.Getenv("PATH") == "" {
		t.Fatal("expected default PATH to be set")
	}
}

func TestCommandExitCode(t *testing.T) {
	if got := commandExitCode(nil); got != 0 {
		t.Fatalf("expected zero exit code, got %d", got)
	}
	err := exec.Command("sh", "-c", "exit 42").Run()
	if got := commandExitCode(err); got != 42 {
		t.Fatalf("expected exit code 42, got %d", got)
	}
}

func TestCommandExitCodeForSignal(t *testing.T) {
	err := exec.Command("sh", "-c", "kill -TERM $$").Run()
	if got := commandExitCode(err); got != 128+int(syscall.SIGTERM) {
		t.Fatalf("expected signal exit code, got %d", got)
	}
}
