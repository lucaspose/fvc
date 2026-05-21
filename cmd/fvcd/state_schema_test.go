package main

import (
	"database/sql"
	"path/filepath"
	"testing"

	"github.com/lucaspose/fvc/internal"
	_ "modernc.org/sqlite"
)

func TestReconcileStateWithSingleConnectionMarksStaleVMStopped(t *testing.T) {
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("failed to open db: %v", err)
	}
	defer db.Close()
	if err := configureStateDB(db); err != nil {
		t.Fatalf("configureStateDB failed: %v", err)
	}
	if err := ensureSchema(db); err != nil {
		t.Fatalf("ensureSchema failed: %v", err)
	}
	_, err = db.Exec(`INSERT INTO vms (id, pid, status, image, process_start_time, tap_name, guest_ip, mac_address, ports) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		"stale-vm", 999999999, internal.VmRunning, "ubuntu", "", "tap0", "172.16.0.2", "02:FC:00:00:00:01", "80:80")
	if err != nil {
		t.Fatalf("insert vm failed: %v", err)
	}
	if err := reconcileState(db, nil); err != nil {
		t.Fatalf("reconcileState failed: %v", err)
	}
	var status string
	var pid int
	if err := db.QueryRow(`SELECT status, pid FROM vms WHERE id = ?`, "stale-vm").Scan(&status, &pid); err != nil {
		t.Fatalf("query vm failed: %v", err)
	}
	if status != internal.VmStopped || pid != 0 {
		t.Fatalf("expected stale vm stopped with pid 0, got status=%s pid=%d", status, pid)
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

func TestEnsureSchemaAddsVsockPath(t *testing.T) {
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("failed to open db: %v", err)
	}
	defer db.Close()
	if err := ensureSchema(db); err != nil {
		t.Fatalf("ensureSchema failed: %v", err)
	}

	if _, err := db.Exec(`INSERT INTO vms (id, status, image, vsock_path) VALUES (?, ?, ?, ?)`, "vm-vsock", "running", "ubuntu", "/run/fvc/vm.vsock"); err != nil {
		t.Fatalf("expected vsock_path column to exist: %v", err)
	}
}

func TestEnsureSchemaAddsExitCode(t *testing.T) {
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("failed to open db: %v", err)
	}
	defer db.Close()
	if err := ensureSchema(db); err != nil {
		t.Fatalf("ensureSchema failed: %v", err)
	}

	if _, err := db.Exec(`INSERT INTO vms (id, status, image, exit_code) VALUES (?, ?, ?, ?)`, "vm-exit", "exited", "ubuntu", 7); err != nil {
		t.Fatalf("expected exit_code column to exist: %v", err)
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

func TestEnsureSchemaEnforcesUniqueVMNames(t *testing.T) {
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("failed to open db: %v", err)
	}
	defer db.Close()
	if err := ensureSchema(db); err != nil {
		t.Fatalf("ensureSchema failed: %v", err)
	}

	if _, err := db.Exec(`INSERT INTO vms (id, name, status, image) VALUES (?, ?, ?, ?)`, "vm-a", "api", "stopped", "ubuntu"); err != nil {
		t.Fatalf("failed to insert first vm: %v", err)
	}
	if _, err := db.Exec(`INSERT INTO vms (id, name, status, image) VALUES (?, ?, ?, ?)`, "vm-b", "api", "stopped", "ubuntu"); err == nil {
		t.Fatal("expected duplicate non-empty vm name to be rejected")
	}
	if _, err := db.Exec(`INSERT INTO vms (id, name, status, image) VALUES (?, ?, ?, ?)`, "vm-c", "", "stopped", "ubuntu"); err != nil {
		t.Fatalf("expected empty vm name to be reusable: %v", err)
	}
}

func TestRuntimePathUsesRuntimeDir(t *testing.T) {
	server := Server{Config: DaemonConfig{RuntimeDir: "/run/fvc"}}
	got := server.runtimePath("fvc-test.socket")
	if got != filepath.Join("/run/fvc", "fvc-test.socket") {
		t.Fatalf("unexpected runtime path: %s", got)
	}
}
