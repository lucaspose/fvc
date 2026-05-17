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

func TestValidateRunRequestDefaults(t *testing.T) {
	image, cpus, memory, err := validateRunRequest(&proto.RunRequest{})
	if err != nil {
		t.Fatalf("expected default request to be valid, got %v", err)
	}
	if image != "ubuntu" || cpus != 1 || memory != 512 {
		t.Fatalf("unexpected defaults: image=%s cpus=%d memory=%d", image, cpus, memory)
	}
}

func TestValidateRunRequestRejectsUnsafeImage(t *testing.T) {
	_, _, _, err := validateRunRequest(&proto.RunRequest{Source: "../debian"})
	if err == nil {
		t.Fatal("expected unsafe image ref to be rejected")
	}
}

func TestValidateRunRequestRejectsInvalidResources(t *testing.T) {
	_, _, _, err := validateRunRequest(&proto.RunRequest{
		Source: "debian",
		Config: &proto.VmConfig{Cpus: 0, MemoryMb: 64},
	})
	if err == nil || !strings.Contains(err.Error(), "cpu") {
		t.Fatalf("expected resource validation error, got %v", err)
	}
}

func TestRunMicroVMEmitsValidationError(t *testing.T) {
	server := Server{Config: DaemonConfig{NetworkEnabled: false}}
	var events []*proto.RunEvent

	res, err := server.runMicroVM(context.Background(), &proto.RunRequest{Source: "../ubuntu"}, func(event *proto.RunEvent) error {
		events = append(events, event)
		return nil
	})
	if err != nil {
		t.Fatalf("runMicroVM returned transport error: %v", err)
	}
	if res.GetStatus() != "failed" {
		t.Fatalf("expected failed response, got %s", res.GetStatus())
	}
	if len(events) != 1 {
		t.Fatalf("expected one event, got %d", len(events))
	}
	if events[0].GetStatus() != "error" || events[0].GetStage() != "validate" {
		t.Fatalf("unexpected event: %#v", events[0])
	}
}

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

func TestEnsureSchemaAddsDrivePath(t *testing.T) {
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("failed to open db: %v", err)
	}
	defer db.Close()
	if err := ensureSchema(db); err != nil {
		t.Fatalf("ensureSchema failed: %v", err)
	}

	if _, err := db.Exec(`INSERT INTO vms (id, status, image, drive_path) VALUES (?, ?, ?, ?)`, "vm-drive", "stopped", "ubuntu", "/tmp/vm-drive.ext4"); err != nil {
		t.Fatalf("expected drive_path column to exist: %v", err)
	}
}

func TestEnsureSchemaAddsConsolePath(t *testing.T) {
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("failed to open db: %v", err)
	}
	defer db.Close()
	if err := ensureSchema(db); err != nil {
		t.Fatalf("ensureSchema failed: %v", err)
	}

	if _, err := db.Exec(`INSERT INTO vms (id, status, image, console_path) VALUES (?, ?, ?, ?)`, "vm-console", "running", "ubuntu", "/tmp/vm-console.in"); err != nil {
		t.Fatalf("expected console_path column to exist: %v", err)
	}
}

func TestConsoleInfo(t *testing.T) {
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("failed to open db: %v", err)
	}
	defer db.Close()
	if err := ensureSchema(db); err != nil {
		t.Fatalf("ensureSchema failed: %v", err)
	}
	dir := t.TempDir()
	logPath := filepath.Join(dir, "vm.log")
	inputPath := filepath.Join(dir, "vm.in")
	if err := os.WriteFile(logPath, []byte("boot\n"), 0600); err != nil {
		t.Fatalf("failed to create log file: %v", err)
	}
	if err := os.WriteFile(inputPath, []byte{}, 0600); err != nil {
		t.Fatalf("failed to create input file: %v", err)
	}
	if _, err := db.Exec(`INSERT INTO vms (id, status, image, log_path, console_path) VALUES (?, ?, ?, ?, ?)`, "vm-console", "running", "ubuntu", logPath, inputPath); err != nil {
		t.Fatalf("failed to insert vm: %v", err)
	}

	server := Server{DB: db}
	res, err := server.ConsoleInfo(context.Background(), &proto.ConsoleInfoRequest{VmId: "vm-console"})
	if err != nil {
		t.Fatalf("ConsoleInfo returned transport error: %v", err)
	}
	if !res.Success || res.LogPath != logPath || res.InputPath != inputPath {
		t.Fatalf("unexpected console response: %#v", res)
	}
}

func TestEnsureSchemaAddsNetworkColumns(t *testing.T) {
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("failed to open db: %v", err)
	}
	defer db.Close()
	if err := ensureSchema(db); err != nil {
		t.Fatalf("ensureSchema failed: %v", err)
	}

	_, err = db.Exec(`INSERT INTO vms (id, status, image, tap_name, guest_ip, mac_address) VALUES (?, ?, ?, ?, ?, ?)`, "vm-net", "stopped", "ubuntu", "fvc123", "172.16.0.2", "02:FC:00:00:00:01")
	if err != nil {
		t.Fatalf("expected network columns to exist: %v", err)
	}
}
