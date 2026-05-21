package buildplan

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadBuildPlanRuntimeAndCopy(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "index.html"), []byte("hello"), 0644); err != nil {
		t.Fatal(err)
	}
	fvcfile := `[image]
from = "ubuntu"
tag = "ubuntu-web"

[config]
workdir = "/srv"
cmd = ["/bin/server"]
env = ["PORT=80"]
expose = [80]

[[copy]]
src = "index.html"
dest = "/srv/index.html"
`
	if err := os.WriteFile(filepath.Join(dir, "Fvcfile"), []byte(fvcfile), 0644); err != nil {
		t.Fatal(err)
	}

	plan, err := Load(dir, "")
	if err != nil {
		t.Fatalf("Load failed: %v", err)
	}
	if plan.BaseImage != "ubuntu" || plan.Tag != "ubuntu-web" {
		t.Fatalf("unexpected plan identity: %#v", plan)
	}
	if plan.Runtime.Workdir != "/srv" || plan.Runtime.Cmd[0] != "/bin/server" || plan.Runtime.Env[0] != "PORT=80" {
		t.Fatalf("unexpected runtime: %#v", plan.Runtime)
	}
	if len(plan.Copies) != 1 || plan.Copies[0].DestPath != "/srv/index.html" {
		t.Fatalf("unexpected copies: %#v", plan.Copies)
	}
}

func TestLoadRejectsIgnoredCopySource(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, ".fvcignore"), []byte("secret.txt\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "secret.txt"), []byte("secret"), 0644); err != nil {
		t.Fatal(err)
	}
	fvcfile := `[image]
from = "ubuntu"
tag = "bad"

[[copy]]
src = "secret.txt"
dest = "/tmp/secret.txt"
`
	if err := os.WriteFile(filepath.Join(dir, "Fvcfile"), []byte(fvcfile), 0644); err != nil {
		t.Fatal(err)
	}

	if _, err := Load(dir, ""); err == nil {
		t.Fatal("expected ignored copy source to be rejected")
	}
}
