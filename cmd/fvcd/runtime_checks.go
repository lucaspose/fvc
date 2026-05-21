package main

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
)

type RuntimeDiagnostic struct {
	Name    string
	OK      bool
	Message string
}

func RuntimeDiagnostics(cfg DaemonConfig) []RuntimeDiagnostic {
	checks := []RuntimeDiagnostic{
		checkExecutable("firecracker", cfg.FirecrackerPath),
		checkDevice("/dev/kvm"),
		checkCommand("mount"),
		checkCommand("umount"),
	}
	if cfg.NetworkEnabled {
		checks = append(checks,
			checkDevice("/dev/net/tun"),
			checkCommand("ip"),
			checkCommand("iptables"),
			checkCommand("sysctl"),
		)
	}
	return checks
}

func RuntimeDiagnosticsError(checks []RuntimeDiagnostic) error {
	var problems []string
	for _, check := range checks {
		if !check.OK {
			problems = append(problems, fmt.Sprintf("%s: %s", check.Name, check.Message))
		}
	}
	if len(problems) == 0 {
		return nil
	}
	return errors.New(strings.Join(problems, "; "))
}

func checkExecutable(name, path string) RuntimeDiagnostic {
	if strings.TrimSpace(path) == "" {
		return RuntimeDiagnostic{Name: name, OK: false, Message: "path is empty"}
	}
	info, err := os.Stat(path)
	if err != nil {
		return RuntimeDiagnostic{Name: name, OK: false, Message: err.Error()}
	}
	if info.IsDir() {
		return RuntimeDiagnostic{Name: name, OK: false, Message: "path is a directory"}
	}
	if info.Mode()&0111 == 0 {
		return RuntimeDiagnostic{Name: name, OK: false, Message: "file is not executable"}
	}
	return RuntimeDiagnostic{Name: name, OK: true, Message: path}
}

func checkCommand(name string) RuntimeDiagnostic {
	path, err := exec.LookPath(name)
	if err != nil {
		return RuntimeDiagnostic{Name: name, OK: false, Message: err.Error()}
	}
	return RuntimeDiagnostic{Name: name, OK: true, Message: path}
}

func checkDevice(path string) RuntimeDiagnostic {
	info, err := os.Stat(path)
	if err != nil {
		return RuntimeDiagnostic{Name: path, OK: false, Message: err.Error()}
	}
	if info.Mode()&os.ModeDevice == 0 {
		return RuntimeDiagnostic{Name: path, OK: false, Message: "path is not a device"}
	}
	return RuntimeDiagnostic{Name: path, OK: true, Message: "available"}
}
