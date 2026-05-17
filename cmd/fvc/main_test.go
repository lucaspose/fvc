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

func TestFormatBytes(t *testing.T) {
	cases := map[int64]string{
		12:              "12B",
		1024:            "1.0KB",
		1024 * 1024:     "1.0MB",
		5 * 1024 * 1024: "5.0MB",
	}
	for size, want := range cases {
		if got := formatBytes(size); got != want {
			t.Fatalf("formatBytes(%d) = %s, want %s", size, got, want)
		}
	}
}

func TestFormatDuration(t *testing.T) {
	cases := map[int64]string{
		12:   "12s",
		65:   "1m05s",
		3660: "1h01m",
	}
	for seconds, want := range cases {
		if got := formatDuration(seconds); got != want {
			t.Fatalf("formatDuration(%d) = %s, want %s", seconds, got, want)
		}
	}
}

func TestFormatMemoryUsage(t *testing.T) {
	got := formatMemoryUsage(128*1024*1024, 1024)
	if got != "128.0MB / 1024MB" {
		t.Fatalf("unexpected memory usage: %s", got)
	}
}
