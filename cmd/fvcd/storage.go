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
	"strings"
	"time"

	"github.com/lucaspose/fvc/internal"
)

const maxDownloadBytes = 10 << 30 // 10 GiB

type ImageStore struct {
	baseDir      string
	cacheDir     string
	activeDir    string
	kernelPath   string
	imageBaseURL string
}

func NewImageStore(cfg DaemonConfig) *ImageStore {
	return &ImageStore{
		baseDir:      cfg.BaseDir,
		cacheDir:     cfg.CacheDir,
		activeDir:    cfg.ActiveDir,
		kernelPath:   cfg.KernelPath,
		imageBaseURL: cfg.ImageBaseURL,
	}
}

func (s *ImageStore) Init() error {
	for _, dir := range []string{s.baseDir, s.cacheDir, s.activeDir} {
		if err := os.MkdirAll(dir, 0755); err != nil {
			return fmt.Errorf("impossible de creer le dossier systeme %s (as-tu les droits sudo ?) : %v", dir, err)
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

	fmt.Printf("Pulling image %s...\n", imageName)
	remoteURL, err := s.imageURL(imageName)
	if err != nil {
		return "", err
	}
	if err := downloadAtomic(remoteURL, localPath); err != nil {
		return "", err
	}
	fmt.Printf("Image %s telechargee avec succes et mise en cache.\n", imageName)
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

	src, err := os.Open(sourcePath)
	if err != nil {
		return "", fmt.Errorf("impossible d'ouvrir l'image source: %v", err)
	}
	defer src.Close()

	dst, err := os.Create(destPath)
	if err != nil {
		return "", fmt.Errorf("impossible de creer le clone ephemere: %v", err)
	}
	defer dst.Close()

	_, err = io.Copy(dst, src)
	if err != nil {
		return "", fmt.Errorf("echec du clonage: %v", err)
	}

	return destPath, nil
}

func (s *ImageStore) PullKernelIfNeeded() (string, error) {
	localPath := s.kernelPath

	if _, err := os.Stat(localPath); err == nil {
		return localPath, nil
	}

	if err := os.MkdirAll(filepath.Dir(localPath), 0755); err != nil {
		return "", fmt.Errorf("impossible de creer le dossier du noyau: %v", err)
	}

	fmt.Println("Kernel vmlinux.bin manquant. Telechargement...")
	remoteURL, err := s.kernelURL()
	if err != nil {
		return "", err
	}
	if err := downloadAtomic(remoteURL, localPath); err != nil {
		return "", err
	}
	fmt.Println("Noyau vmlinux.bin telecharge et configure avec succes.")
	return localPath, nil
}

func (s *ImageStore) cachedImagePath(imageName string) string {
	fileName := base64.RawURLEncoding.EncodeToString([]byte(imageName)) + ".ext4"
	return filepath.Join(s.cacheDir, fileName)
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
		return fmt.Errorf("echec de la requete HTTP: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("fichier distant introuvable (Status: %s)", resp.Status)
	}

	if err := os.MkdirAll(filepath.Dir(destPath), 0755); err != nil {
		return fmt.Errorf("impossible de creer le dossier destination: %v", err)
	}

	tmp, err := os.CreateTemp(filepath.Dir(destPath), "."+filepath.Base(destPath)+".tmp-*")
	if err != nil {
		return fmt.Errorf("impossible de creer le fichier temporaire: %v", err)
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
		return fmt.Errorf("erreur pendant le telechargement: %v", err)
	}
	if written == 0 {
		return fmt.Errorf("fichier distant vide")
	}
	if written > maxDownloadBytes {
		return fmt.Errorf("fichier distant trop volumineux")
	}

	if err := os.Rename(tmpPath, destPath); err != nil {
		return fmt.Errorf("impossible de publier le fichier telecharge: %v", err)
	}
	return nil
}
