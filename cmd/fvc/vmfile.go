package main

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/BurntSushi/toml"
	"github.com/lucaspose/fvc/internal"
)

type VMSection struct {
	Name  string   `toml:"name"`
	CPU   int      `toml:"cpu"`
	RAM   int      `toml:"ram"`
	Disk  int      `toml:"disk"`
	Ports []string `toml:"ports"`
}

type ImageSection struct {
	Source string `toml:"source"`
}

type VmfileConfig struct {
	VM    VMSection    `toml:"vm"`
	Image ImageSection `toml:"image"`
}

func parseVmfile(path string) (*VmfileConfig, error) {
	var config VmfileConfig
	if _, err := toml.DecodeFile(path, &config); err != nil {
		return nil, err
	}
	if err := validateVmfile(config); err != nil {
		return nil, err
	}
	return &config, nil
}

func validateVmfile(config VmfileConfig) error {
	var problems []string

	if strings.TrimSpace(config.Image.Source) == "" {
		problems = append(problems, "image.source is required")
	} else if err := internal.ValidateImageRef(config.Image.Source); err != nil {
		problems = append(problems, "image.source: "+err.Error())
	}
	if err := internal.ValidateVMName(config.VM.Name); err != nil {
		problems = append(problems, "vm.name: "+err.Error())
	}
	for _, port := range config.VM.Ports {
		if err := internal.ValidatePortSpec(port); err != nil {
			problems = append(problems, "vm.ports: "+err.Error())
		}
	}
	if config.VM.CPU < 1 || config.VM.CPU > internal.MaxCPUs {
		problems = append(problems, fmt.Sprintf("vm.cpu must be between 1 and %d", internal.MaxCPUs))
	}
	if config.VM.RAM < internal.MinMemoryMB {
		problems = append(problems, fmt.Sprintf("vm.ram must be at least %d MB", internal.MinMemoryMB))
	}
	if len(problems) > 0 {
		return errors.New(strings.Join(problems, "; "))
	}
	return nil
}

func findVmfile(sourcePath string) (string, bool, error) {
	info, err := os.Stat(sourcePath)
	if err != nil {
		return "", false, err
	}
	if info.IsDir() {
		path := filepath.Join(sourcePath, "Vmfile")
		if _, err := os.Stat(path); err == nil {
			return path, true, nil
		}
		return "", false, nil
	}
	if info.Name() == "Vmfile" {
		return sourcePath, true, nil
	}
	return "", false, nil
}
