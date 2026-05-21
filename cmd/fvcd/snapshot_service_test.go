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

func TestSnapshotCreateRequiresStoppedVM(t *testing.T) {
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("failed to open db: %v", err)
	}
	defer db.Close()
	if err := ensureSchema(db); err != nil {
		t.Fatalf("ensureSchema failed: %v", err)
	}
	dir := t.TempDir()
	drivePath := filepath.Join(dir, "vm.ext4")
	if err := os.WriteFile(drivePath, []byte("drive"), 0644); err != nil {
		t.Fatalf("failed to seed drive: %v", err)
	}
	if _, err := db.Exec(`INSERT INTO vms (id, status, image, drive_path) VALUES (?, ?, ?, ?)`, "vm-running", "running", "ubuntu", drivePath); err != nil {
		t.Fatalf("failed to insert vm: %v", err)
	}

	store := NewImageStore(DaemonConfig{BaseDir: dir, CacheDir: filepath.Join(dir, "cache"), ActiveDir: filepath.Join(dir, "active"), SnapshotDir: filepath.Join(dir, "snapshots")})
	if err := store.Init(); err != nil {
		t.Fatalf("store init failed: %v", err)
	}
	server := Server{DB: db, Store: store}
	res, err := server.SnapshotCreate(context.Background(), &proto.SnapshotCreateRequest{VmId: "vm-running", Name: "clean"})
	if err != nil {
		t.Fatalf("SnapshotCreate returned transport error: %v", err)
	}
	if res.Success || !strings.Contains(res.Message, "stopped") {
		t.Fatalf("expected running vm snapshot rejection, got %#v", res)
	}
}

func TestSnapshotCreateListRestoreRemove(t *testing.T) {
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("failed to open db: %v", err)
	}
	defer db.Close()
	if err := ensureSchema(db); err != nil {
		t.Fatalf("ensureSchema failed: %v", err)
	}
	dir := t.TempDir()
	store := NewImageStore(DaemonConfig{BaseDir: dir, CacheDir: filepath.Join(dir, "cache"), ActiveDir: filepath.Join(dir, "active"), SnapshotDir: filepath.Join(dir, "snapshots")})
	if err := store.Init(); err != nil {
		t.Fatalf("store init failed: %v", err)
	}
	drivePath := filepath.Join(store.activeDir, "vm-snap.ext4")
	if err := os.WriteFile(drivePath, []byte("before"), 0644); err != nil {
		t.Fatalf("failed to seed drive: %v", err)
	}
	if _, err := db.Exec(`INSERT INTO vms (id, status, image, drive_path) VALUES (?, ?, ?, ?)`, "vm-snap", "stopped", "ubuntu", drivePath); err != nil {
		t.Fatalf("failed to insert vm: %v", err)
	}

	server := Server{DB: db, Store: store}
	createRes, err := server.SnapshotCreate(context.Background(), &proto.SnapshotCreateRequest{VmId: "vm-snap", Name: "clean"})
	if err != nil {
		t.Fatalf("SnapshotCreate returned transport error: %v", err)
	}
	if !createRes.Success || createRes.Snapshot.GetName() != "clean" {
		t.Fatalf("unexpected create response: %#v", createRes)
	}

	listRes, err := server.SnapshotList(context.Background(), &proto.SnapshotListRequest{VmId: "vm-snap"})
	if err != nil {
		t.Fatalf("SnapshotList returned transport error: %v", err)
	}
	if !listRes.Success || len(listRes.Snapshots) != 1 {
		t.Fatalf("unexpected list response: %#v", listRes)
	}

	if err := os.WriteFile(drivePath, []byte("after"), 0644); err != nil {
		t.Fatalf("failed to mutate drive: %v", err)
	}
	restoreRes, err := server.SnapshotRestore(context.Background(), &proto.SnapshotRestoreRequest{VmId: "vm-snap", Name: "clean"})
	if err != nil {
		t.Fatalf("SnapshotRestore returned transport error: %v", err)
	}
	if !restoreRes.Success {
		t.Fatalf("unexpected restore response: %#v", restoreRes)
	}
	if got, err := os.ReadFile(drivePath); err != nil || string(got) != "before" {
		t.Fatalf("unexpected restored drive contents %q err=%v", got, err)
	}

	rmRes, err := server.SnapshotRemove(context.Background(), &proto.SnapshotRemoveRequest{VmId: "vm-snap", Name: "clean"})
	if err != nil {
		t.Fatalf("SnapshotRemove returned transport error: %v", err)
	}
	if !rmRes.Success {
		t.Fatalf("unexpected remove response: %#v", rmRes)
	}
}
