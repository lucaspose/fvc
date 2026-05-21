package imagestore

import (
	"os"
	"path/filepath"
	"testing"
)

func newSnapshotTestStore(t *testing.T) *Store {
	t.Helper()
	dir := t.TempDir()
	store := New(Config{
		BaseDir:     dir,
		CacheDir:    filepath.Join(dir, "cache"),
		ActiveDir:   filepath.Join(dir, "active"),
		SnapshotDir: filepath.Join(dir, "snapshots"),
		KernelPath:  filepath.Join(dir, "vmlinux.bin"),
	})
	if err := store.Init(); err != nil {
		t.Fatalf("Init failed: %v", err)
	}
	return store
}

func TestSnapshotCreateListRestoreRemove(t *testing.T) {
	store := newSnapshotTestStore(t)
	drivePath := filepath.Join(store.activeDir, "vm-1.ext4")
	if err := os.WriteFile(drivePath, []byte("before"), 0644); err != nil {
		t.Fatalf("failed to seed drive: %v", err)
	}

	snapshotPath, size, err := store.CreateSnapshot("vm-1", drivePath, "clean")
	if err != nil {
		t.Fatalf("CreateSnapshot failed: %v", err)
	}
	if size != int64(len("before")) {
		t.Fatalf("unexpected snapshot size: %d", size)
	}
	if got, err := os.ReadFile(snapshotPath); err != nil || string(got) != "before" {
		t.Fatalf("unexpected snapshot contents %q err=%v", got, err)
	}

	if err := os.WriteFile(filepath.Join(store.snapshotVMDir("vm-1"), "note.txt"), []byte("ignore"), 0644); err != nil {
		t.Fatalf("failed to seed ignored file: %v", err)
	}

	snapshots, err := store.ListSnapshots("vm-1")
	if err != nil {
		t.Fatalf("ListSnapshots failed: %v", err)
	}
	if len(snapshots) != 1 || snapshots[0].Name != "clean" {
		t.Fatalf("unexpected snapshots: %#v", snapshots)
	}

	if err := os.WriteFile(drivePath, []byte("after"), 0644); err != nil {
		t.Fatalf("failed to mutate drive: %v", err)
	}
	if _, _, err := store.RestoreSnapshot("vm-1", drivePath, "clean"); err != nil {
		t.Fatalf("RestoreSnapshot failed: %v", err)
	}
	if got, err := os.ReadFile(drivePath); err != nil || string(got) != "before" {
		t.Fatalf("unexpected restored contents %q err=%v", got, err)
	}

	if _, err := store.RemoveSnapshot("vm-1", "clean"); err != nil {
		t.Fatalf("RemoveSnapshot failed: %v", err)
	}
	if _, err := os.Stat(snapshotPath); !os.IsNotExist(err) {
		t.Fatalf("expected snapshot removed, got err=%v", err)
	}
}

func TestSnapshotValidation(t *testing.T) {
	store := newSnapshotTestStore(t)
	drivePath := filepath.Join(store.activeDir, "vm-1.ext4")
	if err := os.WriteFile(drivePath, []byte("drive"), 0644); err != nil {
		t.Fatalf("failed to seed drive: %v", err)
	}
	if _, _, err := store.CreateSnapshot("vm-1", drivePath, "../bad"); err == nil {
		t.Fatal("expected unsafe snapshot name to be rejected")
	}
	if _, _, err := store.CreateSnapshot("../vm", drivePath, "clean"); err == nil {
		t.Fatal("expected unsafe vm id to be rejected")
	}
	if _, err := store.RemoveSnapshot("vm-1", "missing"); err == nil {
		t.Fatal("expected missing snapshot error")
	}
}
