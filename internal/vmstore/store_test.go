package vmstore

import (
	"database/sql"
	"testing"

	"github.com/lucaspose/fvc/internal"
	_ "modernc.org/sqlite"
)

func openTestDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("failed to open db: %v", err)
	}
	t.Cleanup(func() {
		_ = db.Close()
	})
	if err := ConfigureDB(db); err != nil {
		t.Fatalf("ConfigureDB failed: %v", err)
	}
	if err := EnsureSchema(db); err != nil {
		t.Fatalf("EnsureSchema failed: %v", err)
	}
	return db
}

func TestEnsureSchemaAllowsEmptyNamesButKeepsNamesUnique(t *testing.T) {
	db := openTestDB(t)
	if _, err := db.Exec(`INSERT INTO vms (id, name, status, image) VALUES (?, ?, ?, ?)`, "vm-a", "api", internal.VmStopped, "ubuntu"); err != nil {
		t.Fatalf("insert named vm failed: %v", err)
	}
	if _, err := db.Exec(`INSERT INTO vms (id, name, status, image) VALUES (?, ?, ?, ?)`, "vm-b", "api", internal.VmStopped, "ubuntu"); err == nil {
		t.Fatal("expected duplicate non-empty VM name to fail")
	}
	if _, err := db.Exec(`INSERT INTO vms (id, name, status, image) VALUES (?, ?, ?, ?)`, "vm-c", "", internal.VmStopped, "ubuntu"); err != nil {
		t.Fatalf("first empty name insert failed: %v", err)
	}
	if _, err := db.Exec(`INSERT INTO vms (id, name, status, image) VALUES (?, ?, ?, ?)`, "vm-d", "", internal.VmStopped, "ubuntu"); err != nil {
		t.Fatalf("second empty name insert failed: %v", err)
	}
}

func TestReconcileMarksStaleVMStopped(t *testing.T) {
	db := openTestDB(t)
	_, err := db.Exec(`INSERT INTO vms (id, pid, status, image, process_start_time, tap_name, guest_ip, mac_address, ports) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		"stale-vm", 123, internal.VmRunning, "ubuntu", "start", "tap0", "172.16.0.2", "02:FC:00:00:00:01", "80:80")
	if err != nil {
		t.Fatalf("insert vm failed: %v", err)
	}

	var stale []StaleVM
	err = Reconcile(db, func(pid int, expectedStartTime string) bool {
		return false
	}, func(vm StaleVM) {
		stale = append(stale, vm)
	})
	if err != nil {
		t.Fatalf("Reconcile failed: %v", err)
	}
	if len(stale) != 1 || stale[0].ID != "stale-vm" || stale[0].PortsValue != "80:80" {
		t.Fatalf("unexpected stale callback values: %#v", stale)
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

func TestReconcileKeepsLiveVMRunning(t *testing.T) {
	db := openTestDB(t)
	_, err := db.Exec(`INSERT INTO vms (id, pid, status, image, process_start_time) VALUES (?, ?, ?, ?, ?)`,
		"live-vm", 456, internal.VmRunning, "ubuntu", "start")
	if err != nil {
		t.Fatalf("insert vm failed: %v", err)
	}

	called := false
	err = Reconcile(db, func(pid int, expectedStartTime string) bool {
		return pid == 456 && expectedStartTime == "start"
	}, func(vm StaleVM) {
		called = true
	})
	if err != nil {
		t.Fatalf("Reconcile failed: %v", err)
	}
	if called {
		t.Fatal("did not expect stale callback for live VM")
	}

	var status string
	if err := db.QueryRow(`SELECT status FROM vms WHERE id = ?`, "live-vm").Scan(&status); err != nil {
		t.Fatalf("query vm failed: %v", err)
	}
	if status != internal.VmRunning {
		t.Fatalf("expected live vm to remain running, got %s", status)
	}
}

func TestResolveRefByIDNameAndPrefix(t *testing.T) {
	db := openTestDB(t)
	if _, err := db.Exec(`INSERT INTO vms (id, name, status, image) VALUES (?, ?, ?, ?)`, "abcdef01", "api", internal.VmStopped, "ubuntu"); err != nil {
		t.Fatalf("insert first vm failed: %v", err)
	}
	if _, err := db.Exec(`INSERT INTO vms (id, name, status, image) VALUES (?, ?, ?, ?)`, "12345678", "", internal.VmStopped, "ubuntu"); err != nil {
		t.Fatalf("insert second vm failed: %v", err)
	}

	for _, tc := range []struct {
		ref  string
		want string
	}{
		{ref: "abcdef01", want: "abcdef01"},
		{ref: "api", want: "abcdef01"},
		{ref: "1234", want: "12345678"},
	} {
		got, err := ResolveRef(db, tc.ref)
		if err != nil {
			t.Fatalf("ResolveRef(%q) failed: %v", tc.ref, err)
		}
		if got != tc.want {
			t.Fatalf("ResolveRef(%q)=%q, want %q", tc.ref, got, tc.want)
		}
	}
}

func TestResolveRefRejectsAmbiguousPrefix(t *testing.T) {
	db := openTestDB(t)
	if _, err := db.Exec(`INSERT INTO vms (id, status, image) VALUES (?, ?, ?)`, "abc-one", internal.VmStopped, "ubuntu"); err != nil {
		t.Fatalf("insert first vm failed: %v", err)
	}
	if _, err := db.Exec(`INSERT INTO vms (id, status, image) VALUES (?, ?, ?)`, "abc-two", internal.VmStopped, "ubuntu"); err != nil {
		t.Fatalf("insert second vm failed: %v", err)
	}
	if _, err := ResolveRef(db, "abc"); err == nil {
		t.Fatal("expected ambiguous prefix error")
	}
}

func TestNameExistsIgnoresRequestedVM(t *testing.T) {
	db := openTestDB(t)
	if _, err := db.Exec(`INSERT INTO vms (id, name, status, image) VALUES (?, ?, ?, ?)`, "vm-a", "api", internal.VmStopped, "ubuntu"); err != nil {
		t.Fatalf("insert vm failed: %v", err)
	}
	exists, err := NameExists(db, "api", "vm-a")
	if err != nil {
		t.Fatalf("NameExists failed: %v", err)
	}
	if exists {
		t.Fatal("expected own name to be ignored")
	}
	exists, err = NameExists(db, "api", "vm-b")
	if err != nil {
		t.Fatalf("NameExists failed: %v", err)
	}
	if !exists {
		t.Fatal("expected name to exist for another VM")
	}
}
