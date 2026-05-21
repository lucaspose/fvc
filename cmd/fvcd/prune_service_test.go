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

func TestPruneCombinesImageActiveAndRuntimeItems(t *testing.T) {
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("failed to open db: %v", err)
	}
	defer db.Close()
	if err := ensureSchema(db); err != nil {
		t.Fatalf("ensureSchema failed: %v", err)
	}

	dir := t.TempDir()
	store := NewImageStore(DaemonConfig{
		BaseDir:     dir,
		CacheDir:    filepath.Join(dir, "cache"),
		ActiveDir:   filepath.Join(dir, "active"),
		SnapshotDir: filepath.Join(dir, "snapshots"),
	})
	if err := store.Init(); err != nil {
		t.Fatalf("store init failed: %v", err)
	}
	if err := os.WriteFile(store.cachedImagePath("ubuntu"), []byte("encoded"), 0644); err != nil {
		t.Fatalf("failed to write encoded image: %v", err)
	}
	legacyPath := filepath.Join(store.cacheDir, "ubuntu.ext4")
	if err := os.WriteFile(legacyPath, []byte("legacy"), 0644); err != nil {
		t.Fatalf("failed to write legacy image: %v", err)
	}
	orphanDrive := filepath.Join(store.activeDir, "orphan.ext4")
	if err := os.WriteFile(orphanDrive, []byte("drive"), 0644); err != nil {
		t.Fatalf("failed to write orphan drive: %v", err)
	}
	runtimeDir := filepath.Join(dir, "runtime")
	if err := os.MkdirAll(runtimeDir, 0755); err != nil {
		t.Fatalf("failed to create runtime dir: %v", err)
	}
	orphanRuntime := filepath.Join(runtimeDir, "fvc-orphan.socket")
	if err := os.WriteFile(orphanRuntime, []byte("runtime"), 0644); err != nil {
		t.Fatalf("failed to write orphan runtime file: %v", err)
	}

	server := Server{DB: db, Store: store, Config: DaemonConfig{ActiveDir: store.activeDir, RuntimeDir: runtimeDir}}
	res, err := server.Prune(context.Background(), &proto.PruneRequest{DryRun: true})
	if err != nil {
		t.Fatalf("Prune returned transport error: %v", err)
	}
	if !res.Success || !res.DryRun {
		t.Fatalf("unexpected prune response: %#v", res)
	}
	if res.RemovedFiles != 3 || res.FreedBytes != int64(len("legacy")+len("drive")+len("runtime")) {
		t.Fatalf("unexpected aggregate counts: %#v", res)
	}
	if len(res.Items) != 3 {
		t.Fatalf("expected three prune items, got %#v", res.Items)
	}
	for _, path := range []string{legacyPath, orphanDrive, orphanRuntime} {
		if _, err := os.Stat(path); err != nil {
			t.Fatalf("dry-run should keep %s: %v", path, err)
		}
	}
}

func TestPruneReportsImageCacheError(t *testing.T) {
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("failed to open db: %v", err)
	}
	defer db.Close()
	dir := t.TempDir()
	store := NewImageStore(DaemonConfig{
		BaseDir:     dir,
		CacheDir:    filepath.Join(dir, "missing-cache"),
		ActiveDir:   filepath.Join(dir, "active"),
		SnapshotDir: filepath.Join(dir, "snapshots"),
	})
	server := Server{DB: db, Store: store, Config: DaemonConfig{ActiveDir: store.activeDir, RuntimeDir: filepath.Join(dir, "runtime")}}

	res, err := server.Prune(context.Background(), &proto.PruneRequest{})
	if err != nil {
		t.Fatalf("Prune returned transport error: %v", err)
	}
	if res.Success || !strings.Contains(res.Message, "cache directory read failed") {
		t.Fatalf("expected cache read failure, got %#v", res)
	}
}

func TestPruneOrphanActiveDrives(t *testing.T) {
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("failed to open db: %v", err)
	}
	defer db.Close()
	if err := ensureSchema(db); err != nil {
		t.Fatalf("ensureSchema failed: %v", err)
	}

	activeDir := t.TempDir()
	knownPath := filepath.Join(activeDir, "known.ext4")
	orphanPath := filepath.Join(activeDir, "orphan.ext4")
	if err := os.WriteFile(knownPath, []byte("known"), 0644); err != nil {
		t.Fatalf("failed to write known drive: %v", err)
	}
	if err := os.WriteFile(orphanPath, []byte("orphan"), 0644); err != nil {
		t.Fatalf("failed to write orphan drive: %v", err)
	}
	if _, err := db.Exec(`INSERT INTO vms (id, status, image, drive_path) VALUES (?, ?, ?, ?)`, "vm-known", "stopped", "ubuntu", knownPath); err != nil {
		t.Fatalf("failed to insert vm: %v", err)
	}

	server := Server{DB: db, Config: DaemonConfig{ActiveDir: activeDir}}
	result, err := server.pruneOrphanActiveDrives(false)
	if err != nil {
		t.Fatalf("pruneOrphanActiveDrives failed: %v", err)
	}
	if result.RemovedFiles != 1 || result.FreedBytes != int64(len("orphan")) {
		t.Fatalf("unexpected prune result: %#v", result)
	}
	if _, err := os.Stat(knownPath); err != nil {
		t.Fatalf("expected known drive to stay: %v", err)
	}
	if _, err := os.Stat(orphanPath); !os.IsNotExist(err) {
		t.Fatalf("expected orphan drive removed, got err=%v", err)
	}
}

func TestPruneRuntimeFilesPreservesRunningVMFiles(t *testing.T) {
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("failed to open db: %v", err)
	}
	defer db.Close()
	if err := ensureSchema(db); err != nil {
		t.Fatalf("ensureSchema failed: %v", err)
	}

	runtimeDir := t.TempDir()
	socketPath := filepath.Join(runtimeDir, "fvc-vm-running.socket")
	consolePath := filepath.Join(runtimeDir, "fvc-vm-running.console.in")
	orphanPath := filepath.Join(runtimeDir, "fvc-orphan.socket")
	for path, contents := range map[string]string{
		socketPath:  "socket",
		consolePath: "console",
		orphanPath:  "orphan",
	} {
		if err := os.WriteFile(path, []byte(contents), 0644); err != nil {
			t.Fatalf("failed to write runtime file %s: %v", path, err)
		}
	}
	if _, err := db.Exec(`INSERT INTO vms (id, status, image, console_path) VALUES (?, ?, ?, ?)`, "vm-running", "running", "ubuntu", consolePath); err != nil {
		t.Fatalf("failed to insert running vm: %v", err)
	}

	server := Server{DB: db, Config: DaemonConfig{RuntimeDir: runtimeDir}}
	result, err := server.pruneRuntimeFiles(false)
	if err != nil {
		t.Fatalf("pruneRuntimeFiles failed: %v", err)
	}
	if result.RemovedFiles != 1 || result.FreedBytes != int64(len("orphan")) {
		t.Fatalf("unexpected prune result: %#v", result)
	}
	for _, path := range []string{socketPath, consolePath} {
		if _, err := os.Stat(path); err != nil {
			t.Fatalf("expected active runtime file to stay: %s err=%v", path, err)
		}
	}
	if _, err := os.Stat(orphanPath); !os.IsNotExist(err) {
		t.Fatalf("expected orphan runtime file removed, got err=%v", err)
	}
}

func TestPruneRuntimeFilesDryRunKeepsOrphan(t *testing.T) {
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("failed to open db: %v", err)
	}
	defer db.Close()
	if err := ensureSchema(db); err != nil {
		t.Fatalf("ensureSchema failed: %v", err)
	}

	runtimeDir := t.TempDir()
	orphanPath := filepath.Join(runtimeDir, "fvc-orphan.socket")
	if err := os.WriteFile(orphanPath, []byte("orphan"), 0644); err != nil {
		t.Fatalf("failed to write orphan runtime file: %v", err)
	}

	server := Server{DB: db, Config: DaemonConfig{RuntimeDir: runtimeDir}}
	result, err := server.pruneRuntimeFiles(true)
	if err != nil {
		t.Fatalf("pruneRuntimeFiles dry-run failed: %v", err)
	}
	if result.RemovedFiles != 1 || len(result.Items) != 1 {
		t.Fatalf("unexpected dry-run result: %#v", result)
	}
	if _, err := os.Stat(orphanPath); err != nil {
		t.Fatalf("expected orphan runtime file to remain: %v", err)
	}
}
