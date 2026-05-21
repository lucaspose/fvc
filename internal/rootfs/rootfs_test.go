package rootfs

import (
	"os"
	"path/filepath"
	"testing"
)

type testIgnore map[string]bool

func (i testIgnore) Matches(rel string) bool {
	return i[rel]
}

func TestCopyRegularFileRejectsSymlinkTarget(t *testing.T) {
	dir := t.TempDir()
	source := filepath.Join(dir, "source.txt")
	if err := os.WriteFile(source, []byte("safe"), 0644); err != nil {
		t.Fatal(err)
	}
	outside := filepath.Join(dir, "outside.txt")
	if err := os.WriteFile(outside, []byte("outside"), 0644); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(dir, "rootfs", "etc", "target.txt")
	if err := os.MkdirAll(filepath.Dir(target), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, target); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}
	if err := CopyRegularFile(source, target, 0644); err == nil {
		t.Fatal("expected symlink target to be rejected")
	}
	data, err := os.ReadFile(outside)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "outside" {
		t.Fatalf("outside file was modified: %q", string(data))
	}
}

func TestCopyIntoCopiesDirectoryAndHonorsIgnore(t *testing.T) {
	dir := t.TempDir()
	contextDir := filepath.Join(dir, "ctx")
	sourceDir := filepath.Join(contextDir, "app")
	mountDir := filepath.Join(dir, "rootfs")
	if err := os.MkdirAll(sourceDir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(sourceDir, "keep.txt"), []byte("keep"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(sourceDir, "skip.txt"), []byte("skip"), 0644); err != nil {
		t.Fatal(err)
	}

	if err := CopyInto(sourceDir, mountDir, "/opt/app", contextDir, testIgnore{"app/skip.txt": true}); err != nil {
		t.Fatalf("CopyInto failed: %v", err)
	}
	if _, err := os.Stat(filepath.Join(mountDir, "opt", "app", "keep.txt")); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(mountDir, "opt", "app", "skip.txt")); !os.IsNotExist(err) {
		t.Fatalf("expected skipped file to be absent, stat err=%v", err)
	}
}

func TestPathRejectsEscapes(t *testing.T) {
	if _, err := Path("/tmp/rootfs", "/../escape"); err == nil {
		t.Fatal("expected path escape to be rejected")
	}
}
