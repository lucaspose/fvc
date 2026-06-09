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

func TestBuildImageStreamRejectsNilRequest(t *testing.T) {
	server := Server{}
	stream := newOperationEventStream()

	if err := server.BuildImageStream(nil, stream); err != nil {
		t.Fatalf("BuildImageStream returned transport error: %v", err)
	}

	event := findOperationEvent(stream.events, "validate", "error")
	if event == nil {
		t.Fatalf("expected validate error event, got %#v", stream.events)
	}
	if event.GetErrorMessage() != "build request is required" {
		t.Fatalf("unexpected error message: %q", event.GetErrorMessage())
	}
}

func TestBuildImageStreamReportsPlanError(t *testing.T) {
	server := Server{}
	stream := newOperationEventStream()

	if err := server.BuildImageStream(&proto.BuildImageRequest{ContextPath: t.TempDir(), Tag: "broken"}, stream); err != nil {
		t.Fatalf("BuildImageStream returned transport error: %v", err)
	}

	if event := findOperationEvent(stream.events, "plan", "running"); event == nil {
		t.Fatalf("expected plan running event, got %#v", stream.events)
	}
	event := findOperationEvent(stream.events, "plan", "error")
	if event == nil {
		t.Fatalf("expected plan error event, got %#v", stream.events)
	}
	if !strings.Contains(event.GetMessage(), "Fvcfile parse failed") {
		t.Fatalf("unexpected plan error message: %q", event.GetMessage())
	}
}

func TestBuildImageStreamReportsBuildError(t *testing.T) {
	dir := t.TempDir()
	fvcfile := `[image]
from = "ubuntu"
tag = "ubuntu-web"
`
	if err := os.WriteFile(filepath.Join(dir, "Fvcfile"), []byte(fvcfile), 0644); err != nil {
		t.Fatalf("failed to write Fvcfile: %v", err)
	}
	store := NewImageStore(DaemonConfig{
		BaseDir:      dir,
		CacheDir:     filepath.Join(dir, "cache"),
		ActiveDir:    filepath.Join(dir, "active"),
		SnapshotDir:  filepath.Join(dir, "snapshots"),
		KernelPath:   filepath.Join(dir, "kernel", "vmlinux.bin"),
		ImageBaseURL: "file:///not-http",
	})
	if err := store.Init(); err != nil {
		t.Fatalf("store init failed: %v", err)
	}
	server := Server{Store: store}
	stream := newOperationEventStream()

	if err := server.BuildImageStream(&proto.BuildImageRequest{ContextPath: dir}, stream); err != nil {
		t.Fatalf("BuildImageStream returned transport error: %v", err)
	}

	if event := findOperationEvent(stream.events, "plan", "complete"); event == nil {
		t.Fatalf("expected plan complete before build failure, got %#v", stream.events)
	}
	event := findOperationEvent(stream.events, "build", "error")
	if event == nil {
		t.Fatalf("expected build error event, got %#v", stream.events)
	}
	if event.GetImage() != "ubuntu-web" {
		t.Fatalf("expected image tag on error, got %q", event.GetImage())
	}
	if !strings.Contains(event.GetErrorMessage(), "image base URL must use http or https") {
		t.Fatalf("unexpected build error message: %q", event.GetErrorMessage())
	}
}

func TestImageRemoveProtectsUsedImage(t *testing.T) {
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
	if err := os.WriteFile(store.cachedImagePath("ubuntu"), []byte("rootfs"), 0644); err != nil {
		t.Fatalf("failed to seed image: %v", err)
	}
	if _, err := db.Exec(`INSERT INTO vms (id, status, image) VALUES (?, ?, ?)`, "vm-used", "stopped", "ubuntu"); err != nil {
		t.Fatalf("failed to insert vm: %v", err)
	}

	server := Server{DB: db, Store: store}
	res, err := server.ImageRemove(context.Background(), &proto.ImageRemoveRequest{Image: "ubuntu"})
	if err != nil {
		t.Fatalf("ImageRemove returned transport error: %v", err)
	}
	if res.Success || !strings.Contains(res.Message, "used") {
		t.Fatalf("expected image usage protection, got %#v", res)
	}
}

func TestImagePruneKeepsUsedImage(t *testing.T) {
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
	if err := os.WriteFile(store.cachedImagePath("used"), []byte("used"), 0644); err != nil {
		t.Fatalf("failed to seed used image: %v", err)
	}
	if err := os.WriteFile(store.cachedImagePath("unused"), []byte("unused"), 0644); err != nil {
		t.Fatalf("failed to seed unused image: %v", err)
	}
	if _, err := db.Exec(`INSERT INTO vms (id, status, image) VALUES (?, ?, ?)`, "vm-used", "stopped", "used"); err != nil {
		t.Fatalf("failed to insert vm: %v", err)
	}

	server := Server{DB: db, Store: store}
	res, err := server.ImagePrune(context.Background(), &proto.ImagePruneRequest{})
	if err != nil {
		t.Fatalf("ImagePrune returned transport error: %v", err)
	}
	if !res.Success || res.RemovedImages != 1 {
		t.Fatalf("unexpected prune response: %#v", res)
	}
	if _, err := store.InspectImage("used"); err != nil {
		t.Fatalf("expected used image to remain: %v", err)
	}
	if _, err := store.InspectImage("unused"); err == nil {
		t.Fatal("expected unused image to be removed")
	}
}

func TestImageImportRejectsHostPathOutsideBaseDir(t *testing.T) {
	baseDir := t.TempDir()
	outsideDir := t.TempDir()
	sourcePath := filepath.Join(outsideDir, "rootfs.ext4")
	if err := os.WriteFile(sourcePath, []byte("rootfs"), 0644); err != nil {
		t.Fatalf("failed to write source image: %v", err)
	}
	server := Server{Config: DaemonConfig{BaseDir: baseDir}}

	res, err := server.ImageImport(context.Background(), &proto.ImageImportRequest{SourcePath: sourcePath, Image: "custom"})
	if err != nil {
		t.Fatalf("ImageImport returned transport error: %v", err)
	}
	if res.Success || !strings.Contains(res.Message, "restricted") {
		t.Fatalf("expected restricted host path response, got %#v", res)
	}
}

func TestImageExportRejectsSymlinkDestination(t *testing.T) {
	baseDir := t.TempDir()
	target := filepath.Join(baseDir, "target.ext4")
	link := filepath.Join(baseDir, "link.ext4")
	if err := os.WriteFile(target, []byte("rootfs"), 0644); err != nil {
		t.Fatalf("failed to write target: %v", err)
	}
	if err := os.Symlink(target, link); err != nil {
		t.Fatalf("failed to create symlink: %v", err)
	}
	server := Server{Config: DaemonConfig{BaseDir: baseDir}}

	if err := server.validateHostImagePath(link, true); err == nil || !strings.Contains(err.Error(), "symlink") {
		t.Fatalf("expected symlink destination to be rejected, got %v", err)
	}
}
