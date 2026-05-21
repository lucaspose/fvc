package main

import (
	"bytes"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"

	"github.com/lucaspose/fvc/proto"
)

func TestValidateVmfileAcceptsMinimalConfig(t *testing.T) {
	config := VmfileConfig{
		VM: VMSection{
			Name: "api",
			CPU:  2,
			RAM:  512,
		},
		Image: ImageSection{
			Source: "debian:bookworm",
		},
	}

	if err := validateVmfile(config); err != nil {
		t.Fatalf("expected config to be valid, got %v", err)
	}
}

func TestValidateVmfileRejectsMissingImage(t *testing.T) {
	config := VmfileConfig{
		VM: VMSection{
			CPU: 1,
			RAM: 512,
		},
	}

	if err := validateVmfile(config); err == nil {
		t.Fatal("expected missing image to be rejected")
	}
}

func TestValidateVmfileRejectsInvalidResources(t *testing.T) {
	config := VmfileConfig{
		VM: VMSection{
			CPU: 0,
			RAM: 64,
		},
		Image: ImageSection{
			Source: "debian",
		},
	}

	if err := validateVmfile(config); err == nil {
		t.Fatal("expected invalid resources to be rejected")
	}
}

func TestValidateVmfileRejectsUnsafeImage(t *testing.T) {
	config := VmfileConfig{
		VM: VMSection{
			CPU: 1,
			RAM: 512,
		},
		Image: ImageSection{
			Source: "../debian",
		},
	}

	if err := validateVmfile(config); err == nil {
		t.Fatal("expected unsafe image to be rejected")
	}
}

func TestSplitConsoleLines(t *testing.T) {
	lines := splitConsoleLines([]byte("one\ntwo\nthree\n"))
	if len(lines) != 3 {
		t.Fatalf("expected 3 lines, got %d", len(lines))
	}
	if string(lines[2]) != "three" {
		t.Fatalf("unexpected last line: %q", lines[2])
	}
}

func TestOpenConsoleWriterMissingReaderDoesNotBlock(t *testing.T) {
	path := filepath.Join(t.TempDir(), "console.in")
	if err := syscall.Mkfifo(path, 0600); err != nil {
		t.Fatalf("mkfifo failed: %v", err)
	}
	if _, err := openConsoleWriter(path); err == nil {
		t.Fatal("expected openConsoleWriter to fail without a reader")
	}
}

func TestNormalizeExecCommandStripsSeparator(t *testing.T) {
	command, err := normalizeExecCommand([]string{"--", "/bin/sh", "-lc", "echo ok"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(command) != 3 || command[0] != "/bin/sh" || command[2] != "echo ok" {
		t.Fatalf("unexpected command: %#v", command)
	}
}

func TestNormalizeExecCommandRejectsEmptyAfterSeparator(t *testing.T) {
	if _, err := normalizeExecCommand([]string{"--"}); err == nil {
		t.Fatal("expected empty command after separator to be rejected")
	}
}

func TestParsePullOptionsAcceptsTargetAfterImage(t *testing.T) {
	options, err := parsePullOptions([]string{"--from", "docker", "hello-world:latest", "-t", "hello-fvc:latest"})
	if err != nil {
		t.Fatalf("parsePullOptions failed: %v", err)
	}
	if options.image != "hello-world:latest" || options.source != "docker" || options.target != "hello-fvc:latest" {
		t.Fatalf("unexpected options: %#v", options)
	}
}

func TestParsePullOptionsAcceptsTargetBeforeImage(t *testing.T) {
	options, err := parsePullOptions([]string{"--from=docker", "--tag=hello-fvc:latest", "hello-world:latest"})
	if err != nil {
		t.Fatalf("parsePullOptions failed: %v", err)
	}
	if options.image != "hello-world:latest" || options.source != "docker" || options.target != "hello-fvc:latest" {
		t.Fatalf("unexpected options: %#v", options)
	}
}

func TestParsePullOptionsDefaultsDockerTargetToSource(t *testing.T) {
	options, err := parsePullOptions([]string{"--from", "docker", "hello-world:latest"})
	if err != nil {
		t.Fatalf("parsePullOptions failed: %v", err)
	}
	if options.target != "hello-world:latest" {
		t.Fatalf("unexpected target %q", options.target)
	}
}

func TestRunCLIWithoutCommandPrintsUsageAndSkipsDaemon(t *testing.T) {
	var out, errOut bytes.Buffer
	calledConnector := false

	code := runCLI(nil, &out, &errOut, defaultCommands(), func() (proto.FvcServiceClient, func() error, error) {
		calledConnector = true
		return nil, nil, nil
	})

	if code != 1 {
		t.Fatalf("expected exit code 1, got %d", code)
	}
	if calledConnector {
		t.Fatal("connector should not be called when no command is provided")
	}
	if !bytes.Contains(out.Bytes(), []byte("Usage: fvc <command> [arguments]")) {
		t.Fatalf("usage was not printed: %q", out.String())
	}
	if errOut.Len() != 0 {
		t.Fatalf("unexpected stderr output: %q", errOut.String())
	}
}

func TestRunCLIUnknownCommandPrintsErrorAndSkipsDaemon(t *testing.T) {
	var out, errOut bytes.Buffer
	calledConnector := false

	code := runCLI([]string{"wat"}, &out, &errOut, defaultCommands(), func() (proto.FvcServiceClient, func() error, error) {
		calledConnector = true
		return nil, nil, nil
	})

	if code != 1 {
		t.Fatalf("expected exit code 1, got %d", code)
	}
	if calledConnector {
		t.Fatal("connector should not be called for an unknown command")
	}
	if !bytes.Contains(errOut.Bytes(), []byte(`unknown command "wat"`)) {
		t.Fatalf("unknown-command error was not printed: %q", errOut.String())
	}
	if !bytes.Contains(out.Bytes(), []byte("Commands:")) {
		t.Fatalf("usage was not printed: %q", out.String())
	}
}

func TestRunCLIPropagatesCommandExitCode(t *testing.T) {
	var out, errOut bytes.Buffer
	commands := map[string]commandFunc{
		"exit": func(proto.FvcServiceClient, []string) error {
			return cliExitError{code: 42}
		},
	}

	code := runCLI([]string{"exit"}, &out, &errOut, commands, func() (proto.FvcServiceClient, func() error, error) {
		return nil, func() error { return nil }, nil
	})

	if code != 42 {
		t.Fatalf("expected exit code 42, got %d", code)
	}
	if errOut.Len() != 0 {
		t.Fatalf("unexpected stderr output: %q", errOut.String())
	}
}

func TestRunCLIReportsConnectorFailure(t *testing.T) {
	var out, errOut bytes.Buffer
	commands := map[string]commandFunc{
		"noop": func(proto.FvcServiceClient, []string) error {
			t.Fatal("command should not run when connector fails")
			return nil
		},
	}

	code := runCLI([]string{"noop"}, &out, &errOut, commands, func() (proto.FvcServiceClient, func() error, error) {
		return nil, nil, errors.New("daemon down")
	})

	if code != 1 {
		t.Fatalf("expected exit code 1, got %d", code)
	}
	if !bytes.Contains(errOut.Bytes(), []byte("daemon down")) {
		t.Fatalf("connector error was not printed: %q", errOut.String())
	}
}

func TestRunCLICallsCloseAfterCommand(t *testing.T) {
	var out, errOut bytes.Buffer
	closed := false
	commands := map[string]commandFunc{
		"noop": func(proto.FvcServiceClient, []string) error {
			return nil
		},
	}

	code := runCLI([]string{"noop"}, &out, &errOut, commands, func() (proto.FvcServiceClient, func() error, error) {
		return nil, func() error {
			closed = true
			return nil
		}, nil
	})

	if code != 0 {
		t.Fatalf("expected exit code 0, got %d", code)
	}
	if !closed {
		t.Fatal("client close function was not called")
	}
}

func TestVMHandlersValidateRequiredArgumentsBeforeDaemonCalls(t *testing.T) {
	tests := []struct {
		name string
		run  func() error
		want string
	}{
		{"stop", func() error { return executeStop(nil, nil) }, "stop requires a microVM ID"},
		{"start", func() error { return executeStart(nil, nil) }, "start requires a microVM ID"},
		{"rm", func() error { return executeRm(nil, nil) }, "rm requires a microVM ID"},
		{"inspect", func() error { return executeInspect(nil, nil) }, "inspect requires a microVM ID"},
		{"wait", func() error { return executeWait(nil, nil) }, "wait requires a microVM ID"},
		{"kill", func() error { return executeKill(nil, nil) }, "kill requires a microVM ID"},
		{"stats interval", func() error { return executeStats(nil, []string{"--interval", "0s"}) }, "interval must be greater than zero"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.run()
			if err == nil {
				t.Fatal("expected validation error")
			}
			if !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("error %q does not contain %q", err.Error(), tt.want)
			}
		})
	}
}

func TestSnapshotHandlersValidateRequiredArgumentsBeforeDaemonCalls(t *testing.T) {
	tests := []struct {
		name string
		run  func() error
		want string
	}{
		{"root", func() error { return executeSnapshot(nil, nil) }, "snapshot requires a subcommand"},
		{"create", func() error { return executeSnapshotCreate(nil, []string{"vm-1"}) }, "snapshot create requires"},
		{"list", func() error { return executeSnapshotList(nil, nil) }, "snapshot ls requires"},
		{"restore", func() error { return executeSnapshotRestore(nil, []string{"vm-1"}) }, "snapshot restore requires"},
		{"remove", func() error { return executeSnapshotRemove(nil, []string{"vm-1"}) }, "snapshot rm requires"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.run()
			if err == nil {
				t.Fatal("expected validation error")
			}
			if !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("error %q does not contain %q", err.Error(), tt.want)
			}
		})
	}
}

func TestImageHandlersValidateRequiredArgumentsBeforeDaemonCalls(t *testing.T) {
	tests := []struct {
		name string
		run  func() error
		want string
	}{
		{"root", func() error { return executeImage(nil, nil) }, "image requires a subcommand"},
		{"inspect", func() error { return executeImageInspect(nil, nil) }, "image inspect requires"},
		{"remove", func() error { return executeImageRemove(nil, nil) }, "image rm requires"},
		{"tag", func() error { return executeImageTag(nil, []string{"ubuntu"}) }, "image tag requires"},
		{"import", func() error { return executeImageImport(nil, []string{"rootfs.ext4"}) }, "image import requires"},
		{"export", func() error { return executeImageExport(nil, []string{"ubuntu"}) }, "image export requires"},
		{"history", func() error { return executeImageHistory(nil, nil) }, "image history requires"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.run()
			if err == nil {
				t.Fatal("expected validation error")
			}
			if !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("error %q does not contain %q", err.Error(), tt.want)
			}
		})
	}
}

func TestPrintVMInspectIncludesRuntimeDetails(t *testing.T) {
	t.Setenv("NO_COLOR", "1")
	output := captureStdout(t, func() {
		printVMInspect(&proto.VmDetails{
			VmId:        "vm-123",
			Name:        "web",
			Status:      "running",
			Pid:         42,
			Image:       "nginx-fvc:latest",
			ExitCode:    -1,
			GuestIp:     "172.16.0.2",
			MacAddress:  "02:FC:00:00:00:01",
			TapName:     "fvc0",
			LogPath:     "/var/lib/fvc/logs/vm.log",
			DrivePath:   "/var/lib/fvc/active/vm.ext4",
			ConsolePath: "/run/fvc/vm.console.in",
			Config: &proto.VmConfig{
				Cpus:     2,
				MemoryMb: 1024,
				Ports:    []string{"8080:80"},
			},
		})
	})

	for _, want := range []string{"[INSPECT] vm-123", "name:", "web", "image:", "nginx-fvc:latest", "cpu:", "2", "ram:", "1024 MB", "ports:", "8080:80", "console:", "/run/fvc/vm.console.in"} {
		if !strings.Contains(output, want) {
			t.Fatalf("inspect output missing %q:\n%s", want, output)
		}
	}
}

func TestPrintStatsTableFormatsResourceColumns(t *testing.T) {
	t.Setenv("NO_COLOR", "1")
	output := captureStdout(t, func() {
		printStatsTable([]*proto.VmStats{{
			VmId:          "vm-123",
			Pid:           42,
			Status:        "running",
			CpuPercent:    12.5,
			RssBytes:      128 * 1024 * 1024,
			MemoryMb:      512,
			UptimeSeconds: 65,
		}})
	})

	for _, want := range []string{"VM ID", "vm-123", "12.5", "128.0MB / 512MB", "1m05s"} {
		if !strings.Contains(output, want) {
			t.Fatalf("stats output missing %q:\n%s", want, output)
		}
	}
}

func captureStdout(t *testing.T, fn func()) string {
	t.Helper()
	old := os.Stdout
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("pipe failed: %v", err)
	}
	os.Stdout = w
	defer func() {
		os.Stdout = old
	}()

	fn()

	if err := w.Close(); err != nil {
		t.Fatalf("stdout pipe close failed: %v", err)
	}
	data, err := io.ReadAll(r)
	if err != nil {
		t.Fatalf("stdout pipe read failed: %v", err)
	}
	return string(data)
}
