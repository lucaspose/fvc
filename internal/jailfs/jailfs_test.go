package jailfs

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
)

func TestEnsureChrootBaseDir(t *testing.T) {
	path := filepath.Join(t.TempDir(), "jailer")
	if err := EnsureChrootBaseDir(path); err != nil {
		t.Fatalf("EnsureChrootBaseDir failed: %v", err)
	}
	if err := EnsureChrootBaseDir(path); err != nil {
		t.Fatalf("second EnsureChrootBaseDir failed: %v", err)
	}
}

func TestEnsureChrootBaseDirRejectsWorldWritableDir(t *testing.T) {
	path := filepath.Join(t.TempDir(), "jailer")
	if err := os.Mkdir(path, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(path, 0777); err != nil {
		t.Fatal(err)
	}
	if err := EnsureChrootBaseDir(path); err == nil {
		t.Fatal("expected a world-writable base directory to be rejected")
	}
}

func TestEnsureChrootBaseDirRejectsSymlink(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "jailer")
	if err := os.Symlink(t.TempDir(), path); err != nil {
		t.Fatal(err)
	}
	if err := EnsureChrootBaseDir(path); err == nil {
		t.Fatal("expected a symlinked base directory to be rejected")
	}
}

func TestPrepareFileAccessKeepsReadOnlyFilesOwnedByDaemon(t *testing.T) {
	path := filepath.Join(t.TempDir(), "kernel.bin")
	if err := os.WriteFile(path, []byte("kernel"), 0600); err != nil {
		t.Fatal(err)
	}
	// The jailer runs as a different user that only shares the group.
	const jailerUID = 65534
	if err := PrepareFileAccess(path, jailerUID, os.Getgid(), 0440); err != nil {
		t.Fatalf("PrepareFileAccess failed: %v", err)
	}
	info := mustStat(t, path)
	if owner := info.Sys().(*syscall.Stat_t).Uid; int(owner) != os.Geteuid() {
		t.Fatalf("read-only file owner = %d, want daemon uid %d", owner, os.Geteuid())
	}
	if perm := info.Mode().Perm(); perm&0222 != 0 {
		t.Fatalf("read-only file should not be writable, got %o", perm)
	}
	if !AccessibleBy(info, jailerUID, os.Getgid(), 0440) {
		t.Fatal("expected read access for the jailer group")
	}
}

func TestPrepareFileAccessGrantsWriteForDrives(t *testing.T) {
	path := filepath.Join(t.TempDir(), "rootfs.ext4")
	if err := os.WriteFile(path, []byte("drive"), 0400); err != nil {
		t.Fatal(err)
	}
	if err := PrepareFileAccess(path, os.Getuid(), os.Getgid(), 0660); err != nil {
		t.Fatalf("PrepareFileAccess failed: %v", err)
	}
	if !AccessibleBy(mustStat(t, path), os.Getuid(), os.Getgid(), 0660) {
		t.Fatal("expected read/write access to the drive")
	}
}

func TestPrepareFileAccessRefusesSymlinks(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "secret")
	if err := os.WriteFile(target, []byte("secret"), 0600); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(dir, "kernel.bin")
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}
	if err := PrepareFileAccess(link, os.Getuid(), os.Getgid(), 0440); err == nil {
		t.Fatal("expected a symlink to be refused")
	}
	if perm := mustStat(t, target).Mode().Perm(); perm != 0600 {
		t.Fatalf("symlink target permissions changed to %o", perm)
	}
}

func TestFindFirecrackerPID(t *testing.T) {
	procRoot := t.TempDir()
	jailRoot := "/srv/jailer/firecracker/fvc-vm-1/root"
	fakeProc(t, procRoot, 1234, "fvc-vm-1", 65534, jailRoot)
	if err := os.Mkdir(filepath.Join(procRoot, "self"), 0755); err != nil {
		t.Fatal(err)
	}

	pid, err := FindFirecrackerPID(procRoot, "fvc-vm-1", 65534, jailRoot)
	if err != nil {
		t.Fatalf("FindFirecrackerPID failed: %v", err)
	}
	if pid != 1234 {
		t.Fatalf("pid = %d, want 1234", pid)
	}
}

func TestFindFirecrackerPIDIgnoresImpostors(t *testing.T) {
	jailRoot := "/srv/jailer/firecracker/fvc-vm-1/root"
	tests := []struct {
		name string
		uid  int
		root string
	}{
		{"wrong uid", 1000, jailRoot},
		{"not chrooted", 65534, "/"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			procRoot := t.TempDir()
			fakeProc(t, procRoot, 4321, "fvc-vm-1", tt.uid, tt.root)
			if pid, err := FindFirecrackerPID(procRoot, "fvc-vm-1", 65534, jailRoot); err == nil {
				t.Fatalf("impostor process %d was accepted", pid)
			}
		})
	}
}

func fakeProc(t *testing.T, procRoot string, pid int, jailerID string, uid int, root string) {
	t.Helper()
	dir := filepath.Join(procRoot, fmt.Sprint(pid))
	if err := os.Mkdir(dir, 0755); err != nil {
		t.Fatal(err)
	}
	cmdline := strings.Join([]string{"/firecracker", "--id", jailerID, "--api-sock", "/run/firecracker.socket"}, "\x00") + "\x00"
	if err := os.WriteFile(filepath.Join(dir, "cmdline"), []byte(cmdline), 0644); err != nil {
		t.Fatal(err)
	}
	status := fmt.Sprintf("Name:\tfirecracker\nUid:\t%d\t%d\t%d\t%d\n", uid, uid, uid, uid)
	if err := os.WriteFile(filepath.Join(dir, "status"), []byte(status), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(root, filepath.Join(dir, "root")); err != nil {
		t.Fatal(err)
	}
}

func mustStat(t *testing.T, path string) os.FileInfo {
	t.Helper()
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	return info
}
