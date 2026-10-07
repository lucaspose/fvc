// Package jailfs prepares host files and directories used by the Firecracker
// jailer, and identifies jailed Firecracker processes.
package jailfs

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
)

// EnsureChrootBaseDir creates the jailer chroot base directory and refuses to
// use it if it is a symlink, not owned by the daemon user, or writable by
// group or others: any of those would let another user tamper with jails.
func EnsureChrootBaseDir(path string) error {
	if err := os.MkdirAll(path, 0755); err != nil {
		return fmt.Errorf("jailer chroot base directory setup failed: %w", err)
	}
	info, err := os.Lstat(path)
	if err != nil {
		return fmt.Errorf("jailer chroot base directory stat failed: %w", err)
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
		return fmt.Errorf("jailer chroot base directory must be a real directory: %s", path)
	}
	if info.Mode().Perm()&0022 != 0 {
		return fmt.Errorf("jailer chroot base directory must not be group or world writable: %s", path)
	}
	if stat, ok := info.Sys().(*syscall.Stat_t); ok && int(stat.Uid) != os.Geteuid() {
		return fmt.Errorf("jailer chroot base directory must be owned by uid %d: %s", os.Geteuid(), path)
	}
	return nil
}

// PrepareFileAccess makes a regular file usable by the jailed Firecracker
// process, which runs as uid/gid.
//
// Read-only files (no write bit in mode), such as the shared kernel, stay owned
// by the daemon user and are only shared through the group: the jailed process
// can read them but can neither modify them nor change their permissions.
// Writable files, such as a VM drive, are handed over to uid/gid.
//
// The file is opened with O_NOFOLLOW and changed through its descriptor, so a
// symlink swapped in at path cannot redirect the change.
func PrepareFileAccess(path string, uid, gid int, mode os.FileMode) error {
	f, err := os.OpenFile(path, os.O_RDONLY|syscall.O_NOFOLLOW, 0)
	if err != nil {
		return fmt.Errorf("source open failed: %w", err)
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return fmt.Errorf("source stat failed: %w", err)
	}
	if !info.Mode().IsRegular() {
		return fmt.Errorf("source must be a regular file: %s", path)
	}

	readOnly := mode&0222 == 0
	owner, perm := uid, mode
	if readOnly {
		owner = os.Geteuid()
		perm = mode & 0440
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	ownedByJailer := ok && int(stat.Uid) == uid && uid != os.Geteuid()
	if AccessibleBy(info, uid, gid, mode) && !(readOnly && ownedByJailer) {
		return nil
	}
	if err := f.Chown(owner, gid); err != nil {
		return fmt.Errorf("source chown failed: %w", err)
	}
	if err := f.Chmod(perm); err != nil {
		return fmt.Errorf("source chmod failed: %w", err)
	}
	return nil
}

// AccessibleBy reports whether uid/gid already have the access requested by mode.
func AccessibleBy(info os.FileInfo, uid, gid int, mode os.FileMode) bool {
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return false
	}
	perm := info.Mode().Perm()
	readNeeded := mode&0444 != 0
	writeNeeded := mode&0222 != 0
	canRead := stat.Uid == uint32(uid) && perm&0400 != 0 ||
		stat.Gid == uint32(gid) && perm&0040 != 0 ||
		perm&0004 != 0
	canWrite := stat.Uid == uint32(uid) && perm&0200 != 0 ||
		stat.Gid == uint32(gid) && perm&0020 != 0 ||
		perm&0002 != 0
	return (!readNeeded || canRead) && (!writeNeeded || canWrite)
}

// FindFirecrackerPID returns the PID of the Firecracker process started by the
// jailer with the given id. Matching the command line alone is not enough, as
// any local user can start a process that looks like it, so the process must
// also run as uid and be chrooted into root.
func FindFirecrackerPID(procRoot, jailerID string, uid int, root string) (int, error) {
	entries, err := os.ReadDir(procRoot)
	if err != nil {
		return 0, fmt.Errorf("proc scan failed: %w", err)
	}
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		pid, err := strconv.Atoi(entry.Name())
		if err != nil {
			continue
		}
		dir := filepath.Join(procRoot, entry.Name())
		if !hasJailerID(dir, jailerID) {
			continue
		}
		if processUID(dir) != uid {
			continue
		}
		if procRootPath, err := os.Readlink(filepath.Join(dir, "root")); err != nil || filepath.Clean(procRootPath) != filepath.Clean(root) {
			continue
		}
		return pid, nil
	}
	return 0, fmt.Errorf("jailed firecracker process not found for id %s", jailerID)
}

func hasJailerID(procDir, jailerID string) bool {
	data, err := os.ReadFile(filepath.Join(procDir, "cmdline"))
	if err != nil || len(data) == 0 {
		return false
	}
	args := strings.Split(strings.TrimRight(string(data), "\x00"), "\x00")
	if len(args) == 0 || filepath.Base(args[0]) != "firecracker" {
		return false
	}
	for i := 0; i+1 < len(args); i++ {
		if args[i] == "--id" && args[i+1] == jailerID {
			return true
		}
	}
	return false
}

// processUID returns the real uid from /proc/<pid>/status, or -1.
func processUID(procDir string) int {
	data, err := os.ReadFile(filepath.Join(procDir, "status"))
	if err != nil {
		return -1
	}
	for _, line := range strings.Split(string(data), "\n") {
		fields := strings.Fields(line)
		if len(fields) >= 2 && fields[0] == "Uid:" {
			uid, err := strconv.Atoi(fields[1])
			if err != nil {
				return -1
			}
			return uid
		}
	}
	return -1
}
