package main

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/lucaspose/fvc/internal"
)

type Snapshot struct {
	Name      string
	Path      string
	SizeBytes int64
}

func (s *ImageStore) CreateSnapshot(vmID, drivePath, name string) (string, int64, error) {
	if err := validateSnapshotInput(vmID, name); err != nil {
		return "", 0, err
	}
	destPath := s.snapshotPath(vmID, name)
	if _, err := os.Stat(destPath); err == nil {
		return "", 0, fmt.Errorf("snapshot already exists: %s", name)
	} else if !os.IsNotExist(err) {
		return "", 0, fmt.Errorf("snapshot stat failed: %w", err)
	}
	size, err := copyFileAtomic(drivePath, destPath)
	if err != nil {
		return "", 0, fmt.Errorf("snapshot create failed: %w", err)
	}
	return destPath, size, nil
}

func (s *ImageStore) RestoreSnapshot(vmID, drivePath, name string) (string, int64, error) {
	if err := validateSnapshotInput(vmID, name); err != nil {
		return "", 0, err
	}
	sourcePath := s.snapshotPath(vmID, name)
	size, err := copyFileAtomic(sourcePath, drivePath)
	if err != nil {
		return "", 0, fmt.Errorf("snapshot restore failed: %w", err)
	}
	return sourcePath, size, nil
}

func (s *ImageStore) RemoveSnapshot(vmID, name string) (string, error) {
	if err := validateSnapshotInput(vmID, name); err != nil {
		return "", err
	}
	path := s.snapshotPath(vmID, name)
	if err := os.Remove(path); err != nil {
		if os.IsNotExist(err) {
			return "", fmt.Errorf("snapshot not found: %s", name)
		}
		return "", fmt.Errorf("snapshot remove failed: %w", err)
	}
	return path, nil
}

func (s *ImageStore) ListSnapshots(vmID string) ([]Snapshot, error) {
	if err := validateSnapshotVMID(vmID); err != nil {
		return nil, err
	}
	dir := s.snapshotVMDir(vmID)
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("snapshot directory read failed: %w", err)
	}

	snapshots := make([]Snapshot, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() || filepath.Ext(entry.Name()) != ".ext4" {
			continue
		}
		name := strings.TrimSuffix(entry.Name(), ".ext4")
		if err := internal.ValidateVMName(name); err != nil || name == "" {
			continue
		}
		info, err := entry.Info()
		if err != nil {
			return nil, fmt.Errorf("snapshot stat failed: %w", err)
		}
		snapshots = append(snapshots, Snapshot{
			Name:      name,
			Path:      filepath.Join(dir, entry.Name()),
			SizeBytes: info.Size(),
		})
	}
	sort.Slice(snapshots, func(i, j int) bool {
		return snapshots[i].Name < snapshots[j].Name
	})
	return snapshots, nil
}

func (s *ImageStore) snapshotVMDir(vmID string) string {
	return filepath.Join(s.snapshotDir, vmID)
}

func (s *ImageStore) snapshotPath(vmID, name string) string {
	return filepath.Join(s.snapshotVMDir(vmID), name+".ext4")
}

func validateSnapshotInput(vmID, name string) error {
	if err := validateSnapshotVMID(vmID); err != nil {
		return err
	}
	if strings.TrimSpace(name) == "" {
		return fmt.Errorf("snapshot name is required")
	}
	if err := internal.ValidateVMName(name); err != nil {
		return fmt.Errorf("invalid snapshot name: %w", err)
	}
	return nil
}

func validateSnapshotVMID(vmID string) error {
	if strings.TrimSpace(vmID) == "" {
		return fmt.Errorf("vm id is required")
	}
	if strings.ContainsAny(vmID, `/\`) {
		return fmt.Errorf("invalid vm id %q", vmID)
	}
	return nil
}
