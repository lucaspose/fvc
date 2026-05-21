package main

import (
	"context"
	"database/sql"
	"testing"

	"github.com/lucaspose/fvc/proto"
	_ "modernc.org/sqlite"
)

func TestInspect(t *testing.T) {
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("failed to open db: %v", err)
	}
	defer db.Close()
	if err := ensureSchema(db); err != nil {
		t.Fatalf("ensureSchema failed: %v", err)
	}
	_, err = db.Exec(`INSERT INTO vms (id, pid, status, image, cpus, memory_mb, log_path, drive_path, console_path, tap_name, guest_ip, mac_address) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		"vm-inspect", 123, "running", "ubuntu", 2, 1024, "/tmp/vm.log", "/tmp/vm.ext4", "/tmp/vm.in", "fvc123", "172.16.0.2", "02:FC:00:00:00:01")
	if err != nil {
		t.Fatalf("failed to insert vm: %v", err)
	}

	server := Server{DB: db}
	res, err := server.Inspect(context.Background(), &proto.InspectRequest{VmId: "vm-inspect"})
	if err != nil {
		t.Fatalf("Inspect returned transport error: %v", err)
	}
	if !res.Success {
		t.Fatalf("expected inspect success: %s", res.Message)
	}
	if res.Vm.GetGuestIp() != "172.16.0.2" || res.Vm.GetTapName() != "fvc123" || res.Vm.GetLogPath() != "/tmp/vm.log" {
		t.Fatalf("unexpected inspect vm: %#v", res.Vm)
	}
}

func TestInspectHidesNetworkForStoppedVM(t *testing.T) {
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("failed to open db: %v", err)
	}
	defer db.Close()
	if err := ensureSchema(db); err != nil {
		t.Fatalf("ensureSchema failed: %v", err)
	}
	_, err = db.Exec(`INSERT INTO vms (id, pid, status, image, tap_name, guest_ip, mac_address) VALUES (?, ?, ?, ?, ?, ?, ?)`,
		"vm-stopped", 0, "stopped", "ubuntu", "fvc123", "172.16.0.2", "02:FC:00:00:00:01")
	if err != nil {
		t.Fatalf("failed to insert vm: %v", err)
	}

	server := Server{DB: db}
	res, err := server.Inspect(context.Background(), &proto.InspectRequest{VmId: "vm-stopped"})
	if err != nil {
		t.Fatalf("Inspect returned transport error: %v", err)
	}
	if res.Vm.GetGuestIp() != "" || res.Vm.GetTapName() != "" || res.Vm.GetMacAddress() != "" {
		t.Fatalf("expected stopped network fields to be hidden, got %#v", res.Vm)
	}
}

func TestPsAcceptsNilRequest(t *testing.T) {
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("failed to open db: %v", err)
	}
	defer db.Close()
	if err := ensureSchema(db); err != nil {
		t.Fatalf("ensureSchema failed: %v", err)
	}
	if _, err := db.Exec(`INSERT INTO vms (id, status, image) VALUES (?, ?, ?)`, "vm-running", "running", "ubuntu"); err != nil {
		t.Fatalf("failed to insert vm: %v", err)
	}

	server := Server{DB: db}
	res, err := server.Ps(context.Background(), nil)
	if err != nil {
		t.Fatalf("Ps returned error: %v", err)
	}
	if len(res.Vms) != 1 {
		t.Fatalf("expected one running vm, got %d", len(res.Vms))
	}
}

func TestStatsReturnsStoppedVM(t *testing.T) {
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("failed to open db: %v", err)
	}
	defer db.Close()
	if err := ensureSchema(db); err != nil {
		t.Fatalf("ensureSchema failed: %v", err)
	}
	if _, err := db.Exec(`INSERT INTO vms (id, pid, status, image, memory_mb) VALUES (?, ?, ?, ?, ?)`, "vm-stats", 0, "stopped", "ubuntu", 1024); err != nil {
		t.Fatalf("failed to insert vm: %v", err)
	}

	server := Server{DB: db}
	res, err := server.Stats(context.Background(), &proto.StatsRequest{VmId: "vm-stats"})
	if err != nil {
		t.Fatalf("Stats failed: %v", err)
	}
	if len(res.Stats) != 1 {
		t.Fatalf("expected one stats row, got %d", len(res.Stats))
	}
	if res.Stats[0].VmId != "vm-stats" || res.Stats[0].MemoryMb != 1024 || res.Stats[0].Status != "stopped" {
		t.Fatalf("unexpected stats row: %#v", res.Stats[0])
	}
}

func TestWaitReturnsImmediatelyForStoppedVM(t *testing.T) {
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("failed to open db: %v", err)
	}
	defer db.Close()
	if err := ensureSchema(db); err != nil {
		t.Fatalf("ensureSchema failed: %v", err)
	}
	if _, err := db.Exec(`INSERT INTO vms (id, status, image) VALUES (?, ?, ?)`, "vm-wait", "stopped", "ubuntu"); err != nil {
		t.Fatalf("failed to insert vm: %v", err)
	}

	server := Server{DB: db}
	res, err := server.Wait(context.Background(), &proto.WaitRequest{VmId: "vm-wait", TimeoutSeconds: 1})
	if err != nil {
		t.Fatalf("Wait failed: %v", err)
	}
	if !res.Success || res.Status != "stopped" {
		t.Fatalf("unexpected wait response: %#v", res)
	}
}
