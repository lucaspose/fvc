package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLoadBuildPlan(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "index.html"), []byte("hello"), 0644); err != nil {
		t.Fatalf("failed to write context file: %v", err)
	}
	fvcfile := `[image]
from = "ubuntu"
tag = "ubuntu-web"

[[copy]]
src = "index.html"
dest = "/var/www/html/index.html"
`
	if err := os.WriteFile(filepath.Join(dir, "Fvcfile"), []byte(fvcfile), 0644); err != nil {
		t.Fatalf("failed to write Fvcfile: %v", err)
	}

	plan, err := LoadBuildPlan(dir, "")
	if err != nil {
		t.Fatalf("LoadBuildPlan failed: %v", err)
	}
	if plan.BaseImage != "ubuntu" || plan.Tag != "ubuntu-web" {
		t.Fatalf("unexpected image plan: %#v", plan)
	}
	if len(plan.Copies) != 1 {
		t.Fatalf("expected one copy, got %d", len(plan.Copies))
	}
	if plan.Copies[0].DestPath != "/var/www/html/index.html" {
		t.Fatalf("unexpected copy dest: %#v", plan.Copies[0])
	}
}

func TestLoadBuildPlanRuntimeMetadata(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "index.html"), []byte("hello"), 0644); err != nil {
		t.Fatalf("failed to write context file: %v", err)
	}
	fvcfile := `[image]
from = "ubuntu"
tag = "ubuntu-web"

[config]
workdir = "/srv"
cmd = ["/bin/server", "--port", "80"]
env = ["PORT=80"]
expose = [80]

[labels]
app = "web"

[[copy]]
src = "index.html"
dest = "/srv/index.html"
`
	if err := os.WriteFile(filepath.Join(dir, "Fvcfile"), []byte(fvcfile), 0644); err != nil {
		t.Fatalf("failed to write Fvcfile: %v", err)
	}

	plan, err := LoadBuildPlan(dir, "")
	if err != nil {
		t.Fatalf("LoadBuildPlan failed: %v", err)
	}
	if plan.Runtime.Workdir != "/srv" || plan.Runtime.Cmd[0] != "/bin/server" || plan.Runtime.Env[0] != "PORT=80" || plan.Runtime.ExposedPorts[0] != 80 {
		t.Fatalf("unexpected runtime metadata: %#v", plan.Runtime)
	}
	if plan.Labels["app"] != "web" {
		t.Fatalf("unexpected labels: %#v", plan.Labels)
	}
}

func TestLoadBuildPlanRejectsRun(t *testing.T) {
	dir := t.TempDir()
	fvcfile := `[image]
from = "ubuntu"
tag = "bad"

[[run]]
command = "apt-get update"
`
	if err := os.WriteFile(filepath.Join(dir, "Fvcfile"), []byte(fvcfile), 0644); err != nil {
		t.Fatalf("failed to write Fvcfile: %v", err)
	}

	plan, err := LoadBuildPlan(dir, "")
	if err != nil {
		t.Fatalf("LoadBuildPlan failed: %v", err)
	}
	if len(plan.Runs) != 1 || plan.Runs[0].Command != "apt-get update" {
		t.Fatalf("unexpected run plan: %#v", plan.Runs)
	}
}

func TestLoadBuildPlanTagOverride(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "file.txt"), []byte("hello"), 0644); err != nil {
		t.Fatalf("failed to write context file: %v", err)
	}
	fvcfile := `[image]
from = "ubuntu"
tag = "ignored"

[[copy]]
src = "file.txt"
dest = "/tmp/file.txt"
`
	if err := os.WriteFile(filepath.Join(dir, "Fvcfile"), []byte(fvcfile), 0644); err != nil {
		t.Fatalf("failed to write Fvcfile: %v", err)
	}

	plan, err := LoadBuildPlan(dir, "override")
	if err != nil {
		t.Fatalf("LoadBuildPlan failed: %v", err)
	}
	if plan.Tag != "override" {
		t.Fatalf("expected override tag, got %s", plan.Tag)
	}
}

func TestLoadBuildPlanRejectsUnsafeCopySource(t *testing.T) {
	dir := t.TempDir()
	fvcfile := `[image]
from = "ubuntu"
tag = "bad"

[[copy]]
src = "../secret"
dest = "/tmp/secret"
`
	if err := os.WriteFile(filepath.Join(dir, "Fvcfile"), []byte(fvcfile), 0644); err != nil {
		t.Fatalf("failed to write Fvcfile: %v", err)
	}

	if _, err := LoadBuildPlan(dir, ""); err == nil {
		t.Fatal("expected unsafe copy source to be rejected")
	}
}

func TestLoadBuildPlanRejectsSymlinkOutsideContext(t *testing.T) {
	dir := t.TempDir()
	outside := filepath.Join(t.TempDir(), "secret")
	if err := os.WriteFile(outside, []byte("secret"), 0644); err != nil {
		t.Fatalf("failed to write outside file: %v", err)
	}
	if err := os.Symlink(outside, filepath.Join(dir, "secret-link")); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}
	fvcfile := `[image]
from = "ubuntu"
tag = "bad"

[[copy]]
src = "secret-link"
dest = "/tmp/secret"
`
	if err := os.WriteFile(filepath.Join(dir, "Fvcfile"), []byte(fvcfile), 0644); err != nil {
		t.Fatalf("failed to write Fvcfile: %v", err)
	}

	if _, err := LoadBuildPlan(dir, ""); err == nil {
		t.Fatal("expected symlink escaping context to be rejected")
	}
}

func TestLoadBuildPlanRejectsRelativeCopyDest(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "file.txt"), []byte("hello"), 0644); err != nil {
		t.Fatalf("failed to write context file: %v", err)
	}
	fvcfile := `[image]
from = "ubuntu"
tag = "bad"

[[copy]]
src = "file.txt"
dest = "tmp/file.txt"
`
	if err := os.WriteFile(filepath.Join(dir, "Fvcfile"), []byte(fvcfile), 0644); err != nil {
		t.Fatalf("failed to write Fvcfile: %v", err)
	}

	if _, err := LoadBuildPlan(dir, ""); err == nil {
		t.Fatal("expected relative copy destination to be rejected")
	}
}

func TestApplyBuildRunsRequiresExplicitAgentBackend(t *testing.T) {
	t.Setenv("FVC_BUILD_BACKEND", "")
	store := &ImageStore{kernelPath: filepath.Join(t.TempDir(), "missing-kernel")}
	err := store.applyBuildRuns(filepath.Join(t.TempDir(), "missing.ext4"), t.TempDir(), BuildPlan{Runs: []BuildRun{{Command: "true"}}}, &fakeRunner{})
	if err == nil || !strings.Contains(err.Error(), "FVC_BUILDER_ROOTFS_PATH") {
		t.Fatalf("expected builder rootfs error, got %v", err)
	}
}

func TestApplyBuildRunsWithLocalAgentRequiresOptIn(t *testing.T) {
	store := &ImageStore{}
	err := store.applyBuildRunsWithLocalAgent(filepath.Join(t.TempDir(), "missing.ext4"), t.TempDir(), BuildPlan{Runs: []BuildRun{{Command: "true"}}}, &fakeRunner{})
	if err == nil || !strings.Contains(err.Error(), "FVC_ALLOW_INSECURE_HOST_AGENT") {
		t.Fatalf("expected insecure host agent opt-in error, got %v", err)
	}
}

func TestBuildAgentRequestTimeout(t *testing.T) {
	t.Setenv("FVC_BUILD_AGENT_REQUEST_TIMEOUT_SECONDS", "2")
	if got := buildAgentRequestTimeout(); got.String() != "2s" {
		t.Fatalf("unexpected timeout: %s", got)
	}
	t.Setenv("FVC_BUILD_AGENT_REQUEST_TIMEOUT_SECONDS", "0")
	if got := buildAgentRequestTimeout(); got.String() != "1h0m0s" {
		t.Fatalf("expected fallback timeout, got %s", got)
	}
}
