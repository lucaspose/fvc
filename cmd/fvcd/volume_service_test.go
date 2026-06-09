package main

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/lucaspose/fvc/internal"
	"github.com/lucaspose/fvc/proto"
	_ "modernc.org/sqlite"
)

type volumeTestRunner struct{}

func (volumeTestRunner) Run(name string, args ...string) error {
	if name != "truncate" {
		return nil
	}
	if len(args) != 3 || args[0] != "-s" {
		return nil
	}
	size := strings.TrimSuffix(args[1], "M")
	mb, err := strconv.ParseInt(size, 10, 64)
	if err != nil {
		return err
	}
	file, err := os.Create(args[2])
	if err != nil {
		return err
	}
	defer file.Close()
	return file.Truncate(mb * 1024 * 1024)
}

func newVolumeTestServer(t *testing.T) (*Server, string) {
	t.Helper()
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("open db failed: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if err := configureStateDB(db); err != nil {
		t.Fatalf("configure db failed: %v", err)
	}
	if err := ensureSchema(db); err != nil {
		t.Fatalf("ensure schema failed: %v", err)
	}
	baseDir := t.TempDir()
	return &Server{DB: db, Config: DaemonConfig{BaseDir: baseDir}, Runner: volumeTestRunner{}}, baseDir
}

func TestVolumeCreateListInspectRemove(t *testing.T) {
	server, _ := newVolumeTestServer(t)

	created, err := server.VolumeCreate(context.Background(), &proto.VolumeCreateRequest{Name: "data", SizeMb: 2})
	if err != nil {
		t.Fatalf("VolumeCreate returned transport error: %v", err)
	}
	if !created.GetSuccess() || created.GetVolume().GetName() != "data" || created.GetVolume().GetSizeBytes() != 2*1024*1024 {
		t.Fatalf("unexpected create response: %#v", created)
	}

	listed, err := server.VolumeList(context.Background(), &proto.VolumeListRequest{})
	if err != nil {
		t.Fatalf("VolumeList returned transport error: %v", err)
	}
	if len(listed.GetVolumes()) != 1 || listed.GetVolumes()[0].GetName() != "data" {
		t.Fatalf("unexpected list response: %#v", listed)
	}

	inspected, err := server.VolumeInspect(context.Background(), &proto.VolumeInspectRequest{Name: "data"})
	if err != nil {
		t.Fatalf("VolumeInspect returned transport error: %v", err)
	}
	if !inspected.GetSuccess() || inspected.GetVolume().GetPath() == "" {
		t.Fatalf("unexpected inspect response: %#v", inspected)
	}

	removed, err := server.VolumeRemove(context.Background(), &proto.VolumeRemoveRequest{Name: "data"})
	if err != nil {
		t.Fatalf("VolumeRemove returned transport error: %v", err)
	}
	if !removed.GetSuccess() {
		t.Fatalf("unexpected remove response: %#v", removed)
	}
	if _, err := os.Stat(removed.GetVolume().GetPath()); !os.IsNotExist(err) {
		t.Fatalf("expected volume file removed, got %v", err)
	}
}

func TestVolumeRemoveProtectsUsedAndRunningVolumes(t *testing.T) {
	server, _ := newVolumeTestServer(t)
	if _, err := server.VolumeCreate(context.Background(), &proto.VolumeCreateRequest{Name: "data", SizeMb: 1}); err != nil {
		t.Fatalf("VolumeCreate failed: %v", err)
	}
	if _, err := server.DB.Exec(`INSERT INTO vms (id, status, image, volumes) VALUES (?, ?, ?, ?)`, "vm-1", internal.VmStopped, "ubuntu", "data:/data"); err != nil {
		t.Fatalf("insert stopped vm failed: %v", err)
	}

	res, err := server.VolumeRemove(context.Background(), &proto.VolumeRemoveRequest{Name: "data"})
	if err != nil {
		t.Fatalf("VolumeRemove returned transport error: %v", err)
	}
	if res.GetSuccess() || !strings.Contains(res.GetMessage(), "use --force") {
		t.Fatalf("expected used volume rejection, got %#v", res)
	}

	forced, err := server.VolumeRemove(context.Background(), &proto.VolumeRemoveRequest{Name: "data", Force: true})
	if err != nil {
		t.Fatalf("VolumeRemove force returned transport error: %v", err)
	}
	if !forced.GetSuccess() {
		t.Fatalf("expected forced remove of stopped reference, got %#v", forced)
	}

	if _, err := server.VolumeCreate(context.Background(), &proto.VolumeCreateRequest{Name: "live", SizeMb: 1}); err != nil {
		t.Fatalf("VolumeCreate live failed: %v", err)
	}
	if _, err := server.DB.Exec(`INSERT INTO vms (id, status, image, volumes) VALUES (?, ?, ?, ?)`, "vm-2", internal.VmRunning, "ubuntu", "live:/data"); err != nil {
		t.Fatalf("insert running vm failed: %v", err)
	}
	live, err := server.VolumeRemove(context.Background(), &proto.VolumeRemoveRequest{Name: "live", Force: true})
	if err != nil {
		t.Fatalf("VolumeRemove live returned transport error: %v", err)
	}
	if live.GetSuccess() || !strings.Contains(live.GetMessage(), "running") {
		t.Fatalf("expected running volume rejection, got %#v", live)
	}
}

func TestVolumePruneOnlyRemovesUnused(t *testing.T) {
	server, baseDir := newVolumeTestServer(t)
	for _, name := range []string{"used", "unused"} {
		if _, err := server.VolumeCreate(context.Background(), &proto.VolumeCreateRequest{Name: name, SizeMb: 1}); err != nil {
			t.Fatalf("VolumeCreate %s failed: %v", name, err)
		}
	}
	if _, err := server.DB.Exec(`INSERT INTO vms (id, status, image, volumes) VALUES (?, ?, ?, ?)`, "vm-1", internal.VmStopped, "ubuntu", "used:/data"); err != nil {
		t.Fatalf("insert vm failed: %v", err)
	}

	res, err := server.VolumePrune(context.Background(), &proto.VolumePruneRequest{})
	if err != nil {
		t.Fatalf("VolumePrune returned transport error: %v", err)
	}
	if !res.GetSuccess() || res.GetRemovedVolumes() != 1 || len(res.GetVolumes()) != 1 || res.GetVolumes()[0].GetName() != "unused" {
		t.Fatalf("unexpected prune response: %#v", res)
	}
	if _, err := os.Stat(filepath.Join(baseDir, "volumes", "used.ext4")); err != nil {
		t.Fatalf("expected used volume to remain: %v", err)
	}
	if _, err := os.Stat(filepath.Join(baseDir, "volumes", "unused.ext4")); !os.IsNotExist(err) {
		t.Fatalf("expected unused volume removed, got %v", err)
	}
}
