package main

import (
	"encoding/base64"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/lucaspose/fvc/internal"
)

const maxDownloadBytes = 10 << 30 // 10 GiB

type ImageStore struct {
	baseDir      string
	cacheDir     string
	activeDir    string
	snapshotDir  string
	kernelPath   string
	imageBaseURL string
}

type CachedImage struct {
	Name      string
	Path      string
	SizeBytes int64
	Encoded   bool
}

type PruneResult struct {
	RemovedFiles int32
	FreedBytes   int64
	Items        []PruneItem
}

type PruneItem struct {
	Kind      string
	Path      string
	SizeBytes int64
}

func NewImageStore(cfg DaemonConfig) *ImageStore {
	snapshotDir := cfg.SnapshotDir
	if snapshotDir == "" {
		snapshotDir = filepath.Join(cfg.BaseDir, "snapshots")
	}
	return &ImageStore{
		baseDir:      cfg.BaseDir,
		cacheDir:     cfg.CacheDir,
		activeDir:    cfg.ActiveDir,
		snapshotDir:  snapshotDir,
		kernelPath:   cfg.KernelPath,
		imageBaseURL: cfg.ImageBaseURL,
	}
}

func (s *ImageStore) Init() error {
	for _, dir := range []string{s.baseDir, s.cacheDir, s.activeDir, s.snapshotDir} {
		if err := os.MkdirAll(dir, 0755); err != nil {
			return fmt.Errorf("storage directory setup failed for %s: %v", dir, err)
		}
	}
	return nil
}

func (s *ImageStore) PullImageIfNeeded(imageName string) (string, error) {
	if err := internal.ValidateImageRef(imageName); err != nil {
		return "", err
	}

	localPath := s.cachedImagePath(imageName)
	if _, err := os.Stat(localPath); err == nil {
		return localPath, nil
	}

	remoteURL, err := s.imageURL(imageName)
	if err != nil {
		return "", err
	}
	if err := downloadAtomic(remoteURL, localPath); err != nil {
		return "", fmt.Errorf("cannot download image %q from %s: %w", imageName, remoteURL, err)
	}
	return localPath, nil
}

func (s *ImageStore) CloneImage(imageName, vmID string) (string, error) {
	if err := internal.ValidateImageRef(imageName); err != nil {
		return "", err
	}
	if vmID == "" || strings.ContainsAny(vmID, `/\`) {
		return "", fmt.Errorf("invalid vm id %q", vmID)
	}

	sourcePath := s.cachedImagePath(imageName)
	destPath := filepath.Join(s.activeDir, vmID+".ext4")
	if _, err := copyFileAtomic(sourcePath, destPath); err != nil {
		return "", fmt.Errorf("ephemeral clone failed: %w", err)
	}

	return destPath, nil
}

func (s *ImageStore) PullKernelIfNeeded() (string, error) {
	localPath := s.kernelPath

	if _, err := os.Stat(localPath); err == nil {
		return localPath, nil
	}

	if err := os.MkdirAll(filepath.Dir(localPath), 0755); err != nil {
		return "", fmt.Errorf("kernel directory setup failed: %v", err)
	}

	remoteURL, err := s.kernelURL()
	if err != nil {
		return "", err
	}
	if err := downloadAtomic(remoteURL, localPath); err != nil {
		return "", fmt.Errorf("cannot download kernel from %s: %w", remoteURL, err)
	}
	return localPath, nil
}

func (s *ImageStore) cachedImagePath(imageName string) string {
	fileName := base64.RawURLEncoding.EncodeToString([]byte(imageName)) + ".ext4"
	return filepath.Join(s.cacheDir, fileName)
}

func (s *ImageStore) ListImages() ([]CachedImage, error) {
	entries, err := os.ReadDir(s.cacheDir)
	if err != nil {
		return nil, fmt.Errorf("cache directory read failed: %v", err)
	}
	byName := make(map[string]CachedImage)
	for _, entry := range entries {
		if entry.IsDir() || filepath.Ext(entry.Name()) != ".ext4" {
			continue
		}
		encoded := strings.TrimSuffix(entry.Name(), ".ext4")
		name := encoded
		isEncoded := false
		if decoded, err := base64.RawURLEncoding.DecodeString(encoded); err == nil && utf8.Valid(decoded) {
			name = string(decoded)
			isEncoded = true
		}
		if err := internal.ValidateImageRef(name); err != nil {
			continue
		}
		info, err := entry.Info()
		if err != nil {
			return nil, fmt.Errorf("cache image stat failed: %v", err)
		}
		image := CachedImage{
			Name:      name,
			Path:      filepath.Join(s.cacheDir, entry.Name()),
			SizeBytes: info.Size(),
			Encoded:   isEncoded,
		}
		current, exists := byName[name]
		if !exists || (!current.Encoded && image.Encoded) {
			byName[name] = image
		}
	}
	images := make([]CachedImage, 0, len(byName))
	for _, image := range byName {
		images = append(images, image)
	}
	sort.Slice(images, func(i, j int) bool {
		return images[i].Name < images[j].Name
	})
	return images, nil
}

func (s *ImageStore) PruneImageCache(dryRun bool) (PruneResult, error) {
	entries, err := os.ReadDir(s.cacheDir)
	if err != nil {
		return PruneResult{}, fmt.Errorf("cache directory read failed: %v", err)
	}

	encodedNames := make(map[string]bool)
	for _, entry := range entries {
		if entry.IsDir() || filepath.Ext(entry.Name()) != ".ext4" {
			continue
		}
		base := strings.TrimSuffix(entry.Name(), ".ext4")
		decoded, err := base64.RawURLEncoding.DecodeString(base)
		if err != nil || !utf8.Valid(decoded) {
			continue
		}
		name := string(decoded)
		if internal.ValidateImageRef(name) == nil {
			encodedNames[name] = true
		}
	}

	var result PruneResult
	for _, entry := range entries {
		if entry.IsDir() || filepath.Ext(entry.Name()) != ".ext4" {
			continue
		}
		base := strings.TrimSuffix(entry.Name(), ".ext4")
		if !encodedNames[base] {
			continue
		}
		path := filepath.Join(s.cacheDir, entry.Name())
		info, err := entry.Info()
		if err != nil {
			return result, fmt.Errorf("cache image stat failed: %v", err)
		}
		if !dryRun {
			if err := os.Remove(path); err != nil {
				return result, fmt.Errorf("cache image remove failed: %v", err)
			}
		}
		result.RemovedFiles++
		result.FreedBytes += info.Size()
		result.Items = append(result.Items, PruneItem{Kind: "image-cache", Path: path, SizeBytes: info.Size()})
	}
	return result, nil
}

func (s *ImageStore) imageURL(imageName string) (string, error) {
	return joinBaseURL(s.imageBaseURL, imageName+".ext4")
}

func (s *ImageStore) kernelURL() (string, error) {
	return joinBaseURL(s.imageBaseURL, "vmlinux.bin")
}

func joinBaseURL(base, elem string) (string, error) {
	parsed, err := url.Parse(base)
	if err != nil {
		return "", fmt.Errorf("invalid image base URL: %w", err)
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return "", fmt.Errorf("image base URL must use http or https")
	}
	parsed.Path = path.Join(parsed.Path, elem)
	return parsed.String(), nil
}

func downloadAtomic(remoteURL, destPath string) error {
	client := http.Client{Timeout: 10 * time.Minute}
	resp, err := client.Get(remoteURL)
	if err != nil {
		return fmt.Errorf("http request failed: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("remote file unavailable: %s", resp.Status)
	}

	if err := os.MkdirAll(filepath.Dir(destPath), 0755); err != nil {
		return fmt.Errorf("destination directory setup failed: %v", err)
	}

	tmp, err := os.CreateTemp(filepath.Dir(destPath), "."+filepath.Base(destPath)+".tmp-*")
	if err != nil {
		return fmt.Errorf("temporary file create failed: %v", err)
	}
	tmpPath := tmp.Name()
	defer func() {
		_ = os.Remove(tmpPath)
	}()

	written, err := io.Copy(tmp, io.LimitReader(resp.Body, maxDownloadBytes+1))
	if closeErr := tmp.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		return fmt.Errorf("download copy failed: %v", err)
	}
	if written == 0 {
		return fmt.Errorf("remote file is empty")
	}
	if written > maxDownloadBytes {
		return fmt.Errorf("remote file exceeds maximum size")
	}

	if err := os.Rename(tmpPath, destPath); err != nil {
		return fmt.Errorf("download publish failed: %v", err)
	}
	return nil
}

func copyFileAtomic(sourcePath, destPath string) (int64, error) {
	src, err := os.Open(sourcePath)
	if err != nil {
		if os.IsNotExist(err) {
			return 0, fmt.Errorf("source file not found: %s", sourcePath)
		}
		return 0, fmt.Errorf("source file open failed: %v", err)
	}
	defer src.Close()

	if err := os.MkdirAll(filepath.Dir(destPath), 0755); err != nil {
		return 0, fmt.Errorf("destination directory setup failed: %v", err)
	}

	tmp, err := os.CreateTemp(filepath.Dir(destPath), "."+filepath.Base(destPath)+".tmp-*")
	if err != nil {
		return 0, fmt.Errorf("temporary file create failed: %v", err)
	}
	tmpPath := tmp.Name()
	defer func() {
		_ = os.Remove(tmpPath)
	}()

	size, copyErr := io.Copy(tmp, src)
	closeErr := tmp.Close()
	if copyErr != nil {
		return 0, fmt.Errorf("file copy failed: %v", copyErr)
	}
	if closeErr != nil {
		return 0, fmt.Errorf("temporary file close failed: %v", closeErr)
	}
	if err := os.Rename(tmpPath, destPath); err != nil {
		return 0, fmt.Errorf("file publish failed: %v", err)
	}
	return size, nil
}
