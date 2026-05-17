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

func TestDownloadAtomicDoesNotLeavePartialDestination(t *testing.T) {
	dest := filepath.Join(t.TempDir(), "cache", "image.ext4")
	if err := downloadAtomic("://bad-url", dest); err == nil {
		t.Fatal("expected download to fail")
	}
	if _, err := os.Stat(dest); !os.IsNotExist(err) {
		t.Fatalf("expected no destination file after failed download, got err=%v", err)
	}
}
