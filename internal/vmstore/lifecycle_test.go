package vmstore

import (
	"database/sql"
	"testing"

	"github.com/lucaspose/fvc/internal"
)

func TestInsertRunningAndLifecycleReads(t *testing.T) {
	db := openTestDB(t)
	err := InsertRunning(db, RunRecord{
		ID:               "vm-1",
		Name:             "api",
		PID:              123,
		ProcessStartTime: "456",
		Image:            "ubuntu",
		CPUs:             2,
		MemoryMB:         1024,
		Ports:            []string{"8080:80", "443:443"},
		NetworkMode:      "nat",
		Volumes:          []string{"data:/data", "cache:/cache:ro"},
		LogPath:          "/tmp/vm.log",
		DrivePath:        "/tmp/vm.ext4",
		ConsolePath:      "/tmp/vm.console",
		Network:          NetworkFields{TapName: "tap0", GuestIP: "172.16.0.2", MAC: "02:FC:00:00:00:01"},
		AgentToken:       "token",
		VsockPath:        "/tmp/vm.vsock",
		AutoRemove:       true,
	})
	if err != nil {
		t.Fatalf("InsertRunning failed: %v", err)
	}

	stopState, err := GetStopState(db, "vm-1")
	if err != nil {
		t.Fatalf("GetStopState failed: %v", err)
	}
	if stopState.PID != 123 || stopState.ProcessStartTime != "456" || stopState.Status != internal.VmRunning || stopState.Network.Ports[0] != "8080:80" || stopState.VsockPath == "" || !stopState.AutoRemove || stopState.NetworkMode != "nat" {
		t.Fatalf("unexpected stop state: %#v", stopState)
	}
	runtimeState, err := GetRuntimeState(db, "vm-1")
	if err != nil {
		t.Fatalf("GetRuntimeState failed: %v", err)
	}
	if runtimeState.DrivePath != "/tmp/vm.ext4" || runtimeState.LogPath != "/tmp/vm.log" || !runtimeState.AutoRemove || runtimeState.NetworkMode != "nat" {
		t.Fatalf("unexpected runtime state: %#v", runtimeState)
	}

	startState, err := GetStartState(db, "vm-1")
	if err != nil {
		t.Fatalf("GetStartState failed: %v", err)
	}
	if startState.Image != "ubuntu" || startState.CPUs != 2 || startState.MemoryMB != 1024 || startState.AgentToken != "token" || startState.NetworkMode != "nat" || len(startState.Volumes) != 2 {
		t.Fatalf("unexpected start state: %#v", startState)
	}

	execState, err := GetExecAgentState(db, "vm-1")
	if err != nil {
		t.Fatalf("GetExecAgentState failed: %v", err)
	}
	if execState.Status != internal.VmRunning || execState.GuestIP != "172.16.0.2" || execState.AgentToken != "token" || execState.VsockPath == "" {
		t.Fatalf("unexpected exec state: %#v", execState)
	}
}

func TestMarkRunningStoppedExitedAndDelete(t *testing.T) {
	db := openTestDB(t)
	if _, err := db.Exec(`INSERT INTO vms (id, status, image, drive_path, log_path) VALUES (?, ?, ?, ?, ?)`, "vm-1", internal.VmStopped, "ubuntu", "/tmp/vm.ext4", "/tmp/vm.log"); err != nil {
		t.Fatalf("insert vm failed: %v", err)
	}

	err := MarkRunning(db, RunningUpdate{
		ID:               "vm-1",
		PID:              321,
		ProcessStartTime: "654",
		ConsolePath:      "/tmp/vm.console",
		Network:          NetworkFields{TapName: "tap0", GuestIP: "172.16.0.2", MAC: "02:FC:00:00:00:01"},
		AgentToken:       "token",
		VsockPath:        "/tmp/vm.vsock",
	})
	if err != nil {
		t.Fatalf("MarkRunning failed: %v", err)
	}
	stopState, err := GetStopState(db, "vm-1")
	if err != nil {
		t.Fatalf("GetStopState failed: %v", err)
	}
	if stopState.Status != internal.VmRunning || stopState.PID != 321 || stopState.Network.GuestIP == "" {
		t.Fatalf("unexpected running state: %#v", stopState)
	}

	if err := MarkStoppedIfRunning(db, "vm-1", 7); err != nil {
		t.Fatalf("MarkStoppedIfRunning failed: %v", err)
	}
	waitStatus, err := GetWaitStatus(db, "vm-1")
	if err != nil {
		t.Fatalf("GetWaitStatus failed: %v", err)
	}
	if waitStatus.Status != internal.VmExited || waitStatus.ExitCode != 7 {
		t.Fatalf("unexpected exited status: %#v", waitStatus)
	}

	if err := MarkStopped(db, "vm-1"); err != nil {
		t.Fatalf("MarkStopped failed: %v", err)
	}
	waitStatus, err = GetWaitStatus(db, "vm-1")
	if err != nil {
		t.Fatalf("GetWaitStatus failed: %v", err)
	}
	if waitStatus.Status != internal.VmStopped || waitStatus.ExitCode != -1 {
		t.Fatalf("unexpected stopped status: %#v", waitStatus)
	}

	if err := Delete(db, "vm-1"); err != nil {
		t.Fatalf("Delete failed: %v", err)
	}
	if _, err := GetStopState(db, "vm-1"); err != sql.ErrNoRows {
		t.Fatalf("expected sql.ErrNoRows after delete, got %v", err)
	}
}

func TestConsoleAndLogState(t *testing.T) {
	db := openTestDB(t)
	if _, err := db.Exec(`INSERT INTO vms (id, status, image, log_path, console_path) VALUES (?, ?, ?, ?, ?)`, "vm-1", internal.VmRunning, "ubuntu", "/tmp/vm.log", "/tmp/vm.console"); err != nil {
		t.Fatalf("insert vm failed: %v", err)
	}

	consoleState, err := GetConsoleState(db, "vm-1")
	if err != nil {
		t.Fatalf("GetConsoleState failed: %v", err)
	}
	if consoleState.Status != internal.VmRunning || consoleState.LogPath != "/tmp/vm.log" || consoleState.ConsolePath != "/tmp/vm.console" {
		t.Fatalf("unexpected console state: %#v", consoleState)
	}
	logPath, err := LogPath(db, "vm-1")
	if err != nil {
		t.Fatalf("LogPath failed: %v", err)
	}
	if logPath != "/tmp/vm.log" {
		t.Fatalf("unexpected log path: %s", logPath)
	}
}

func TestSnapshotDriveStateAndExists(t *testing.T) {
	db := openTestDB(t)
	if _, err := db.Exec(`INSERT INTO vms (id, status, image, drive_path) VALUES (?, ?, ?, ?)`, "vm-1", internal.VmStopped, "ubuntu", "/tmp/vm.ext4"); err != nil {
		t.Fatalf("insert vm failed: %v", err)
	}

	state, err := GetSnapshotDriveState(db, "vm-1")
	if err != nil {
		t.Fatalf("GetSnapshotDriveState failed: %v", err)
	}
	if state.Status != internal.VmStopped || state.DrivePath != "/tmp/vm.ext4" {
		t.Fatalf("unexpected snapshot drive state: %#v", state)
	}
	if err := Exists(db, "vm-1"); err != nil {
		t.Fatalf("Exists failed: %v", err)
	}
	if err := Exists(db, "missing"); err != sql.ErrNoRows {
		t.Fatalf("expected sql.ErrNoRows for missing VM, got %v", err)
	}
}
