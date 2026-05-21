package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRuntimeDiagnosticsErrorIncludesFailures(t *testing.T) {
	err := RuntimeDiagnosticsError([]RuntimeDiagnostic{
		{Name: "firecracker", OK: true, Message: "/usr/local/bin/firecracker"},
		{Name: "/dev/kvm", OK: false, Message: "missing"},
	})
	if err == nil || !strings.Contains(err.Error(), "/dev/kvm: missing") {
		t.Fatalf("expected diagnostic error to include failing check, got %v", err)
	}
}

func TestCheckExecutable(t *testing.T) {
	path := filepath.Join(t.TempDir(), "firecracker")
	if err := os.WriteFile(path, []byte("#!/bin/sh\nexit 0\n"), 0755); err != nil {
		t.Fatalf("failed to write executable: %v", err)
	}

	check := checkExecutable("firecracker", path)
	if !check.OK {
		t.Fatalf("expected executable check to pass: %#v", check)
	}
}
