package imagestore

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
	"github.com/lucaspose/fvc/internal/storeio"
)

// Config describes where image store artifacts live on disk.
type Config struct {
	BaseDir                string
	CacheDir               string
	ActiveDir              string
	SnapshotDir            string
	KernelPath             string
	ImageBaseURL           string
	RequireChecksums       bool
	AllowInsecureDownloads bool
}

// Store manages cached rootfs images and related artifacts.
type Store struct {
	baseDir                string
	cacheDir               string
	activeDir              string
	snapshotDir            string
	kernelPath             string
	imageBaseURL           string
	requireChecksums       bool
	allowInsecureDownloads bool
}

// CachedImage is a rootfs image discovered in the local cache.
type CachedImage struct {
	Name      string
	Path      string
	SizeBytes int64
	Encoded   bool
}

// PruneResult summarizes removed cache files.
type PruneResult struct {
	RemovedFiles int32
	FreedBytes   int64
	Items        []PruneItem
}

// PruneItem describes one removed cache file.
type PruneItem struct {
	Kind      string
	Path      string
	SizeBytes int64
}

// ProgressFunc reports byte-level transfer progress.
type ProgressFunc = storeio.ProgressFunc

// New creates an image store backed by cfg.
func New(cfg Config) *Store {
	snapshotDir := cfg.SnapshotDir
	if snapshotDir == "" {
		snapshotDir = filepath.Join(cfg.BaseDir, "snapshots")
	}
	return &Store{
		baseDir:                cfg.BaseDir,
		cacheDir:               cfg.CacheDir,
		activeDir:              cfg.ActiveDir,
		snapshotDir:            snapshotDir,
		kernelPath:             cfg.KernelPath,
		imageBaseURL:           cfg.ImageBaseURL,
		requireChecksums:       cfg.RequireChecksums,
		allowInsecureDownloads: cfg.AllowInsecureDownloads,
	}
}

func (s *Store) BaseDir() string {
	return s.baseDir
}

func (s *Store) CacheDir() string {
	return s.cacheDir
}

func (s *Store) SnapshotDir() string {
	return s.snapshotDir
}

func (s *Store) KernelPath() string {
	return s.kernelPath
}

// Init creates the store directories if needed.
func (s *Store) Init() error {
	for _, dir := range []string{s.baseDir, s.cacheDir, s.activeDir, s.snapshotDir} {
		if err := os.MkdirAll(dir, 0755); err != nil {
			return fmt.Errorf("storage directory setup failed for %s: %v", dir, err)
		}
	}
	return nil
}

func (s *Store) PullImageIfNeeded(imageName string) (string, error) {
	return s.PullImageIfNeededProgress(imageName, nil)
}

func (s *Store) PullImageIfNeededProgress(imageName string, progress ProgressFunc) (string, error) {
	if err := internal.ValidateImageRef(imageName); err != nil {
		return "", err
	}

	localPath := s.CachedImagePath(imageName)
	if _, err := os.Stat(localPath); err == nil {
		return localPath, nil
	}

	remoteURL, err := s.ImageURL(imageName)
	if err != nil {
		return "", err
	}
	checksum, err := s.remoteChecksum(remoteURL)
	if err != nil {
		return "", err
	}
	if err := storeio.DownloadAtomicVerifiedProgress(remoteURL, localPath, checksum, progress); err != nil {
		return "", fmt.Errorf("cannot download image %q from %s: %w", imageName, remoteURL, err)
	}
	return localPath, nil
}

func (s *Store) CloneImage(imageName, vmID string) (string, error) {
	return s.CloneImageProgress(imageName, vmID, nil)
}

func (s *Store) CloneImageProgress(imageName, vmID string, progress ProgressFunc) (string, error) {
	if err := internal.ValidateImageRef(imageName); err != nil {
		return "", err
	}
	if vmID == "" || strings.ContainsAny(vmID, `/\`) {
		return "", fmt.Errorf("invalid vm id %q", vmID)
	}

	sourcePath := s.CachedImagePath(imageName)
	destPath := filepath.Join(s.activeDir, vmID+".ext4")
	if _, err := storeio.CopyFileAtomicProgress(sourcePath, destPath, progress); err != nil {
		return "", fmt.Errorf("ephemeral clone failed: %w", err)
	}

	return destPath, nil
}

func (s *Store) PullKernelIfNeeded() (string, error) {
	return s.PullKernelIfNeededProgress(nil)
}

func (s *Store) PullKernelIfNeededProgress(progress ProgressFunc) (string, error) {
	localPath := s.kernelPath

	if _, err := os.Stat(localPath); err == nil {
		return localPath, nil
	}

	if err := os.MkdirAll(filepath.Dir(localPath), 0755); err != nil {
		return "", fmt.Errorf("kernel directory setup failed: %v", err)
	}

	remoteURL, err := s.KernelURL()
	if err != nil {
		return "", err
	}
	checksum, err := s.remoteChecksum(remoteURL)
	if err != nil {
		return "", err
	}
	if err := storeio.DownloadAtomicVerifiedProgress(remoteURL, localPath, checksum, progress); err != nil {
		return "", fmt.Errorf("cannot download kernel from %s: %w", remoteURL, err)
	}
	return localPath, nil
}

func (s *Store) remoteChecksum(remoteURL string) (string, error) {
	if !s.requireChecksums {
		return "", nil
	}
	if !s.allowInsecureDownloads && !secureDownloadURL(remoteURL) {
		return "", fmt.Errorf("refusing insecure image download %s; use https or set FVC_ALLOW_INSECURE_DOWNLOADS=true for isolated development", remoteURL)
	}
	checksumURL := remoteURL + ".sha256"
	client := http.Client{Timeout: 30 * time.Second}
	resp, err := client.Get(checksumURL)
	if err != nil {
		return "", fmt.Errorf("checksum request failed for %s: %w", checksumURL, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("checksum unavailable for %s: %s", remoteURL, resp.Status)
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, 4096))
	if err != nil {
		return "", fmt.Errorf("checksum read failed for %s: %w", remoteURL, err)
	}
	fields := strings.Fields(string(data))
	if len(fields) == 0 {
		return "", fmt.Errorf("checksum file is empty for %s", remoteURL)
	}
	return fields[0], nil
}

func secureDownloadURL(rawURL string) bool {
	parsed, err := url.Parse(rawURL)
	if err != nil {
		return false
	}
	return parsed.Scheme == "https"
}

func (s *Store) CachedImagePath(imageName string) string {
	fileName := base64.RawURLEncoding.EncodeToString([]byte(imageName)) + ".ext4"
	return filepath.Join(s.cacheDir, fileName)
}

func (s *Store) ListImages() ([]CachedImage, error) {
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

func (s *Store) PruneImageCache(dryRun bool) (PruneResult, error) {
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

func (s *Store) ImageURL(imageName string) (string, error) {
	return joinBaseURL(s.imageBaseURL, imageName+".ext4")
}

func (s *Store) KernelURL() (string, error) {
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
