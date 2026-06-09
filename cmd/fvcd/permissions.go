package main

import (
	"fmt"
	"os"
	"os/user"
	"strconv"
)

var lookupGroupID = func(name string) (int, error) {
	group, err := user.LookupGroup(name)
	if err != nil {
		return 0, err
	}
	gid, err := strconv.Atoi(group.Gid)
	if err != nil {
		return 0, err
	}
	return gid, nil
}

func runtimeGID(groupName string) (int, bool, error) {
	if groupName == "" {
		return -1, false, nil
	}
	gid, err := lookupGroupID(groupName)
	if err != nil {
		return -1, false, fmt.Errorf("runtime group lookup failed for %q: %w", groupName, err)
	}
	return gid, true, nil
}

func applyRuntimePermissions(path string, groupName string, mode os.FileMode) error {
	info, err := os.Lstat(path)
	if err != nil {
		return fmt.Errorf("runtime permission path stat failed for %s: %w", path, err)
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("runtime permission path refuses symlink: %s", path)
	}
	gid, ok, err := runtimeGID(groupName)
	if err != nil {
		return err
	}
	if ok {
		if err := os.Chown(path, -1, gid); err != nil {
			return fmt.Errorf("runtime group chown failed for %s: %w", path, err)
		}
	}
	if err := os.Chmod(path, mode); err != nil {
		return fmt.Errorf("runtime chmod failed for %s: %w", path, err)
	}
	return nil
}
