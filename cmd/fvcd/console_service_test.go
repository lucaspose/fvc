package main

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"testing"

	"github.com/lucaspose/fvc/proto"
	_ "modernc.org/sqlite"
)

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
