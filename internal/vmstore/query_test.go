package vmstore

import (
	"database/sql"
	"testing"
	"time"

	"github.com/lucaspose/fvc/internal"
)

func TestListDetailsHidesStoppedNetworkFields(t *testing.T) {
	db := openTestDB(t)
	_, err := db.Exec(`INSERT INTO vms (id, name, pid, status, image, cpus, memory_mb, ports, guest_ip, mac_address, tap_name, log_path, drive_path, console_path, exit_code) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		"vm-stopped", "web", 0, internal.VmStopped, "ubuntu", 2, 1024, "80:80,443:443", "172.16.0.2", "02:FC:00:00:00:01", "tap0", "/tmp/vm.log", "/tmp/vm.ext4", "/tmp/vm.in", 7)
	if err != nil {
		t.Fatalf("insert vm failed: %v", err)
	}

	vms, err := ListDetails(db, true)
	if err != nil {
		t.Fatalf("ListDetails failed: %v", err)
	}
	if len(vms) != 1 {
		t.Fatalf("expected one VM, got %d", len(vms))
	}
	vm := vms[0]
	if vm.ID != "vm-stopped" || vm.Name != "web" || vm.CPUs != 2 || vm.MemoryMB != 1024 || vm.ExitCode != 7 {
		t.Fatalf("unexpected VM details: %#v", vm)
	}
	if vm.GuestIP != "" || vm.MACAddress != "" || vm.TapName != "" {
		t.Fatalf("expected stopped network fields hidden, got %#v", vm)
	}
	if len(vm.Ports) != 2 || vm.Ports[0] != "80:80" || vm.Ports[1] != "443:443" {
		t.Fatalf("unexpected ports: %#v", vm.Ports)
	}
}

func TestListDetailsDefaultsToRunningOnly(t *testing.T) {
	db := openTestDB(t)
	if _, err := db.Exec(`INSERT INTO vms (id, status, image) VALUES (?, ?, ?)`, "running-vm", internal.VmRunning, "ubuntu"); err != nil {
		t.Fatalf("insert running vm failed: %v", err)
	}
	if _, err := db.Exec(`INSERT INTO vms (id, status, image) VALUES (?, ?, ?)`, "stopped-vm", internal.VmStopped, "ubuntu"); err != nil {
		t.Fatalf("insert stopped vm failed: %v", err)
	}

	vms, err := ListDetails(db, false)
	if err != nil {
		t.Fatalf("ListDetails failed: %v", err)
	}
	if len(vms) != 1 || vms[0].ID != "running-vm" {
		t.Fatalf("expected running VM only, got %#v", vms)
	}
}

func TestGetDetailsReturnsNoRows(t *testing.T) {
	db := openTestDB(t)
	if _, err := GetDetails(db, "missing"); err != sql.ErrNoRows {
		t.Fatalf("expected sql.ErrNoRows, got %v", err)
	}
}

func TestListStatsRecordsAndWaitStatus(t *testing.T) {
	db := openTestDB(t)
	if _, err := db.Exec(`INSERT INTO vms (id, pid, status, image, memory_mb, exit_code, created_at) VALUES (?, ?, ?, ?, ?, ?, ?)`,
		"vm-stats", 42, internal.VmExited, "ubuntu", 768, 3, "2026-05-21 07:00:00"); err != nil {
		t.Fatalf("insert vm failed: %v", err)
	}

	stats, err := ListStatsRecords(db, "vm-stats")
	if err != nil {
		t.Fatalf("ListStatsRecords failed: %v", err)
	}
	if len(stats) != 1 || stats[0].ID != "vm-stats" || stats[0].PID != 42 || stats[0].MemoryMB != 768 {
		t.Fatalf("unexpected stats: %#v", stats)
	}

	status, err := GetWaitStatus(db, "vm-stats")
	if err != nil {
		t.Fatalf("GetWaitStatus failed: %v", err)
	}
	if status.Status != internal.VmExited || status.ExitCode != 3 {
		t.Fatalf("unexpected wait status: %#v", status)
	}
}

func TestParseDBTimeFormats(t *testing.T) {
	for _, value := range []string{
		"2026-05-21 07:00:00",
		time.Date(2026, 5, 21, 7, 0, 0, 1, time.UTC).Format(time.RFC3339Nano),
		time.Date(2026, 5, 21, 7, 0, 0, 0, time.UTC).Format(time.RFC3339),
	} {
		if _, err := ParseDBTime(value); err != nil {
			t.Fatalf("ParseDBTime(%q) failed: %v", value, err)
		}
	}
}

func TestSplitPortsTrimsEmptyParts(t *testing.T) {
	ports := SplitPorts(" 80:80, ,443:443 ")
	if len(ports) != 2 || ports[0] != "80:80" || ports[1] != "443:443" {
		t.Fatalf("unexpected ports: %#v", ports)
	}
	if SplitPorts("  ") != nil {
		t.Fatal("expected blank ports to decode to nil")
	}
}
