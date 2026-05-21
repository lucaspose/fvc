package main

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/lucaspose/fvc/proto"
	_ "modernc.org/sqlite"
)

func TestReadTailLines(t *testing.T) {
	path := filepath.Join(t.TempDir(), "vm.log")
	if err := os.WriteFile(path, []byte("one\ntwo\nthree\n"), 0644); err != nil {
		t.Fatalf("failed to write test log: %v", err)
	}

	lines, offset, err := readTailLines(path, 2)
	if err != nil {
		t.Fatalf("readTailLines failed: %v", err)
	}
	if offset != int64(len("one\ntwo\nthree\n")) {
		t.Fatalf("unexpected offset %d", offset)
	}
	if got := strings.Join(lines, ","); got != "two,three" {
		t.Fatalf("unexpected tail lines: %s", got)
	}
}

func TestExitCodeFromLog(t *testing.T) {
	path := filepath.Join(t.TempDir(), "vm.log")
	if err := os.WriteFile(path, []byte("boot\napp done\nFVC_EXIT_CODE=42\n"), 0644); err != nil {
		t.Fatalf("failed to write test log: %v", err)
	}
	if got := exitCodeFromLog(path); got != 42 {
		t.Fatalf("expected exit code 42, got %d", got)
	}
}

func TestExitCodeFromLogMissingMarker(t *testing.T) {
	path := filepath.Join(t.TempDir(), "vm.log")
	if err := os.WriteFile(path, []byte("boot\n"), 0644); err != nil {
		t.Fatalf("failed to write test log: %v", err)
	}
	if got := exitCodeFromLog(path); got != -1 {
		t.Fatalf("expected missing exit code, got %d", got)
	}
}

func TestLogPathForVM(t *testing.T) {
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("failed to open db: %v", err)
	}
	defer db.Close()
	if err := ensureSchema(db); err != nil {
		t.Fatalf("ensureSchema failed: %v", err)
	}
	if _, err := db.Exec(`INSERT INTO vms (id, status, image, log_path) VALUES (?, ?, ?, ?)`, "vm-1", "running", "debian", "/tmp/vm-1.log"); err != nil {
		t.Fatalf("failed to insert vm: %v", err)
	}

	server := Server{DB: db}
	logPath, err := server.logPathForVM("vm-1")
	if err != nil {
		t.Fatalf("logPathForVM failed: %v", err)
	}
	if logPath != "/tmp/vm-1.log" {
		t.Fatalf("unexpected log path: %s", logPath)
	}
	if _, err := server.logPathForVM("missing"); err == nil {
		t.Fatal("expected missing vm to return an error")
	}
}

func TestDiagnosticsReportsConfiguredRuntime(t *testing.T) {
	dir := t.TempDir()
	firecracker := filepath.Join(dir, "firecracker")
	if err := os.WriteFile(firecracker, []byte("#!/bin/sh\n"), 0755); err != nil {
		t.Fatalf("failed to write fake firecracker: %v", err)
	}
	server := Server{Config: DaemonConfig{
		FirecrackerPath: firecracker,
		GRPCNetwork:     "unix",
		GRPCAddr:        "/run/fvc/fvcd.sock",
		BaseDir:         filepath.Join(dir, "data"),
		RuntimeDir:      filepath.Join(dir, "run"),
		NetworkEnabled:  false,
	}}

	res, err := server.Diagnostics(context.Background(), &proto.DiagnosticsRequest{})
	if err != nil {
		t.Fatalf("Diagnostics returned transport error: %v", err)
	}
	if res.GetGrpcNetwork() != "unix" || res.GetGrpcAddress() != "/run/fvc/fvcd.sock" || res.GetNetworkEnabled() {
		t.Fatalf("unexpected diagnostics config: %#v", res)
	}
	check := findDiagnosticCheck(res.Checks, "firecracker")
	if check == nil {
		t.Fatalf("expected firecracker check, got %#v", res.Checks)
	}
	if !check.GetOk() || check.GetMessage() != firecracker {
		t.Fatalf("unexpected firecracker check: %#v", check)
	}
}

func TestDiagnosticsReportsRuntimeCheckError(t *testing.T) {
	server := Server{Config: DaemonConfig{
		FirecrackerPath: filepath.Join(t.TempDir(), "missing-firecracker"),
		NetworkEnabled:  false,
	}}

	res, err := server.Diagnostics(context.Background(), &proto.DiagnosticsRequest{})
	if err != nil {
		t.Fatalf("Diagnostics returned transport error: %v", err)
	}
	check := findDiagnosticCheck(res.Checks, "firecracker")
	if check == nil {
		t.Fatalf("expected firecracker check, got %#v", res.Checks)
	}
	if check.GetOk() || !strings.Contains(check.GetMessage(), "no such file") {
		t.Fatalf("expected missing firecracker diagnostic, got %#v", check)
	}
}

func findDiagnosticCheck(checks []*proto.DiagnosticCheck, name string) *proto.DiagnosticCheck {
	for _, check := range checks {
		if check.GetName() == name {
			return check
		}
	}
	return nil
}
