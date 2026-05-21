package guestruntime

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestRequiresInit(t *testing.T) {
	if RequiresInit(Config{}) {
		t.Fatal("empty config should not require runtime init")
	}
	if !RequiresInit(Config{Cmd: []string{"/bin/app"}}) {
		t.Fatal("cmd should require runtime init")
	}
	if !RequiresInit(Config{Env: []string{"PORT=80"}}) {
		t.Fatal("env should require runtime init")
	}
	if !RequiresInit(Config{Workdir: "/srv"}) {
		t.Fatal("workdir should require runtime init")
	}
}

func TestGenerateRandomSeed(t *testing.T) {
	seed, err := GenerateRandomSeed()
	if err != nil {
		t.Fatalf("GenerateRandomSeed failed: %v", err)
	}
	if seed == "" {
		t.Fatal("expected random seed")
	}
}

func TestInstallWritesInitAndRuntimeConfig(t *testing.T) {
	dir := t.TempDir()
	initPath := filepath.Join(dir, "fvc-init")
	if err := os.WriteFile(initPath, []byte("#!/bin/sh\n"), 0755); err != nil {
		t.Fatal(err)
	}
	mountDir := filepath.Join(dir, "mnt")
	if err := Install(mountDir, initPath, Config{Cmd: []string{"/bin/app"}, Env: []string{"PORT=80"}, Workdir: "/srv"}); err != nil {
		t.Fatalf("Install failed: %v", err)
	}
	if info, err := os.Stat(filepath.Join(mountDir, "usr/local/bin/fvc-init")); err != nil {
		t.Fatal(err)
	} else if info.Mode().Perm() != 0755 {
		t.Fatalf("unexpected init mode %o", info.Mode().Perm())
	}
	data, err := os.ReadFile(filepath.Join(mountDir, "etc/fvc/runtime.json"))
	if err != nil {
		t.Fatal(err)
	}
	var config Config
	if err := json.Unmarshal(data, &config); err != nil {
		t.Fatal(err)
	}
	if config.Cmd[0] != "/bin/app" || config.Env[0] != "PORT=80" || config.Workdir != "/srv" || config.RandomSeed == "" {
		t.Fatalf("unexpected config: %#v", config)
	}
}

func TestValidateHostInit(t *testing.T) {
	if err := ValidateHostInit(""); err == nil {
		t.Fatal("expected empty path error")
	}
	if err := ValidateHostInit(filepath.Join(t.TempDir(), "missing")); err == nil {
		t.Fatal("expected missing init error")
	}
}
