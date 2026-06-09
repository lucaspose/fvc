package guestruntime

import (
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/lucaspose/fvc/internal/rootfs"
)

const ConfigPath = "/etc/fvc/runtime.json"
const InitPath = "/usr/local/bin/fvc-init"

type Config struct {
	Env        []string `json:"env,omitempty"`
	Cmd        []string `json:"cmd,omitempty"`
	Workdir    string   `json:"workdir,omitempty"`
	Volumes    []Volume `json:"volumes,omitempty"`
	RandomSeed string   `json:"random_seed,omitempty"`
}

type Volume struct {
	Device   string `json:"device"`
	Target   string `json:"target"`
	ReadOnly bool   `json:"read_only,omitempty"`
}

func RequiresInit(config Config) bool {
	return len(config.Cmd) > 0 || len(config.Env) > 0 || strings.TrimSpace(config.Workdir) != "" || len(config.Volumes) > 0
}

func GenerateRandomSeed() (string, error) {
	seed := make([]byte, 64)
	if _, err := rand.Read(seed); err != nil {
		return "", fmt.Errorf("runtime random seed generation failed: %w", err)
	}
	return base64.StdEncoding.EncodeToString(seed), nil
}

// Install copies fvc-init and writes runtime.json inside a mounted guest rootfs.
func Install(mountDir, hostInitPath string, config Config) error {
	initTarget, err := rootfs.Path(mountDir, InitPath)
	if err != nil {
		return err
	}
	if err := rootfs.CopyRegularFile(hostInitPath, initTarget, 0755); err != nil {
		return fmt.Errorf("runtime init install failed: %w", err)
	}

	configTarget, err := rootfs.Path(mountDir, ConfigPath)
	if err != nil {
		return err
	}
	seed, err := GenerateRandomSeed()
	if err != nil {
		return err
	}
	config.RandomSeed = seed
	data, err := json.MarshalIndent(config, "", "  ")
	if err != nil {
		return fmt.Errorf("runtime config encode failed: %w", err)
	}
	if err := rootfs.EnsureDirNoSymlink(filepath.Dir(configTarget), 0755); err != nil {
		return fmt.Errorf("runtime config parent create failed: %w", err)
	}
	if err := rootfs.WriteFileNoFollow(configTarget, append(data, '\n'), 0644); err != nil {
		return fmt.Errorf("runtime config write failed: %w", err)
	}
	return nil
}

func ValidateHostInit(path string) error {
	if strings.TrimSpace(path) == "" {
		return fmt.Errorf("runtime init is required by image metadata but FVC_RUNTIME_INIT_PATH is empty")
	}
	if !filepath.IsAbs(path) {
		return fmt.Errorf("runtime init path must be absolute: %s", path)
	}
	linkInfo, err := os.Lstat(path)
	if err != nil {
		return fmt.Errorf("runtime init unavailable at %s: %w", path, err)
	}
	if linkInfo.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("runtime init path must not be a symlink: %s", path)
	}
	info, err := os.Stat(path)
	if err != nil {
		return fmt.Errorf("runtime init unavailable at %s: %w", path, err)
	}
	if info.IsDir() {
		return fmt.Errorf("runtime init path is a directory: %s", path)
	}
	if info.Mode()&0111 == 0 {
		return fmt.Errorf("runtime init path is not executable: %s", path)
	}
	return nil
}
