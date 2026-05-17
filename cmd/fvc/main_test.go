package main

import (
	"path/filepath"
	"syscall"
	"testing"
)

func TestValidateVmfileAcceptsMinimalConfig(t *testing.T) {
	config := VmfileConfig{
		VM: VMSection{
			Name: "api",
			CPU:  2,
			RAM:  512,
		},
		Image: ImageSection{
			Source: "debian:bookworm",
		},
	}

	if err := validateVmfile(config); err != nil {
		t.Fatalf("expected config to be valid, got %v", err)
	}
}

func TestValidateVmfileRejectsMissingImage(t *testing.T) {
	config := VmfileConfig{
		VM: VMSection{
			CPU: 1,
			RAM: 512,
		},
	}

	if err := validateVmfile(config); err == nil {
		t.Fatal("expected missing image to be rejected")
	}
}

func TestValidateVmfileRejectsInvalidResources(t *testing.T) {
	config := VmfileConfig{
		VM: VMSection{
			CPU: 0,
			RAM: 64,
		},
		Image: ImageSection{
			Source: "debian",
		},
	}

	if err := validateVmfile(config); err == nil {
		t.Fatal("expected invalid resources to be rejected")
	}
}

func TestValidateVmfileRejectsUnsafeImage(t *testing.T) {
	config := VmfileConfig{
		VM: VMSection{
			CPU: 1,
			RAM: 512,
		},
		Image: ImageSection{
			Source: "../debian",
		},
	}

	if err := validateVmfile(config); err == nil {
		t.Fatal("expected unsafe image to be rejected")
	}
}

func TestSplitConsoleLines(t *testing.T) {
	lines := splitConsoleLines([]byte("one\ntwo\nthree\n"))
	if len(lines) != 3 {
		t.Fatalf("expected 3 lines, got %d", len(lines))
	}
	if string(lines[2]) != "three" {
		t.Fatalf("unexpected last line: %q", lines[2])
	}
}

func TestOpenConsoleWriterMissingReaderDoesNotBlock(t *testing.T) {
	path := filepath.Join(t.TempDir(), "console.in")
	if err := syscall.Mkfifo(path, 0600); err != nil {
		t.Fatalf("mkfifo failed: %v", err)
	}
	if _, err := openConsoleWriter(path); err == nil {
		t.Fatal("expected openConsoleWriter to fail without a reader")
	}
}
