package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/lucaspose/fvc/internal/guestruntime"
)

func TestRuntimeBootArgsAddsRootAndInit(t *testing.T) {
	cfg := BuildNetworkConfig("runtime-boot")
	args := runtimeBootArgs(&cfg, "/dev/vdb", true, "token", "vsock")
	for _, want := range []string{
		"root=/dev/vdb",
		"rw",
		"init=/usr/local/bin/fvc-init",
		"fvc_agent_token=token",
		"fvc_agent_mode=vsock",
		"random.trust_cpu=on",
		"ip=" + cfg.GuestIP,
	} {
		if !strings.Contains(args, want) {
			t.Fatalf("expected boot args to contain %q, got %s", want, args)
		}
	}
}

func TestRuntimeBootArgsOmitsInitWhenUnused(t *testing.T) {
	args := runtimeBootArgs(nil, "", false, "", "")
	if !strings.Contains(args, "root=/dev/vda") {
		t.Fatalf("expected default root device, got %s", args)
	}
	if strings.Contains(args, "init=/usr/local/bin/fvc-init") {
		t.Fatalf("did not expect runtime init in args: %s", args)
	}
}

func TestRequiresRuntimeInit(t *testing.T) {
	if requiresRuntimeInit(GuestRuntimeConfig{}) {
		t.Fatal("empty config should not require runtime init")
	}
	if !requiresRuntimeInit(GuestRuntimeConfig{Cmd: []string{"/bin/app"}}) {
		t.Fatal("cmd should require runtime init")
	}
	if !requiresRuntimeInit(GuestRuntimeConfig{Env: []string{"PORT=80"}}) {
		t.Fatal("env should require runtime init")
	}
	if !requiresRuntimeInit(GuestRuntimeConfig{Workdir: "/srv"}) {
		t.Fatal("workdir should require runtime init")
	}
	if !requiresRuntimeInit(GuestRuntimeConfig{Volumes: []guestruntime.Volume{{Device: "/dev/vdb", Target: "/data"}}}) {
		t.Fatal("volumes should require runtime init")
	}
}

func TestGenerateRuntimeRandomSeed(t *testing.T) {
	seed, err := guestruntime.GenerateRandomSeed()
	if err != nil {
		t.Fatalf("GenerateRandomSeed failed: %v", err)
	}
	if seed == "" {
		t.Fatal("expected random seed")
	}
}

func TestPrepareRuntimeRootfsInstallsInitWithRunner(t *testing.T) {
	dir := t.TempDir()
	initPath := filepath.Join(dir, "fvc-init")
	if err := os.WriteFile(initPath, []byte("#!/bin/sh\n"), 0755); err != nil {
		t.Fatalf("failed to write init: %v", err)
	}
	drivePath := filepath.Join(dir, "vm.ext4")
	if err := os.WriteFile(drivePath, []byte("disk"), 0644); err != nil {
		t.Fatalf("failed to write drive: %v", err)
	}
	runner := &fakeRunner{}
	server := Server{
		Config: DaemonConfig{BaseDir: dir, RuntimeInitPath: initPath},
		Runner: runner,
	}
	err := server.prepareRuntimeRootfs(drivePath, GuestRuntimeConfig{
		Cmd:     []string{"/bin/app"},
		Env:     []string{"PORT=80"},
		Workdir: "/srv",
	})
	if err != nil {
		t.Fatalf("prepareRuntimeRootfs failed: %v", err)
	}
	if !containsCallPart(runner.calls, "mount -o loop "+drivePath) {
		t.Fatalf("expected mount call in %#v", runner.calls)
	}
	if !containsCallPart(runner.calls, "umount ") {
		t.Fatalf("expected umount call in %#v", runner.calls)
	}
}

func TestPrepareRuntimeRootfsRequiresInitBinary(t *testing.T) {
	server := Server{Config: DaemonConfig{RuntimeInitPath: filepath.Join(t.TempDir(), "missing")}}
	err := server.prepareRuntimeRootfs("/tmp/missing.ext4", GuestRuntimeConfig{Cmd: []string{"/bin/app"}})
	if err == nil || !strings.Contains(err.Error(), "runtime init unavailable") {
		t.Fatalf("expected missing init error, got %v", err)
	}
}

func containsCallPart(calls []string, expected string) bool {
	for _, call := range calls {
		if strings.Contains(call, expected) {
			return true
		}
	}
	return false
}
