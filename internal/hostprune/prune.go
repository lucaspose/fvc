// Package hostprune removes host-side artifacts that are no longer referenced
// by daemon state.
package hostprune

import (
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/lucaspose/fvc/internal"
)

type Result struct {
	RemovedFiles int32
	FreedBytes   int64
	Items        []Item
}

type Item struct {
	Kind      string
	Path      string
	SizeBytes int64
}

func OrphanActiveDrives(db *sql.DB, activeDir string, dryRun bool) (Result, error) {
	known := make(map[string]bool)
	rows, err := db.Query(`SELECT COALESCE(drive_path, '') FROM vms`)
	if err != nil {
		return Result{}, fmt.Errorf("drive state query failed: %v", err)
	}
	defer rows.Close()
	for rows.Next() {
		var path string
		if err := rows.Scan(&path); err != nil {
			return Result{}, fmt.Errorf("drive state scan failed: %v", err)
		}
		if path != "" {
			known[path] = true
		}
	}
	if err := rows.Err(); err != nil {
		return Result{}, fmt.Errorf("drive state rows failed: %v", err)
	}

	entries, err := os.ReadDir(activeDir)
	if err != nil {
		if os.IsNotExist(err) {
			return Result{}, nil
		}
		return Result{}, fmt.Errorf("active directory read failed: %v", err)
	}

	var result Result
	for _, entry := range entries {
		if entry.IsDir() || filepath.Ext(entry.Name()) != ".ext4" {
			continue
		}
		path := filepath.Join(activeDir, entry.Name())
		if known[path] {
			continue
		}
		info, err := entry.Info()
		if err != nil {
			return result, fmt.Errorf("active drive stat failed: %v", err)
		}
		if !dryRun {
			if err := os.Remove(path); err != nil {
				return result, fmt.Errorf("active drive remove failed: %v", err)
			}
		}
		result.RemovedFiles++
		result.FreedBytes += info.Size()
		result.Items = append(result.Items, Item{Kind: "active-drive", Path: path, SizeBytes: info.Size()})
	}
	return result, nil
}

func RuntimeFiles(db *sql.DB, runtimeDir string, dryRun bool) (Result, error) {
	if runtimeDir == "" {
		runtimeDir = os.TempDir()
	}

	keep := make(map[string]bool)
	rows, err := db.Query(`SELECT id, COALESCE(console_path, '') FROM vms WHERE status = ?`, internal.VmRunning)
	if err != nil {
		return Result{}, fmt.Errorf("runtime state query failed: %v", err)
	}
	defer rows.Close()
	for rows.Next() {
		var vmID, consolePath string
		if err := rows.Scan(&vmID, &consolePath); err != nil {
			return Result{}, fmt.Errorf("runtime state scan failed: %v", err)
		}
		keep[filepath.Join(runtimeDir, fmt.Sprintf("fvc-%s.socket", vmID))] = true
		if consolePath != "" {
			keep[consolePath] = true
		}
	}
	if err := rows.Err(); err != nil {
		return Result{}, fmt.Errorf("runtime state rows failed: %v", err)
	}

	entries, err := os.ReadDir(runtimeDir)
	if err != nil {
		if os.IsNotExist(err) {
			return Result{}, nil
		}
		return Result{}, fmt.Errorf("runtime directory read failed: %v", err)
	}

	var result Result
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasPrefix(entry.Name(), "fvc-") {
			continue
		}
		path := filepath.Join(runtimeDir, entry.Name())
		if keep[path] {
			continue
		}
		info, err := entry.Info()
		if err != nil {
			return result, fmt.Errorf("runtime file stat failed: %v", err)
		}
		if !dryRun {
			if err := os.Remove(path); err != nil {
				return result, fmt.Errorf("runtime file remove failed: %v", err)
			}
		}
		result.RemovedFiles++
		result.FreedBytes += info.Size()
		result.Items = append(result.Items, Item{Kind: "runtime", Path: path, SizeBytes: info.Size()})
	}
	return result, nil
}
