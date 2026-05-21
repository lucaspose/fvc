package hostprune

import (
	"database/sql"
	"os"
	"path/filepath"
	"testing"

	"github.com/lucaspose/fvc/internal"
	_ "modernc.org/sqlite"
)

func newPruneTestDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("open db failed: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	_, err = db.Exec(`CREATE TABLE vms (
		id TEXT,
		status TEXT,
		drive_path TEXT,
		console_path TEXT
	)`)
	if err != nil {
		t.Fatalf("schema setup failed: %v", err)
	}
	return db
}

func TestOrphanActiveDrivesRemovesUnreferencedExt4(t *testing.T) {
	db := newPruneTestDB(t)
	activeDir := t.TempDir()
	kept := filepath.Join(activeDir, "kept.ext4")
	orphan := filepath.Join(activeDir, "orphan.ext4")
	ignored := filepath.Join(activeDir, "note.txt")
	for path, content := range map[string]string{kept: "kept", orphan: "orphan", ignored: "ignore"} {
		if err := os.WriteFile(path, []byte(content), 0644); err != nil {
			t.Fatalf("seed %s failed: %v", path, err)
		}
	}
	if _, err := db.Exec(`INSERT INTO vms (id, drive_path) VALUES (?, ?)`, "vm-1", kept); err != nil {
		t.Fatalf("insert vm failed: %v", err)
	}

	result, err := OrphanActiveDrives(db, activeDir, false)
	if err != nil {
		t.Fatalf("OrphanActiveDrives failed: %v", err)
	}
	if result.RemovedFiles != 1 || len(result.Items) != 1 || result.Items[0].Path != orphan {
		t.Fatalf("unexpected result: %#v", result)
	}
	if _, err := os.Stat(orphan); !os.IsNotExist(err) {
		t.Fatalf("expected orphan removed, got err=%v", err)
	}
	if _, err := os.Stat(kept); err != nil {
		t.Fatalf("expected referenced drive kept: %v", err)
	}
	if _, err := os.Stat(ignored); err != nil {
		t.Fatalf("expected non-ext4 ignored file kept: %v", err)
	}
}

func TestRuntimeFilesPreservesRunningVMFiles(t *testing.T) {
	db := newPruneTestDB(t)
	runtimeDir := t.TempDir()
	keepSocket := filepath.Join(runtimeDir, "fvc-vm-1.socket")
	keepConsole := filepath.Join(runtimeDir, "fvc-vm-1.console.in")
	orphan := filepath.Join(runtimeDir, "fvc-orphan.socket")
	ignored := filepath.Join(runtimeDir, "other.sock")
	for path, content := range map[string]string{keepSocket: "socket", keepConsole: "console", orphan: "orphan", ignored: "ignore"} {
		if err := os.WriteFile(path, []byte(content), 0644); err != nil {
			t.Fatalf("seed %s failed: %v", path, err)
		}
	}
	if _, err := db.Exec(`INSERT INTO vms (id, status, console_path) VALUES (?, ?, ?)`, "vm-1", internal.VmRunning, keepConsole); err != nil {
		t.Fatalf("insert vm failed: %v", err)
	}

	result, err := RuntimeFiles(db, runtimeDir, false)
	if err != nil {
		t.Fatalf("RuntimeFiles failed: %v", err)
	}
	if result.RemovedFiles != 1 || len(result.Items) != 1 || result.Items[0].Path != orphan {
		t.Fatalf("unexpected result: %#v", result)
	}
	for _, path := range []string{keepSocket, keepConsole, ignored} {
		if _, err := os.Stat(path); err != nil {
			t.Fatalf("expected %s kept: %v", path, err)
		}
	}
	if _, err := os.Stat(orphan); !os.IsNotExist(err) {
		t.Fatalf("expected orphan runtime file removed, got err=%v", err)
	}
}

func TestRuntimeFilesDryRunKeepsOrphan(t *testing.T) {
	db := newPruneTestDB(t)
	runtimeDir := t.TempDir()
	orphan := filepath.Join(runtimeDir, "fvc-orphan.socket")
	if err := os.WriteFile(orphan, []byte("orphan"), 0644); err != nil {
		t.Fatalf("seed orphan failed: %v", err)
	}

	result, err := RuntimeFiles(db, runtimeDir, true)
	if err != nil {
		t.Fatalf("RuntimeFiles dry-run failed: %v", err)
	}
	if result.RemovedFiles != 1 || len(result.Items) != 1 {
		t.Fatalf("unexpected result: %#v", result)
	}
	if _, err := os.Stat(orphan); err != nil {
		t.Fatalf("expected dry-run to keep orphan: %v", err)
	}
}
