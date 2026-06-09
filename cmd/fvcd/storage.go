package main

import (
	"path/filepath"

	"github.com/lucaspose/fvc/internal/imagestore"
)

type ImageStore struct {
	*imagestore.Store

	baseDir                string
	cacheDir               string
	activeDir              string
	snapshotDir            string
	kernelPath             string
	imageBaseURL           string
	requireChecksums       bool
	allowInsecureDownloads bool
}

type CachedImage = imagestore.CachedImage
type ImageHistory = imagestore.ImageHistory
type ImageInfo = imagestore.ImageInfo
type ImageMetadata = imagestore.ImageMetadata
type ImagePruneResult = imagestore.ImagePruneResult
type PruneItem = imagestore.PruneItem
type PruneResult = imagestore.PruneResult
type ProgressFunc = imagestore.ProgressFunc

func NewImageStore(cfg DaemonConfig) *ImageStore {
	snapshotDir := cfg.SnapshotDir
	if snapshotDir == "" {
		snapshotDir = filepath.Join(cfg.BaseDir, "snapshots")
	}
	config := imagestore.Config{
		BaseDir:                cfg.BaseDir,
		CacheDir:               cfg.CacheDir,
		ActiveDir:              cfg.ActiveDir,
		SnapshotDir:            snapshotDir,
		KernelPath:             cfg.KernelPath,
		ImageBaseURL:           cfg.ImageBaseURL,
		RequireChecksums:       cfg.RequireImageChecksums,
		AllowInsecureDownloads: cfg.AllowInsecureDownloads,
	}
	return &ImageStore{
		Store:                  imagestore.New(config),
		baseDir:                config.BaseDir,
		cacheDir:               config.CacheDir,
		activeDir:              config.ActiveDir,
		snapshotDir:            config.SnapshotDir,
		kernelPath:             config.KernelPath,
		imageBaseURL:           config.ImageBaseURL,
		requireChecksums:       config.RequireChecksums,
		allowInsecureDownloads: config.AllowInsecureDownloads,
	}
}

func (s *ImageStore) innerStore() *imagestore.Store {
	if s.Store == nil {
		s.Store = imagestore.New(imagestore.Config{
			BaseDir:                s.baseDir,
			CacheDir:               s.cacheDir,
			ActiveDir:              s.activeDir,
			SnapshotDir:            s.snapshotDir,
			KernelPath:             s.kernelPath,
			ImageBaseURL:           s.imageBaseURL,
			RequireChecksums:       s.requireChecksums,
			AllowInsecureDownloads: s.allowInsecureDownloads,
		})
	}
	return s.Store
}

func (s *ImageStore) cachedImagePath(imageName string) string {
	return s.innerStore().CachedImagePath(imageName)
}

func (s *ImageStore) imageURL(imageName string) (string, error) {
	return s.innerStore().ImageURL(imageName)
}

func (s *ImageStore) kernelURL() (string, error) {
	return s.innerStore().KernelURL()
}
