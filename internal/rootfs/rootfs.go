package rootfs

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"syscall"
)

type IgnoreMatcher interface {
	Matches(rel string) bool
}

// CopyInto copies sourcePath into a mounted rootfs at guestDest while refusing
// symlink path components in the destination.
func CopyInto(sourcePath, mountDir, guestDest, contextPath string, ignore IgnoreMatcher) error {
	targetPath, err := Path(mountDir, guestDest)
	if err != nil {
		return err
	}
	info, err := os.Stat(sourcePath)
	if err != nil {
		return fmt.Errorf("copy source stat failed: %w", err)
	}
	if info.IsDir() {
		return copyDir(sourcePath, targetPath, contextPath, ignore)
	}
	return CopyRegularFile(sourcePath, targetPath, info.Mode())
}

func copyDir(sourceDir, targetDir, contextPath string, ignore IgnoreMatcher) error {
	if err := EnsureDirNoSymlink(targetDir, 0755); err != nil {
		return fmt.Errorf("copy directory create failed: %w", err)
	}
	entries, err := os.ReadDir(sourceDir)
	if err != nil {
		return fmt.Errorf("copy directory read failed: %w", err)
	}
	for _, entry := range entries {
		sourcePath := filepath.Join(sourceDir, entry.Name())
		rel, err := filepath.Rel(contextPath, sourcePath)
		if err != nil {
			return fmt.Errorf("copy entry path resolve failed: %w", err)
		}
		if ignore != nil && ignore.Matches(filepath.ToSlash(rel)) {
			continue
		}
		targetPath := filepath.Join(targetDir, entry.Name())
		info, err := entry.Info()
		if err != nil {
			return fmt.Errorf("copy entry stat failed: %w", err)
		}
		if info.IsDir() {
			if err := copyDir(sourcePath, targetPath, contextPath, ignore); err != nil {
				return err
			}
			continue
		}
		if !info.Mode().IsRegular() {
			continue
		}
		if err := CopyRegularFile(sourcePath, targetPath, info.Mode()); err != nil {
			return err
		}
	}
	return nil
}

// CopyRegularFile copies a regular file to targetPath without following an
// existing symlink destination.
func CopyRegularFile(sourcePath, targetPath string, mode os.FileMode) error {
	if err := EnsureDirNoSymlink(filepath.Dir(targetPath), 0755); err != nil {
		return fmt.Errorf("copy parent create failed: %w", err)
	}
	src, err := os.Open(sourcePath)
	if err != nil {
		return fmt.Errorf("copy source open failed: %w", err)
	}
	defer src.Close()
	dst, err := os.OpenFile(targetPath, os.O_CREATE|os.O_TRUNC|os.O_WRONLY|syscall.O_NOFOLLOW, mode.Perm())
	if err != nil {
		return fmt.Errorf("copy destination open failed: %w", err)
	}
	if _, err := io.Copy(dst, src); err != nil {
		_ = dst.Close()
		return fmt.Errorf("copy file failed: %w", err)
	}
	if err := dst.Close(); err != nil {
		return fmt.Errorf("copy destination close failed: %w", err)
	}
	if err := os.Chmod(targetPath, mode.Perm()); err != nil {
		return fmt.Errorf("copy destination chmod failed: %w", err)
	}
	return nil
}

// EnsureDirNoSymlink creates dir and refuses symlink components on the way.
func EnsureDirNoSymlink(dir string, mode os.FileMode) error {
	clean := filepath.Clean(dir)
	if clean == "." || clean == string(filepath.Separator) {
		return nil
	}
	var current string
	if filepath.IsAbs(clean) {
		current = string(filepath.Separator)
	}
	for _, segment := range strings.Split(strings.Trim(clean, string(filepath.Separator)), string(filepath.Separator)) {
		if segment == "" {
			continue
		}
		current = filepath.Join(current, segment)
		info, err := os.Lstat(current)
		if err == nil {
			if info.Mode()&os.ModeSymlink != 0 {
				return fmt.Errorf("refusing symlink path component: %s", current)
			}
			if !info.IsDir() {
				return fmt.Errorf("path component is not a directory: %s", current)
			}
			continue
		}
		if !os.IsNotExist(err) {
			return err
		}
		if err := os.Mkdir(current, mode); err != nil && !os.IsExist(err) {
			return err
		}
	}
	return nil
}

// Path resolves guestDest below mountDir and rejects escapes.
func Path(mountDir, guestDest string) (string, error) {
	targetPath := filepath.Join(mountDir, strings.TrimPrefix(guestDest, string(filepath.Separator)))
	if !pathWithin(mountDir, targetPath) {
		return "", fmt.Errorf("copy.dest escapes mounted rootfs: %s", guestDest)
	}
	return targetPath, nil
}

// WriteFileNoFollow writes a file without following an existing symlink target.
func WriteFileNoFollow(path string, data []byte, mode os.FileMode) error {
	file, err := os.OpenFile(path, os.O_CREATE|os.O_TRUNC|os.O_WRONLY|syscall.O_NOFOLLOW, mode)
	if err != nil {
		return err
	}
	if _, err := file.Write(data); err != nil {
		_ = file.Close()
		return err
	}
	return file.Close()
}

func pathWithin(parent, child string) bool {
	rel, err := filepath.Rel(parent, child)
	if err != nil {
		return false
	}
	return rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}
