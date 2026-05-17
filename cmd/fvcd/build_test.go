package main

import (
	"os"
	"path/filepath"
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
