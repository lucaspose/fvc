package internal

import (
	"errors"
	"fmt"
	"path"
	"regexp"
	"strconv"
	"strings"
)

const (
	MinMemoryMB = 128
	MaxCPUs     = 32
	MinVolumeMB = 1
	MaxVolumeMB = 1024 * 1024

	NetworkModeNAT  = "nat"
	NetworkModeNone = "none"
)

var (
	imageRefPattern = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9._/-]*(?::[a-zA-Z0-9._-]+)?$`)
	vmNamePattern   = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9._-]{0,62}$`)
	volumePattern   = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9._-]{0,62}$`)
)

type VolumeSpec struct {
	Name     string
	Target   string
	ReadOnly bool
}

func ValidateImageRef(ref string) error {
	ref = strings.TrimSpace(ref)
	if ref == "" {
		return errors.New("image source is required")
	}
	if len(ref) > 255 {
		return errors.New("image source is too long")
	}
	if !imageRefPattern.MatchString(ref) {
		return errors.New("image source contains invalid characters")
	}
	if strings.HasPrefix(ref, "/") || strings.HasSuffix(ref, "/") {
		return errors.New("image source must be relative and cannot end with slash")
	}
	for _, segment := range strings.Split(ref, "/") {
		if segment == "" || segment == "." || segment == ".." {
			return errors.New("image source contains an invalid path segment")
		}
	}
	if cleaned := path.Clean(ref); cleaned != ref {
		return errors.New("image source must be normalized")
	}
	return nil
}

func ValidateVMName(name string) error {
	if name == "" {
		return nil
	}
	if !vmNamePattern.MatchString(name) {
		return errors.New("vm name must contain only letters, numbers, dots, underscores or dashes")
	}
	return nil
}

func ValidateVolumeName(name string) error {
	name = strings.TrimSpace(name)
	if name == "" {
		return errors.New("volume name is required")
	}
	if !volumePattern.MatchString(name) {
		return errors.New("volume name must contain only letters, numbers, dots, underscores or dashes")
	}
	return nil
}

func ValidateVolumeSizeMB(sizeMB int64) error {
	if sizeMB < MinVolumeMB || sizeMB > MaxVolumeMB {
		return fmt.Errorf("volume size must be between %d MB and %d MB", MinVolumeMB, MaxVolumeMB)
	}
	return nil
}

func ValidateResources(cpus, memoryMB int32) error {
	var problems []string
	if cpus < 1 || cpus > MaxCPUs {
		problems = append(problems, fmt.Sprintf("cpu must be between 1 and %d", MaxCPUs))
	}
	if memoryMB < MinMemoryMB {
		problems = append(problems, fmt.Sprintf("memory must be at least %d MB", MinMemoryMB))
	}
	if len(problems) > 0 {
		return errors.New(strings.Join(problems, "; "))
	}
	return nil
}

func ValidatePortSpec(spec string) error {
	_, _, err := ParsePortSpec(spec)
	return err
}

func ValidateNetworkMode(mode string) error {
	mode = strings.TrimSpace(mode)
	if mode == "" || mode == NetworkModeNAT || mode == NetworkModeNone {
		return nil
	}
	return fmt.Errorf("unsupported network mode %q", mode)
}

func ParseVolumeSpec(spec string) (VolumeSpec, error) {
	spec = strings.TrimSpace(spec)
	if spec == "" {
		return VolumeSpec{}, errors.New("volume spec is required")
	}
	parts := strings.Split(spec, ":")
	if len(parts) < 2 || len(parts) > 3 {
		return VolumeSpec{}, fmt.Errorf("volume %q must be NAME:GUEST_PATH or NAME:GUEST_PATH:ro", spec)
	}
	name := strings.TrimSpace(parts[0])
	target := strings.TrimSpace(parts[1])
	mode := "rw"
	if len(parts) == 3 {
		mode = strings.ToLower(strings.TrimSpace(parts[2]))
	}
	if !volumePattern.MatchString(name) {
		return VolumeSpec{}, fmt.Errorf("volume name %q must contain only letters, numbers, dots, underscores or dashes", name)
	}
	if target == "" || !strings.HasPrefix(target, "/") {
		return VolumeSpec{}, fmt.Errorf("volume target %q must be an absolute guest path", target)
	}
	if target == "/" || strings.Contains(target, "\x00") {
		return VolumeSpec{}, fmt.Errorf("volume target %q is not allowed", target)
	}
	if cleaned := path.Clean(target); cleaned != target {
		return VolumeSpec{}, fmt.Errorf("volume target %q must be normalized", target)
	}
	for _, segment := range strings.Split(strings.TrimPrefix(target, "/"), "/") {
		if segment == "" || segment == "." || segment == ".." {
			return VolumeSpec{}, fmt.Errorf("volume target %q contains an invalid path segment", target)
		}
	}
	switch mode {
	case "", "rw":
		return VolumeSpec{Name: name, Target: target}, nil
	case "ro":
		return VolumeSpec{Name: name, Target: target, ReadOnly: true}, nil
	default:
		return VolumeSpec{}, fmt.Errorf("volume mode %q must be rw or ro", mode)
	}
}

func FormatVolumeSpec(volume VolumeSpec) string {
	spec := volume.Name + ":" + volume.Target
	if volume.ReadOnly {
		spec += ":ro"
	}
	return spec
}

func NormalizeVolumeSpec(spec string) (string, error) {
	volume, err := ParseVolumeSpec(spec)
	if err != nil {
		return "", err
	}
	return FormatVolumeSpec(volume), nil
}

func ParsePortSpec(spec string) (int, int, error) {
	spec = strings.TrimSpace(spec)
	if spec == "" {
		return 0, 0, errors.New("port mapping is required")
	}
	parts := strings.Split(spec, ":")
	if len(parts) == 1 {
		port, err := parsePort(parts[0])
		if err != nil {
			return 0, 0, err
		}
		return port, port, nil
	}
	if len(parts) != 2 {
		return 0, 0, fmt.Errorf("port mapping %q must be HOST:GUEST or PORT", spec)
	}
	host, err := parsePort(parts[0])
	if err != nil {
		return 0, 0, fmt.Errorf("host port: %w", err)
	}
	guest, err := parsePort(parts[1])
	if err != nil {
		return 0, 0, fmt.Errorf("guest port: %w", err)
	}
	return host, guest, nil
}

func NormalizePortSpec(spec string) (string, error) {
	host, guest, err := ParsePortSpec(spec)
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("%d:%d", host, guest), nil
}

func parsePort(value string) (int, error) {
	value = strings.TrimSpace(value)
	port, err := strconv.Atoi(value)
	if err != nil {
		return 0, fmt.Errorf("invalid port %q", value)
	}
	if port < 1 || port > 65535 {
		return 0, fmt.Errorf("port %d must be between 1 and 65535", port)
	}
	return port, nil
}
