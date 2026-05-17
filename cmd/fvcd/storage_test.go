package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func newTestStore(t *testing.T, baseURL string) *ImageStore {
	t.Helper()
	baseDir := t.TempDir()
	store := NewImageStore(DaemonConfig{
		BaseDir:      baseDir,
		CacheDir:     filepath.Join(baseDir, "cache"),
		ActiveDir:    filepath.Join(baseDir, "active"),
		KernelPath:   filepath.Join(baseDir, "vmlinux.bin"),
		ImageBaseURL: baseURL,
	})
	if err := store.Init(); err != nil {
		t.Fatalf("Init failed: %v", err)
	}
	return store
}

func TestPullImageStoresEncodedCachePath(t *testing.T) {
	store := newTestStore(t, "https://example.test/images")
	cachedPath := store.cachedImagePath("library/debian:bookworm")
	if err := os.WriteFile(cachedPath, []byte("rootfs"), 0644); err != nil {
		t.Fatalf("failed to seed cache: %v", err)
	}

	localPath, err := store.PullImageIfNeeded("library/debian:bookworm")
	if err != nil {
		t.Fatalf("PullImageIfNeeded failed: %v", err)
	}
	if filepath.Dir(localPath) != store.cacheDir {
		t.Fatalf("expected image under cache dir, got %s", localPath)
	}
	if strings.Contains(filepath.Base(localPath), "/") {
		t.Fatalf("expected encoded filename, got %s", localPath)
	}
	if got, err := os.ReadFile(localPath); err != nil || string(got) != "rootfs" {
		t.Fatalf("unexpected cache contents %q, err=%v", got, err)
	}

	remoteURL, err := store.imageURL("library/debian:bookworm")
	if err != nil {
		t.Fatalf("imageURL failed: %v", err)
	}
	if remoteURL != "https://example.test/images/library/debian:bookworm.ext4" {
		t.Fatalf("unexpected remote URL: %s", remoteURL)
	}
}

func TestPullImageRejectsTraversal(t *testing.T) {
	store := newTestStore(t, "https://example.invalid/images")
	if _, err := store.PullImageIfNeeded("../debian"); err == nil {
		t.Fatal("expected traversal image ref to be rejected")
	}
}

func TestListImagesDecodesCacheNames(t *testing.T) {
	store := newTestStore(t, "https://example.test/images")
	path := store.cachedImagePath("ubuntu:22.04")
	if err := os.WriteFile(path, []byte("rootfs"), 0644); err != nil {
		t.Fatalf("failed to seed image: %v", err)
	}

	images, err := store.ListImages()
	if err != nil {
		t.Fatalf("ListImages failed: %v", err)
	}
	if len(images) != 1 {
		t.Fatalf("expected one image, got %d", len(images))
	}
	if images[0].Name != "ubuntu:22.04" || images[0].SizeBytes != int64(len("rootfs")) {
		t.Fatalf("unexpected image details: %#v", images[0])
	}
}

func TestListImagesHandlesLegacyNames(t *testing.T) {
	store := newTestStore(t, "https://example.test/images")
	path := filepath.Join(store.cacheDir, "ubuntu.ext4")
	if err := os.WriteFile(path, []byte("legacy"), 0644); err != nil {
		t.Fatalf("failed to seed legacy image: %v", err)
	}

	images, err := store.ListImages()
	if err != nil {
		t.Fatalf("ListImages failed: %v", err)
	}
	if len(images) != 1 {
		t.Fatalf("expected one image, got %d", len(images))
	}
	if images[0].Name != "ubuntu" {
		t.Fatalf("unexpected legacy image name: %#v", images[0])
	}
}

func TestListImagesDeduplicatesLegacyWhenEncodedExists(t *testing.T) {
	store := newTestStore(t, "https://example.test/images")
	if err := os.WriteFile(store.cachedImagePath("ubuntu"), []byte("encoded"), 0644); err != nil {
		t.Fatalf("failed to seed encoded image: %v", err)
	}
	if err := os.WriteFile(filepath.Join(store.cacheDir, "ubuntu.ext4"), []byte("legacy"), 0644); err != nil {
		t.Fatalf("failed to seed legacy image: %v", err)
	}

	images, err := store.ListImages()
	if err != nil {
		t.Fatalf("ListImages failed: %v", err)
	}
	if len(images) != 1 {
		t.Fatalf("expected one deduplicated image, got %d", len(images))
	}
	if images[0].Name != "ubuntu" || !images[0].Encoded {
		t.Fatalf("expected encoded ubuntu image, got %#v", images[0])
	}
}

func TestPruneImageCacheRemovesLegacyDuplicate(t *testing.T) {
	store := newTestStore(t, "https://example.test/images")
	legacyPath := filepath.Join(store.cacheDir, "ubuntu.ext4")
	if err := os.WriteFile(store.cachedImagePath("ubuntu"), []byte("encoded"), 0644); err != nil {
		t.Fatalf("failed to seed encoded image: %v", err)
	}
	if err := os.WriteFile(legacyPath, []byte("legacy"), 0644); err != nil {
		t.Fatalf("failed to seed legacy image: %v", err)
	}

	result, err := store.PruneImageCache(false)
	if err != nil {
		t.Fatalf("PruneImageCache failed: %v", err)
	}
	if result.RemovedFiles != 1 || result.FreedBytes != int64(len("legacy")) {
		t.Fatalf("unexpected prune result: %#v", result)
	}
	if _, err := os.Stat(legacyPath); !os.IsNotExist(err) {
		t.Fatalf("expected legacy image removed, got err=%v", err)
	}
}

func TestPruneImageCacheDryRunKeepsLegacyDuplicate(t *testing.T) {
	store := newTestStore(t, "https://example.test/images")
	legacyPath := filepath.Join(store.cacheDir, "ubuntu.ext4")
	if err := os.WriteFile(store.cachedImagePath("ubuntu"), []byte("encoded"), 0644); err != nil {
		t.Fatalf("failed to seed encoded image: %v", err)
	}
	if err := os.WriteFile(legacyPath, []byte("legacy"), 0644); err != nil {
		t.Fatalf("failed to seed legacy image: %v", err)
	}

	result, err := store.PruneImageCache(true)
	if err != nil {
		t.Fatalf("PruneImageCache dry-run failed: %v", err)
	}
	if result.RemovedFiles != 1 || len(result.Items) != 1 {
		t.Fatalf("unexpected dry-run result: %#v", result)
	}
	if _, err := os.Stat(legacyPath); err != nil {
		t.Fatalf("expected legacy image to remain: %v", err)
	}
}

func TestListImagesSkipsInvalidDecodedNames(t *testing.T) {
	store := newTestStore(t, "https://example.test/images")
	path := filepath.Join(store.cacheDir, "__8.ext4")
	if err := os.WriteFile(path, []byte("bad"), 0644); err != nil {
		t.Fatalf("failed to seed invalid image: %v", err)
	}

	images, err := store.ListImages()
	if err != nil {
		t.Fatalf("ListImages failed: %v", err)
	}
	if len(images) != 0 {
		t.Fatalf("expected invalid image to be skipped, got %#v", images)
	}
}

func TestDownloadAtomicDoesNotLeavePartialDestination(t *testing.T) {
	dest := filepath.Join(t.TempDir(), "cache", "image.ext4")
	if err := downloadAtomic("://bad-url", dest); err == nil {
		t.Fatal("expected download to fail")
	}
	if _, err := os.Stat(dest); !os.IsNotExist(err) {
		t.Fatalf("expected no destination file after failed download, got err=%v", err)
	}
}

func TestImageStoreSnapshotCreateListRestoreRemove(t *testing.T) {
	store := newTestStore(t, "https://example.test/images")
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
		t.Fatalf("unexpected restored drive contents %q err=%v", got, err)
	}

	if _, err := store.RemoveSnapshot("vm-1", "clean"); err != nil {
		t.Fatalf("RemoveSnapshot failed: %v", err)
	}
	if _, err := os.Stat(snapshotPath); !os.IsNotExist(err) {
		t.Fatalf("expected snapshot removed, got err=%v", err)
	}
}

func TestSnapshotRejectsUnsafeName(t *testing.T) {
	store := newTestStore(t, "https://example.test/images")
	drivePath := filepath.Join(store.activeDir, "vm-1.ext4")
	if err := os.WriteFile(drivePath, []byte("drive"), 0644); err != nil {
		t.Fatalf("failed to seed drive: %v", err)
	}
	if _, _, err := store.CreateSnapshot("vm-1", drivePath, "../bad"); err == nil {
		t.Fatal("expected unsafe snapshot name to be rejected")
	}
}

func TestBuildImagePublishesTag(t *testing.T) {
	store := newTestStore(t, "https://example.test/images")
	if err := os.WriteFile(store.cachedImagePath("ubuntu"), []byte("base"), 0644); err != nil {
		t.Fatalf("failed to seed base image: %v", err)
	}
	contextDir := t.TempDir()
	sourcePath := filepath.Join(contextDir, "hello.txt")
	if err := os.WriteFile(sourcePath, []byte("hello"), 0644); err != nil {
		t.Fatalf("failed to write source file: %v", err)
	}

	runner := &fakeRunner{}
	path, err := store.BuildImage(BuildPlan{
		ContextPath: contextDir,
		BaseImage:   "ubuntu",
		Tag:         "ubuntu-copy",
		Copies: []BuildCopy{{
			SourcePath: sourcePath,
			DestPath:   "/tmp/hello.txt",
		}},
	}, runner)
	if err != nil {
		t.Fatalf("BuildImage failed: %v", err)
	}
	if path != store.cachedImagePath("ubuntu-copy") {
		t.Fatalf("unexpected image path: %s", path)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("expected built image to exist: %v", err)
	}
	if len(runner.calls) < 2 || !strings.HasPrefix(runner.calls[0], "mount -o loop ") || !strings.HasPrefix(runner.calls[len(runner.calls)-1], "umount ") {
		t.Fatalf("expected mount and umount calls, got %#v", runner.calls)
	}
}

func TestImageImportInspectExportTagRemove(t *testing.T) {
	store := newTestStore(t, "https://example.test/images")
	sourcePath := filepath.Join(t.TempDir(), "rootfs.ext4")
	if err := os.WriteFile(sourcePath, []byte("rootfs"), 0644); err != nil {
		t.Fatalf("failed to write source image: %v", err)
	}

	imported, err := store.ImportImage(sourcePath, "custom")
	if err != nil {
		t.Fatalf("ImportImage failed: %v", err)
	}
	if imported.Name != "custom" || imported.Digest == "" || imported.Metadata.Source != "import:"+sourcePath {
		t.Fatalf("unexpected imported image: %#v", imported)
	}

	inspected, err := store.InspectImage("custom")
	if err != nil {
		t.Fatalf("InspectImage failed: %v", err)
	}
	if inspected.Digest != imported.Digest {
		t.Fatalf("digest changed: %s != %s", inspected.Digest, imported.Digest)
	}

	exportPath := filepath.Join(t.TempDir(), "export.ext4")
	if _, err := store.ExportImage("custom", exportPath); err != nil {
		t.Fatalf("ExportImage failed: %v", err)
	}
	if got, err := os.ReadFile(exportPath); err != nil || string(got) != "rootfs" {
		t.Fatalf("unexpected exported contents %q err=%v", got, err)
	}

	tagged, err := store.TagImage("custom", "custom-copy")
	if err != nil {
		t.Fatalf("TagImage failed: %v", err)
	}
	if tagged.Name != "custom-copy" {
		t.Fatalf("unexpected tag result: %#v", tagged)
	}
	history, err := store.ImageHistory("custom-copy")
	if err != nil {
		t.Fatalf("ImageHistory failed: %v", err)
	}
	if len(history) == 0 || history[len(history)-1].Action != "tag" {
		t.Fatalf("unexpected history: %#v", history)
	}

	if err := store.RemoveImage("custom-copy"); err != nil {
		t.Fatalf("RemoveImage failed: %v", err)
	}
	if _, err := store.InspectImage("custom-copy"); err == nil {
		t.Fatal("expected removed image to be missing")
	}
}

func TestImagePruneSkipsUsedImages(t *testing.T) {
	store := newTestStore(t, "https://example.test/images")
	if err := os.WriteFile(store.cachedImagePath("used"), []byte("used"), 0644); err != nil {
		t.Fatalf("failed to seed used image: %v", err)
	}
	if err := os.WriteFile(store.cachedImagePath("unused"), []byte("unused"), 0644); err != nil {
		t.Fatalf("failed to seed unused image: %v", err)
	}

	result, err := store.PruneUnusedImages(map[string]bool{"used": true}, false)
	if err != nil {
		t.Fatalf("PruneUnusedImages failed: %v", err)
	}
	if result.RemovedImages != 1 || len(result.Images) != 1 || result.Images[0].Name != "unused" {
		t.Fatalf("unexpected prune result: %#v", result)
	}
	if _, err := store.InspectImage("used"); err != nil {
		t.Fatalf("expected used image to remain: %v", err)
	}
	if _, err := store.InspectImage("unused"); err == nil {
		t.Fatal("expected unused image to be removed")
	}
}
