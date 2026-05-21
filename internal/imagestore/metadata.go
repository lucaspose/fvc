package imagestore

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/lucaspose/fvc/internal"
	"github.com/lucaspose/fvc/internal/buildplan"
	"github.com/lucaspose/fvc/internal/storeio"
)

// ImageMetadata is the JSON sidecar stored next to each cached rootfs image.
type ImageMetadata struct {
	Name         string            `json:"name"`
	Source       string            `json:"source"`
	CreatedAt    time.Time         `json:"created_at"`
	Labels       map[string]string `json:"labels,omitempty"`
	Env          []string          `json:"env,omitempty"`
	Cmd          []string          `json:"cmd,omitempty"`
	Workdir      string            `json:"workdir,omitempty"`
	ExposedPorts []int32           `json:"exposed_ports,omitempty"`
	History      []ImageHistory    `json:"history,omitempty"`
}

// ImageHistory describes one operation that produced or mutated an image.
type ImageHistory struct {
	Action    string    `json:"action"`
	Message   string    `json:"message"`
	CreatedAt time.Time `json:"created_at"`
}

// ImageInfo combines filesystem details and metadata for one image.
type ImageInfo struct {
	Name      string
	Path      string
	SizeBytes int64
	Digest    string
	Metadata  ImageMetadata
}

// ImagePruneResult summarizes images removed because no VM references them.
type ImagePruneResult struct {
	RemovedImages int32
	FreedBytes    int64
	Images        []ImageInfo
}

func (s *Store) InspectImage(name string) (ImageInfo, error) {
	path, err := s.imagePath(name)
	if err != nil {
		return ImageInfo{}, err
	}
	return s.inspectImageAt(name, path, true)
}

func (s *Store) InspectCachedImage(name, path string, includeDigest bool) (ImageInfo, error) {
	return s.inspectImageAt(name, path, includeDigest)
}

func (s *Store) TagImage(source, target string) (ImageInfo, error) {
	if err := internal.ValidateImageRef(target); err != nil {
		return ImageInfo{}, err
	}
	sourcePath, err := s.imagePath(source)
	if err != nil {
		return ImageInfo{}, err
	}
	targetPath := s.CachedImagePath(target)
	if _, err := os.Stat(targetPath); err == nil {
		return ImageInfo{}, fmt.Errorf("image already exists: %s", target)
	} else if !os.IsNotExist(err) {
		return ImageInfo{}, fmt.Errorf("target image stat failed: %w", err)
	}
	if _, err := storeio.CopyFileAtomic(sourcePath, targetPath); err != nil {
		return ImageInfo{}, fmt.Errorf("image tag copy failed: %w", err)
	}

	sourceInfo, _ := s.InspectImage(source)
	metadata := ImageMetadata{
		Name:         target,
		Source:       "tag:" + source,
		CreatedAt:    time.Now(),
		Labels:       cloneLabels(sourceInfo.Metadata.Labels),
		Env:          append([]string(nil), sourceInfo.Metadata.Env...),
		Cmd:          append([]string(nil), sourceInfo.Metadata.Cmd...),
		Workdir:      sourceInfo.Metadata.Workdir,
		ExposedPorts: append([]int32(nil), sourceInfo.Metadata.ExposedPorts...),
		History: append(sourceInfo.Metadata.History, ImageHistory{
			Action:    "tag",
			Message:   fmt.Sprintf("tagged from %s", source),
			CreatedAt: time.Now(),
		}),
	}
	if err := s.writeImageMetadata(target, metadata); err != nil {
		return ImageInfo{}, err
	}
	return s.inspectImageAt(target, targetPath, true)
}

func (s *Store) ImportImage(sourcePath, name string) (ImageInfo, error) {
	return s.ImportImageWithMetadata(sourcePath, name, ImageMetadata{})
}

func (s *Store) ImportImageWithMetadata(sourcePath, name string, metadata ImageMetadata) (ImageInfo, error) {
	if err := internal.ValidateImageRef(name); err != nil {
		return ImageInfo{}, err
	}
	if strings.TrimSpace(sourcePath) == "" {
		return ImageInfo{}, fmt.Errorf("source path is required")
	}
	info, err := os.Stat(sourcePath)
	if err != nil {
		return ImageInfo{}, fmt.Errorf("source image stat failed: %w", err)
	}
	if info.IsDir() || !info.Mode().IsRegular() {
		return ImageInfo{}, fmt.Errorf("source image must be a regular file")
	}
	if filepath.Ext(sourcePath) != ".ext4" {
		return ImageInfo{}, fmt.Errorf("source image must use .ext4 extension")
	}
	targetPath := s.CachedImagePath(name)
	if _, err := os.Stat(targetPath); err == nil {
		return ImageInfo{}, fmt.Errorf("image already exists: %s", name)
	} else if !os.IsNotExist(err) {
		return ImageInfo{}, fmt.Errorf("target image stat failed: %w", err)
	}
	if _, err := storeio.CopyFileAtomic(sourcePath, targetPath); err != nil {
		return ImageInfo{}, fmt.Errorf("image import failed: %w", err)
	}
	now := time.Now()
	if metadata.Name == "" {
		metadata.Name = name
	}
	if metadata.Source == "" {
		metadata.Source = "import:" + sourcePath
	}
	if metadata.CreatedAt.IsZero() {
		metadata.CreatedAt = now
	}
	if len(metadata.History) == 0 {
		metadata.History = []ImageHistory{{
			Action:    "import",
			Message:   fmt.Sprintf("imported from %s", sourcePath),
			CreatedAt: now,
		}}
	}
	if err := s.writeImageMetadata(name, metadata); err != nil {
		return ImageInfo{}, err
	}
	return s.inspectImageAt(name, targetPath, true)
}

func (s *Store) ExportImage(name, destPath string) (string, error) {
	sourcePath, err := s.imagePath(name)
	if err != nil {
		return "", err
	}
	if strings.TrimSpace(destPath) == "" {
		return "", fmt.Errorf("destination path is required")
	}
	if info, err := os.Stat(destPath); err == nil && info.IsDir() {
		return "", fmt.Errorf("destination path is a directory")
	}
	if filepath.Ext(destPath) != ".ext4" {
		return "", fmt.Errorf("destination path must use .ext4 extension")
	}
	if _, err := storeio.CopyFileAtomic(sourcePath, destPath); err != nil {
		return "", fmt.Errorf("image export failed: %w", err)
	}
	return destPath, nil
}

func (s *Store) RemoveImage(name string) error {
	path, err := s.imagePath(name)
	if err != nil {
		return err
	}
	if err := os.Remove(path); err != nil {
		return fmt.Errorf("image remove failed: %w", err)
	}
	_ = os.Remove(s.imageMetadataPath(name))
	return nil
}

func (s *Store) ImageHistory(name string) ([]ImageHistory, error) {
	info, err := s.InspectImage(name)
	if err != nil {
		return nil, err
	}
	if len(info.Metadata.History) == 0 {
		return []ImageHistory{{
			Action:    "legacy",
			Message:   "image has no metadata history",
			CreatedAt: info.Metadata.CreatedAt,
		}}, nil
	}
	return info.Metadata.History, nil
}

func (s *Store) PruneUnusedImages(used map[string]bool, dryRun bool) (ImagePruneResult, error) {
	images, err := s.ListImages()
	if err != nil {
		return ImagePruneResult{}, err
	}
	var result ImagePruneResult
	for _, image := range images {
		if used[image.Name] {
			continue
		}
		info, err := s.inspectImageAt(image.Name, image.Path, false)
		if err != nil {
			return result, err
		}
		if !dryRun {
			if err := os.Remove(image.Path); err != nil {
				return result, fmt.Errorf("image prune failed for %s: %w", image.Name, err)
			}
			_ = os.Remove(s.imageMetadataPath(image.Name))
		}
		result.RemovedImages++
		result.FreedBytes += image.SizeBytes
		result.Images = append(result.Images, info)
	}
	return result, nil
}

func (s *Store) WriteBuildMetadata(plan buildplan.Plan) error {
	now := time.Now()
	history := []ImageHistory{{
		Action:    "build",
		Message:   fmt.Sprintf("built from %s using base %s", plan.ContextPath, plan.BaseImage),
		CreatedAt: now,
	}}
	for _, copySpec := range plan.Copies {
		history = append(history, ImageHistory{
			Action:    "copy",
			Message:   fmt.Sprintf("copied %s to %s", copySpec.SourceRel, copySpec.DestPath),
			CreatedAt: now,
		})
	}
	for _, run := range plan.Runs {
		history = append(history, ImageHistory{
			Action:    "run",
			Message:   run.Command,
			CreatedAt: now,
		})
	}
	metadata := ImageMetadata{
		Name:         plan.Tag,
		Source:       "build:" + plan.ContextPath,
		CreatedAt:    now,
		Labels:       cloneLabels(plan.Labels),
		Env:          append([]string(nil), plan.Runtime.Env...),
		Cmd:          append([]string(nil), plan.Runtime.Cmd...),
		Workdir:      plan.Runtime.Workdir,
		ExposedPorts: append([]int32(nil), plan.Runtime.ExposedPorts...),
		History:      history,
	}
	return s.writeImageMetadata(plan.Tag, metadata)
}

func (s *Store) imagePath(name string) (string, error) {
	if err := internal.ValidateImageRef(name); err != nil {
		return "", err
	}
	encoded := s.CachedImagePath(name)
	if _, err := os.Stat(encoded); err == nil {
		return encoded, nil
	} else if !os.IsNotExist(err) {
		return "", fmt.Errorf("image stat failed: %w", err)
	}
	legacy := filepath.Join(s.cacheDir, name+".ext4")
	if _, err := os.Stat(legacy); err == nil {
		return legacy, nil
	}
	return "", fmt.Errorf("image not found: %s", name)
}

func (s *Store) inspectImageAt(name, imagePath string, includeDigest bool) (ImageInfo, error) {
	info, err := os.Stat(imagePath)
	if err != nil {
		return ImageInfo{}, fmt.Errorf("image stat failed: %w", err)
	}
	metadata, err := s.readImageMetadata(name)
	if err != nil {
		return ImageInfo{}, err
	}
	if metadata.CreatedAt.IsZero() {
		metadata.CreatedAt = info.ModTime()
	}
	if metadata.Name == "" {
		metadata.Name = name
	}
	digest := ""
	if includeDigest {
		digest, err = fileDigest(imagePath)
		if err != nil {
			return ImageInfo{}, err
		}
	}
	return ImageInfo{
		Name:      name,
		Path:      imagePath,
		SizeBytes: info.Size(),
		Digest:    digest,
		Metadata:  metadata,
	}, nil
}

func (s *Store) readImageMetadata(name string) (ImageMetadata, error) {
	path := s.imageMetadataPath(name)
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return ImageMetadata{Name: name, Source: "unknown"}, nil
		}
		return ImageMetadata{}, fmt.Errorf("image metadata read failed: %w", err)
	}
	var metadata ImageMetadata
	if err := json.Unmarshal(data, &metadata); err != nil {
		return ImageMetadata{}, fmt.Errorf("image metadata parse failed: %w", err)
	}
	return metadata, nil
}

func (s *Store) writeImageMetadata(name string, metadata ImageMetadata) error {
	if metadata.Name == "" {
		metadata.Name = name
	}
	if metadata.CreatedAt.IsZero() {
		metadata.CreatedAt = time.Now()
	}
	data, err := json.MarshalIndent(metadata, "", "  ")
	if err != nil {
		return fmt.Errorf("image metadata encode failed: %w", err)
	}
	path := s.imageMetadataPath(name)
	tmp, err := os.CreateTemp(filepath.Dir(path), "."+filepath.Base(path)+".tmp-*")
	if err != nil {
		return fmt.Errorf("image metadata temp create failed: %w", err)
	}
	tmpPath := tmp.Name()
	defer func() {
		_ = os.Remove(tmpPath)
	}()
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("image metadata write failed: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("image metadata close failed: %w", err)
	}
	if err := os.Rename(tmpPath, path); err != nil {
		return fmt.Errorf("image metadata publish failed: %w", err)
	}
	return nil
}

func (s *Store) imageMetadataPath(name string) string {
	return s.CachedImagePath(name) + ".json"
}

func fileDigest(path string) (string, error) {
	file, err := os.Open(path)
	if err != nil {
		return "", fmt.Errorf("image digest open failed: %w", err)
	}
	defer file.Close()
	hash := sha256.New()
	if _, err := io.Copy(hash, file); err != nil {
		return "", fmt.Errorf("image digest read failed: %w", err)
	}
	return "sha256:" + hex.EncodeToString(hash.Sum(nil)), nil
}

func cloneLabels(labels map[string]string) map[string]string {
	if len(labels) == 0 {
		return nil
	}
	out := make(map[string]string, len(labels))
	for key, value := range labels {
		out[key] = value
	}
	return out
}
