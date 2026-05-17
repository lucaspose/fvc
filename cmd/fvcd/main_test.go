package main

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/lucaspose/fvc/proto"
	_ "modernc.org/sqlite"
)

func TestValidateRunRequestDefaults(t *testing.T) {
	image, cpus, memory, err := validateRunRequest(&proto.RunRequest{})
	if err != nil {
		t.Fatalf("expected default request to be valid, got %v", err)
	}
	if image != "ubuntu" || cpus != 1 || memory != 512 {
		t.Fatalf("unexpected defaults: image=%s cpus=%d memory=%d", image, cpus, memory)
	}
}

func TestValidateRunRequestRejectsUnsafeImage(t *testing.T) {
	_, _, _, err := validateRunRequest(&proto.RunRequest{Source: "../debian"})
	if err == nil {
		t.Fatal("expected unsafe image ref to be rejected")
	}
}

func TestValidateRunRequestRejectsInvalidResources(t *testing.T) {
	_, _, _, err := validateRunRequest(&proto.RunRequest{
		Source: "debian",
		Config: &proto.VmConfig{Cpus: 0, MemoryMb: 64},
	})
	if err == nil || !strings.Contains(err.Error(), "cpu") {
		t.Fatalf("expected resource validation error, got %v", err)
	}
}

func TestRunMicroVMEmitsValidationError(t *testing.T) {
	server := Server{Config: DaemonConfig{NetworkEnabled: false}}
	var events []*proto.RunEvent

	res, err := server.runMicroVM(context.Background(), &proto.RunRequest{Source: "../ubuntu"}, func(event *proto.RunEvent) error {
		events = append(events, event)
		return nil
	})
	if err != nil {
		t.Fatalf("runMicroVM returned transport error: %v", err)
	}
	if res.GetStatus() != "failed" {
		t.Fatalf("expected failed response, got %s", res.GetStatus())
	}
	if len(events) != 1 {
		t.Fatalf("expected one event, got %d", len(events))
	}
	if events[0].GetStatus() != "error" || events[0].GetStage() != "validate" {
		t.Fatalf("unexpected event: %#v", events[0])
	}
}

func TestReadTailLines(t *testing.T) {
	path := filepath.Join(t.TempDir(), "vm.log")
	if err := os.WriteFile(path, []byte("one\ntwo\nthree\n"), 0644); err != nil {
		t.Fatalf("failed to write test log: %v", err)
	}

	lines, offset, err := readTailLines(path, 2)
	if err != nil {
		t.Fatalf("readTailLines failed: %v", err)
	}
	if offset != int64(len("one\ntwo\nthree\n")) {
		t.Fatalf("unexpected offset %d", offset)
	}
	if got := strings.Join(lines, ","); got != "two,three" {
		t.Fatalf("unexpected tail lines: %s", got)
	}
}

func TestLogPathForVM(t *testing.T) {
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("failed to open db: %v", err)
	}
	defer db.Close()
	if err := ensureSchema(db); err != nil {
		t.Fatalf("ensureSchema failed: %v", err)
	}
	if _, err := db.Exec(`INSERT INTO vms (id, status, image, log_path) VALUES (?, ?, ?, ?)`, "vm-1", "running", "debian", "/tmp/vm-1.log"); err != nil {
		t.Fatalf("failed to insert vm: %v", err)
	}

	server := Server{DB: db}
	logPath, err := server.logPathForVM("vm-1")
	if err != nil {
		t.Fatalf("logPathForVM failed: %v", err)
	}
	if logPath != "/tmp/vm-1.log" {
		t.Fatalf("unexpected log path: %s", logPath)
	}
	if _, err := server.logPathForVM("missing"); err == nil {
		t.Fatal("expected missing vm to return an error")
	}
}

func TestEnsureSchemaAddsDrivePath(t *testing.T) {
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("failed to open db: %v", err)
	}
	defer db.Close()
	if err := ensureSchema(db); err != nil {
		t.Fatalf("ensureSchema failed: %v", err)
	}

	if _, err := db.Exec(`INSERT INTO vms (id, status, image, drive_path) VALUES (?, ?, ?, ?)`, "vm-drive", "stopped", "ubuntu", "/tmp/vm-drive.ext4"); err != nil {
		t.Fatalf("expected drive_path column to exist: %v", err)
	}
}

func TestEnsureSchemaAddsConsolePath(t *testing.T) {
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("failed to open db: %v", err)
	}
	defer db.Close()
	if err := ensureSchema(db); err != nil {
		t.Fatalf("ensureSchema failed: %v", err)
	}

	if _, err := db.Exec(`INSERT INTO vms (id, status, image, console_path) VALUES (?, ?, ?, ?)`, "vm-console", "running", "ubuntu", "/tmp/vm-console.in"); err != nil {
		t.Fatalf("expected console_path column to exist: %v", err)
	}
}

func TestConsoleInfo(t *testing.T) {
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("failed to open db: %v", err)
	}
	defer db.Close()
	if err := ensureSchema(db); err != nil {
		t.Fatalf("ensureSchema failed: %v", err)
	}
	dir := t.TempDir()
	logPath := filepath.Join(dir, "vm.log")
	inputPath := filepath.Join(dir, "vm.in")
	if err := os.WriteFile(logPath, []byte("boot\n"), 0600); err != nil {
		t.Fatalf("failed to create log file: %v", err)
	}
	if err := os.WriteFile(inputPath, []byte{}, 0600); err != nil {
		t.Fatalf("failed to create input file: %v", err)
	}
	if _, err := db.Exec(`INSERT INTO vms (id, status, image, log_path, console_path) VALUES (?, ?, ?, ?, ?)`, "vm-console", "running", "ubuntu", logPath, inputPath); err != nil {
		t.Fatalf("failed to insert vm: %v", err)
	}

	server := Server{DB: db}
	res, err := server.ConsoleInfo(context.Background(), &proto.ConsoleInfoRequest{VmId: "vm-console"})
	if err != nil {
		t.Fatalf("ConsoleInfo returned transport error: %v", err)
	}
	if !res.Success || res.LogPath != logPath || res.InputPath != inputPath {
		t.Fatalf("unexpected console response: %#v", res)
	}
}

func TestInspect(t *testing.T) {
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("failed to open db: %v", err)
	}
	defer db.Close()
	if err := ensureSchema(db); err != nil {
		t.Fatalf("ensureSchema failed: %v", err)
	}
	_, err = db.Exec(`INSERT INTO vms (id, pid, status, image, cpus, memory_mb, log_path, drive_path, console_path, tap_name, guest_ip, mac_address) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		"vm-inspect", 123, "running", "ubuntu", 2, 1024, "/tmp/vm.log", "/tmp/vm.ext4", "/tmp/vm.in", "fvc123", "172.16.0.2", "02:FC:00:00:00:01")
	if err != nil {
		t.Fatalf("failed to insert vm: %v", err)
	}

	server := Server{DB: db}
	res, err := server.Inspect(context.Background(), &proto.InspectRequest{VmId: "vm-inspect"})
	if err != nil {
		t.Fatalf("Inspect returned transport error: %v", err)
	}
	if !res.Success {
		t.Fatalf("expected inspect success: %s", res.Message)
	}
	if res.Vm.GetGuestIp() != "172.16.0.2" || res.Vm.GetTapName() != "fvc123" || res.Vm.GetLogPath() != "/tmp/vm.log" {
		t.Fatalf("unexpected inspect vm: %#v", res.Vm)
	}
}

func TestInspectHidesNetworkForStoppedVM(t *testing.T) {
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("failed to open db: %v", err)
	}
	defer db.Close()
	if err := ensureSchema(db); err != nil {
		t.Fatalf("ensureSchema failed: %v", err)
	}
	_, err = db.Exec(`INSERT INTO vms (id, pid, status, image, tap_name, guest_ip, mac_address) VALUES (?, ?, ?, ?, ?, ?, ?)`,
		"vm-stopped", 0, "stopped", "ubuntu", "fvc123", "172.16.0.2", "02:FC:00:00:00:01")
	if err != nil {
		t.Fatalf("failed to insert vm: %v", err)
	}

	server := Server{DB: db}
	res, err := server.Inspect(context.Background(), &proto.InspectRequest{VmId: "vm-stopped"})
	if err != nil {
		t.Fatalf("Inspect returned transport error: %v", err)
	}
	if res.Vm.GetGuestIp() != "" || res.Vm.GetTapName() != "" || res.Vm.GetMacAddress() != "" {
		t.Fatalf("expected stopped network fields to be hidden, got %#v", res.Vm)
	}
}

func TestEnsureSchemaAddsNetworkColumns(t *testing.T) {
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("failed to open db: %v", err)
	}
	defer db.Close()
	if err := ensureSchema(db); err != nil {
		t.Fatalf("ensureSchema failed: %v", err)
	}

	_, err = db.Exec(`INSERT INTO vms (id, status, image, tap_name, guest_ip, mac_address) VALUES (?, ?, ?, ?, ?, ?)`, "vm-net", "stopped", "ubuntu", "fvc123", "172.16.0.2", "02:FC:00:00:00:01")
	if err != nil {
		t.Fatalf("expected network columns to exist: %v", err)
	}
}

func TestRuntimePathUsesRuntimeDir(t *testing.T) {
	server := Server{Config: DaemonConfig{RuntimeDir: "/run/fvc"}}
	got := server.runtimePath("fvc-test.socket")
	if got != filepath.Join("/run/fvc", "fvc-test.socket") {
		t.Fatalf("unexpected runtime path: %s", got)
	}
}

func TestPruneOrphanActiveDrives(t *testing.T) {
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("failed to open db: %v", err)
	}
	defer db.Close()
	if err := ensureSchema(db); err != nil {
		t.Fatalf("ensureSchema failed: %v", err)
	}

	activeDir := t.TempDir()
	knownPath := filepath.Join(activeDir, "known.ext4")
	orphanPath := filepath.Join(activeDir, "orphan.ext4")
	if err := os.WriteFile(knownPath, []byte("known"), 0644); err != nil {
		t.Fatalf("failed to write known drive: %v", err)
	}
	if err := os.WriteFile(orphanPath, []byte("orphan"), 0644); err != nil {
		t.Fatalf("failed to write orphan drive: %v", err)
	}
	if _, err := db.Exec(`INSERT INTO vms (id, status, image, drive_path) VALUES (?, ?, ?, ?)`, "vm-known", "stopped", "ubuntu", knownPath); err != nil {
		t.Fatalf("failed to insert vm: %v", err)
	}

	server := Server{DB: db, Config: DaemonConfig{ActiveDir: activeDir}}
	result, err := server.pruneOrphanActiveDrives(false)
	if err != nil {
		t.Fatalf("pruneOrphanActiveDrives failed: %v", err)
	}
	if result.RemovedFiles != 1 || result.FreedBytes != int64(len("orphan")) {
		t.Fatalf("unexpected prune result: %#v", result)
	}
	if _, err := os.Stat(knownPath); err != nil {
		t.Fatalf("expected known drive to stay: %v", err)
	}
	if _, err := os.Stat(orphanPath); !os.IsNotExist(err) {
		t.Fatalf("expected orphan drive removed, got err=%v", err)
	}
}

func TestPruneRuntimeFilesPreservesRunningVMFiles(t *testing.T) {
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("failed to open db: %v", err)
	}
	defer db.Close()
	if err := ensureSchema(db); err != nil {
		t.Fatalf("ensureSchema failed: %v", err)
	}

	runtimeDir := t.TempDir()
	socketPath := filepath.Join(runtimeDir, "fvc-vm-running.socket")
	consolePath := filepath.Join(runtimeDir, "fvc-vm-running.console.in")
	orphanPath := filepath.Join(runtimeDir, "fvc-orphan.socket")
	for path, contents := range map[string]string{
		socketPath:  "socket",
		consolePath: "console",
		orphanPath:  "orphan",
	} {
		if err := os.WriteFile(path, []byte(contents), 0644); err != nil {
			t.Fatalf("failed to write runtime file %s: %v", path, err)
		}
	}
	if _, err := db.Exec(`INSERT INTO vms (id, status, image, console_path) VALUES (?, ?, ?, ?)`, "vm-running", "running", "ubuntu", consolePath); err != nil {
		t.Fatalf("failed to insert running vm: %v", err)
	}

	server := Server{DB: db, Config: DaemonConfig{RuntimeDir: runtimeDir}}
	result, err := server.pruneRuntimeFiles(false)
	if err != nil {
		t.Fatalf("pruneRuntimeFiles failed: %v", err)
	}
	if result.RemovedFiles != 1 || result.FreedBytes != int64(len("orphan")) {
		t.Fatalf("unexpected prune result: %#v", result)
	}
	for _, path := range []string{socketPath, consolePath} {
		if _, err := os.Stat(path); err != nil {
			t.Fatalf("expected active runtime file to stay: %s err=%v", path, err)
		}
	}
	if _, err := os.Stat(orphanPath); !os.IsNotExist(err) {
		t.Fatalf("expected orphan runtime file removed, got err=%v", err)
	}
}

func TestPruneRuntimeFilesDryRunKeepsOrphan(t *testing.T) {
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("failed to open db: %v", err)
	}
	defer db.Close()
	if err := ensureSchema(db); err != nil {
		t.Fatalf("ensureSchema failed: %v", err)
	}

	runtimeDir := t.TempDir()
	orphanPath := filepath.Join(runtimeDir, "fvc-orphan.socket")
	if err := os.WriteFile(orphanPath, []byte("orphan"), 0644); err != nil {
		t.Fatalf("failed to write orphan runtime file: %v", err)
	}

	server := Server{DB: db, Config: DaemonConfig{RuntimeDir: runtimeDir}}
	result, err := server.pruneRuntimeFiles(true)
	if err != nil {
		t.Fatalf("pruneRuntimeFiles dry-run failed: %v", err)
	}
	if result.RemovedFiles != 1 || len(result.Items) != 1 {
		t.Fatalf("unexpected dry-run result: %#v", result)
	}
	if _, err := os.Stat(orphanPath); err != nil {
		t.Fatalf("expected orphan runtime file to remain: %v", err)
	}
}

func TestStatsReturnsStoppedVM(t *testing.T) {
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("failed to open db: %v", err)
	}
	defer db.Close()
	if err := ensureSchema(db); err != nil {
		t.Fatalf("ensureSchema failed: %v", err)
	}
	if _, err := db.Exec(`INSERT INTO vms (id, pid, status, image, memory_mb) VALUES (?, ?, ?, ?, ?)`, "vm-stats", 0, "stopped", "ubuntu", 1024); err != nil {
		t.Fatalf("failed to insert vm: %v", err)
	}

	server := Server{DB: db}
	res, err := server.Stats(context.Background(), &proto.StatsRequest{VmId: "vm-stats"})
	if err != nil {
		t.Fatalf("Stats failed: %v", err)
	}
	if len(res.Stats) != 1 {
		t.Fatalf("expected one stats row, got %d", len(res.Stats))
	}
	if res.Stats[0].VmId != "vm-stats" || res.Stats[0].MemoryMb != 1024 || res.Stats[0].Status != "stopped" {
		t.Fatalf("unexpected stats row: %#v", res.Stats[0])
	}
}

func TestWaitReturnsImmediatelyForStoppedVM(t *testing.T) {
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("failed to open db: %v", err)
	}
	defer db.Close()
	if err := ensureSchema(db); err != nil {
		t.Fatalf("ensureSchema failed: %v", err)
	}
	if _, err := db.Exec(`INSERT INTO vms (id, status, image) VALUES (?, ?, ?)`, "vm-wait", "stopped", "ubuntu"); err != nil {
		t.Fatalf("failed to insert vm: %v", err)
	}

	server := Server{DB: db}
	res, err := server.Wait(context.Background(), &proto.WaitRequest{VmId: "vm-wait", TimeoutSeconds: 1})
	if err != nil {
		t.Fatalf("Wait failed: %v", err)
	}
	if !res.Success || res.Status != "stopped" {
		t.Fatalf("unexpected wait response: %#v", res)
	}
}

func TestSnapshotCreateRequiresStoppedVM(t *testing.T) {
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("failed to open db: %v", err)
	}
	defer db.Close()
	if err := ensureSchema(db); err != nil {
		t.Fatalf("ensureSchema failed: %v", err)
	}
	dir := t.TempDir()
	drivePath := filepath.Join(dir, "vm.ext4")
	if err := os.WriteFile(drivePath, []byte("drive"), 0644); err != nil {
		t.Fatalf("failed to seed drive: %v", err)
	}
	if _, err := db.Exec(`INSERT INTO vms (id, status, image, drive_path) VALUES (?, ?, ?, ?)`, "vm-running", "running", "ubuntu", drivePath); err != nil {
		t.Fatalf("failed to insert vm: %v", err)
	}

	store := NewImageStore(DaemonConfig{BaseDir: dir, CacheDir: filepath.Join(dir, "cache"), ActiveDir: filepath.Join(dir, "active"), SnapshotDir: filepath.Join(dir, "snapshots")})
	if err := store.Init(); err != nil {
		t.Fatalf("store init failed: %v", err)
	}
	server := Server{DB: db, Store: store}
	res, err := server.SnapshotCreate(context.Background(), &proto.SnapshotCreateRequest{VmId: "vm-running", Name: "clean"})
	if err != nil {
		t.Fatalf("SnapshotCreate returned transport error: %v", err)
	}
	if res.Success || !strings.Contains(res.Message, "stopped") {
		t.Fatalf("expected running vm snapshot rejection, got %#v", res)
	}
}

func TestSnapshotCreateListRestoreRemove(t *testing.T) {
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("failed to open db: %v", err)
	}
	defer db.Close()
	if err := ensureSchema(db); err != nil {
		t.Fatalf("ensureSchema failed: %v", err)
	}
	dir := t.TempDir()
	store := NewImageStore(DaemonConfig{BaseDir: dir, CacheDir: filepath.Join(dir, "cache"), ActiveDir: filepath.Join(dir, "active"), SnapshotDir: filepath.Join(dir, "snapshots")})
	if err := store.Init(); err != nil {
		t.Fatalf("store init failed: %v", err)
	}
	drivePath := filepath.Join(store.activeDir, "vm-snap.ext4")
	if err := os.WriteFile(drivePath, []byte("before"), 0644); err != nil {
		t.Fatalf("failed to seed drive: %v", err)
	}
	if _, err := db.Exec(`INSERT INTO vms (id, status, image, drive_path) VALUES (?, ?, ?, ?)`, "vm-snap", "stopped", "ubuntu", drivePath); err != nil {
		t.Fatalf("failed to insert vm: %v", err)
	}

	server := Server{DB: db, Store: store}
	createRes, err := server.SnapshotCreate(context.Background(), &proto.SnapshotCreateRequest{VmId: "vm-snap", Name: "clean"})
	if err != nil {
		t.Fatalf("SnapshotCreate returned transport error: %v", err)
	}
	if !createRes.Success || createRes.Snapshot.GetName() != "clean" {
		t.Fatalf("unexpected create response: %#v", createRes)
	}

	listRes, err := server.SnapshotList(context.Background(), &proto.SnapshotListRequest{VmId: "vm-snap"})
	if err != nil {
		t.Fatalf("SnapshotList returned transport error: %v", err)
	}
	if !listRes.Success || len(listRes.Snapshots) != 1 {
		t.Fatalf("unexpected list response: %#v", listRes)
	}

	if err := os.WriteFile(drivePath, []byte("after"), 0644); err != nil {
		t.Fatalf("failed to mutate drive: %v", err)
	}
	restoreRes, err := server.SnapshotRestore(context.Background(), &proto.SnapshotRestoreRequest{VmId: "vm-snap", Name: "clean"})
	if err != nil {
		t.Fatalf("SnapshotRestore returned transport error: %v", err)
	}
	if !restoreRes.Success {
		t.Fatalf("unexpected restore response: %#v", restoreRes)
	}
	if got, err := os.ReadFile(drivePath); err != nil || string(got) != "before" {
		t.Fatalf("unexpected restored drive contents %q err=%v", got, err)
	}

	rmRes, err := server.SnapshotRemove(context.Background(), &proto.SnapshotRemoveRequest{VmId: "vm-snap", Name: "clean"})
	if err != nil {
		t.Fatalf("SnapshotRemove returned transport error: %v", err)
	}
	if !rmRes.Success {
		t.Fatalf("unexpected remove response: %#v", rmRes)
	}
}

func TestImageRemoveProtectsUsedImage(t *testing.T) {
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("failed to open db: %v", err)
	}
	defer db.Close()
	if err := ensureSchema(db); err != nil {
		t.Fatalf("ensureSchema failed: %v", err)
	}
	dir := t.TempDir()
	store := NewImageStore(DaemonConfig{BaseDir: dir, CacheDir: filepath.Join(dir, "cache"), ActiveDir: filepath.Join(dir, "active"), SnapshotDir: filepath.Join(dir, "snapshots")})
	if err := store.Init(); err != nil {
		t.Fatalf("store init failed: %v", err)
	}
	if err := os.WriteFile(store.cachedImagePath("ubuntu"), []byte("rootfs"), 0644); err != nil {
		t.Fatalf("failed to seed image: %v", err)
	}
	if _, err := db.Exec(`INSERT INTO vms (id, status, image) VALUES (?, ?, ?)`, "vm-used", "stopped", "ubuntu"); err != nil {
		t.Fatalf("failed to insert vm: %v", err)
	}

	server := Server{DB: db, Store: store}
	res, err := server.ImageRemove(context.Background(), &proto.ImageRemoveRequest{Image: "ubuntu"})
	if err != nil {
		t.Fatalf("ImageRemove returned transport error: %v", err)
	}
	if res.Success || !strings.Contains(res.Message, "used") {
		t.Fatalf("expected image usage protection, got %#v", res)
	}
}

func TestImagePruneKeepsUsedImage(t *testing.T) {
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("failed to open db: %v", err)
	}
	defer db.Close()
	if err := ensureSchema(db); err != nil {
		t.Fatalf("ensureSchema failed: %v", err)
	}
	dir := t.TempDir()
	store := NewImageStore(DaemonConfig{BaseDir: dir, CacheDir: filepath.Join(dir, "cache"), ActiveDir: filepath.Join(dir, "active"), SnapshotDir: filepath.Join(dir, "snapshots")})
	if err := store.Init(); err != nil {
		t.Fatalf("store init failed: %v", err)
	}
	if err := os.WriteFile(store.cachedImagePath("used"), []byte("used"), 0644); err != nil {
		t.Fatalf("failed to seed used image: %v", err)
	}
	if err := os.WriteFile(store.cachedImagePath("unused"), []byte("unused"), 0644); err != nil {
		t.Fatalf("failed to seed unused image: %v", err)
	}
	if _, err := db.Exec(`INSERT INTO vms (id, status, image) VALUES (?, ?, ?)`, "vm-used", "stopped", "used"); err != nil {
		t.Fatalf("failed to insert vm: %v", err)
	}

	server := Server{DB: db, Store: store}
	res, err := server.ImagePrune(context.Background(), &proto.ImagePruneRequest{})
	if err != nil {
		t.Fatalf("ImagePrune returned transport error: %v", err)
	}
	if !res.Success || res.RemovedImages != 1 {
		t.Fatalf("unexpected prune response: %#v", res)
	}
	if _, err := store.InspectImage("used"); err != nil {
		t.Fatalf("expected used image to remain: %v", err)
	}
	if _, err := store.InspectImage("unused"); err == nil {
		t.Fatal("expected unused image to be removed")
	}
}
