package main

import (
	"errors"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/lucaspose/fvc/proto"
)

func TestExecuteRunSendsConfigAndPorts(t *testing.T) {
	client := &fakeFvcClient{
		runEvents: []*proto.RunEvent{{Status: "complete", VmId: "vm-1"}},
	}
	contextDir := t.TempDir()

	_ = captureStdout(t, func() {
		err := executeRun(client, []string{"--image", "nginx-fvc:latest", "--name", "web", "--cpu", "2", "--ram", "1024", "-p", "8080:80", "--publish-all", contextDir})
		if err != nil {
			t.Fatalf("executeRun failed: %v", err)
		}
	})

	if client.runReq == nil {
		t.Fatal("expected RunStream to be called")
	}
	if client.runReq.GetSource() != "nginx-fvc:latest" || client.runReq.GetName() != "web" {
		t.Fatalf("unexpected run request identity: %#v", client.runReq)
	}
	config := client.runReq.GetConfig()
	if config.GetCpus() != 2 || config.GetMemoryMb() != 1024 || !config.GetPublishAll() {
		t.Fatalf("unexpected run config: %#v", config)
	}
	if want := []string{"8080:80"}; !reflect.DeepEqual(config.GetPorts(), want) {
		t.Fatalf("unexpected ports: %#v", config.GetPorts())
	}
}

func TestExecuteRunReturnsStreamError(t *testing.T) {
	client := &fakeFvcClient{
		runEvents: []*proto.RunEvent{{Stage: "image", Status: "error", ErrorMessage: "image missing"}},
	}

	err := executeRun(client, []string{"--image", "ubuntu", t.TempDir()})
	if err == nil || !strings.Contains(err.Error(), "microVM start failed: image missing") {
		t.Fatalf("expected stream error, got %v", err)
	}
}

func TestExecuteBuildSendsAbsoluteContextAndTag(t *testing.T) {
	client := &fakeFvcClient{
		buildEvents: []*proto.OperationEvent{{Stage: "done", Status: "complete", Image: "web:dev", Path: "/cache/web.ext4"}},
	}
	contextDir := t.TempDir()

	_ = captureStdout(t, func() {
		if err := executeBuild(client, []string{"-t", "web:dev", contextDir}); err != nil {
			t.Fatalf("executeBuild failed: %v", err)
		}
	})

	if client.buildReq == nil {
		t.Fatal("expected BuildImageStream to be called")
	}
	if client.buildReq.GetContextPath() != contextDir || client.buildReq.GetTag() != "web:dev" {
		t.Fatalf("unexpected build request: %#v", client.buildReq)
	}
}

func TestExecutePullDockerSendsTarget(t *testing.T) {
	client := &fakeFvcClient{
		pullEvents: []*proto.OperationEvent{{Stage: "done", Status: "complete", Path: "/cache/hello.ext4"}},
	}

	_ = captureStdout(t, func() {
		if err := executePull(client, []string{"--from", "docker", "hello-world:latest", "-t", "hello-fvc:latest"}); err != nil {
			t.Fatalf("executePull failed: %v", err)
		}
	})

	if client.pullReq == nil {
		t.Fatal("expected PullImageStream to be called")
	}
	if client.pullReq.GetImage() != "hello-world:latest" || client.pullReq.GetSource() != "docker" || client.pullReq.GetTarget() != "hello-fvc:latest" {
		t.Fatalf("unexpected pull request: %#v", client.pullReq)
	}
}

func TestExecutePullReturnsStreamError(t *testing.T) {
	client := &fakeFvcClient{
		pullEvents: []*proto.OperationEvent{{Stage: "docker", Status: "error", ErrorMessage: "manifest denied"}},
	}

	err := executePull(client, []string{"--from", "docker", "private/image:latest"})
	if err == nil || !strings.Contains(err.Error(), "pull failed: manifest denied") {
		t.Fatalf("expected pull stream error, got %v", err)
	}
}

func TestExecuteLifecycleCommandsSendRequests(t *testing.T) {
	client := &fakeFvcClient{}

	_ = captureStdout(t, func() {
		if err := executeStop(client, []string{"--timeout", "3", "vm-1"}); err != nil {
			t.Fatalf("executeStop failed: %v", err)
		}
		if err := executeStart(client, []string{"vm-2"}); err != nil {
			t.Fatalf("executeStart failed: %v", err)
		}
		if err := executeRm(client, []string{"vm-3"}); err != nil {
			t.Fatalf("executeRm failed: %v", err)
		}
		if err := executeKill(client, []string{"vm-4"}); err != nil {
			t.Fatalf("executeKill failed: %v", err)
		}
	})

	if client.stopReq.GetVmId() != "vm-1" || client.stopReq.GetTimeoutSeconds() != 3 {
		t.Fatalf("unexpected stop request: %#v", client.stopReq)
	}
	if client.startReq.GetVmId() != "vm-2" {
		t.Fatalf("unexpected start request: %#v", client.startReq)
	}
	if client.rmReq.GetVmId() != "vm-3" {
		t.Fatalf("unexpected rm request: %#v", client.rmReq)
	}
	if client.killReq.GetVmId() != "vm-4" {
		t.Fatalf("unexpected kill request: %#v", client.killReq)
	}
}

func TestExecuteQueryCommandsSendRequests(t *testing.T) {
	client := &fakeFvcClient{}

	_ = captureStdout(t, func() {
		if err := executePs(client, []string{"--all"}); err != nil {
			t.Fatalf("executePs failed: %v", err)
		}
		if err := executeInspect(client, []string{"vm-inspect"}); err != nil {
			t.Fatalf("executeInspect failed: %v", err)
		}
		if err := executeStats(client, []string{"--interval", "1s", "vm-stats"}); err != nil {
			t.Fatalf("executeStats failed: %v", err)
		}
		if err := executeWait(client, []string{"--timeout", "8", "vm-wait"}); err != nil {
			t.Fatalf("executeWait failed: %v", err)
		}
	})

	if client.psReq == nil || !client.psReq.GetAll() {
		t.Fatalf("unexpected ps request: %#v", client.psReq)
	}
	if client.inspectReq.GetVmId() != "vm-inspect" {
		t.Fatalf("unexpected inspect request: %#v", client.inspectReq)
	}
	if client.statsReq.GetVmId() != "vm-stats" {
		t.Fatalf("unexpected stats request: %#v", client.statsReq)
	}
	if client.waitReq.GetVmId() != "vm-wait" || client.waitReq.GetTimeoutSeconds() != 8 {
		t.Fatalf("unexpected wait request: %#v", client.waitReq)
	}
}

func TestExecuteMaintenanceCommandsSendRequests(t *testing.T) {
	client := &fakeFvcClient{}

	_ = captureStdout(t, func() {
		if err := executeImages(client, nil); err != nil {
			t.Fatalf("executeImages failed: %v", err)
		}
		if err := executePrune(client, []string{"--dry-run"}); err != nil {
			t.Fatalf("executePrune dry-run failed: %v", err)
		}
		if err := executePrune(client, []string{"--force"}); err != nil {
			t.Fatalf("executePrune force failed: %v", err)
		}
	})

	if client.listImagesReq == nil {
		t.Fatal("expected ListImages to be called")
	}
	if client.pruneReq == nil {
		t.Fatal("expected Prune to be called")
	}
	if client.pruneReq.GetDryRun() {
		t.Fatalf("expected last prune call to be destructive because --force was used, got %#v", client.pruneReq)
	}
}

func TestExecuteLogsSendsTailAndFollow(t *testing.T) {
	client := &fakeFvcClient{
		logsEvents: []*proto.LogsResponse{{Line: "one"}, {Line: "two"}},
	}

	output := captureStdout(t, func() {
		if err := executeLogs(client, []string{"--tail", "2", "--follow", "vm-1"}); err != nil {
			t.Fatalf("executeLogs failed: %v", err)
		}
	})

	if client.logsReq == nil {
		t.Fatal("expected StreamLogs to be called")
	}
	if client.logsReq.GetVmId() != "vm-1" || client.logsReq.GetTail() != 2 || !client.logsReq.GetFollow() {
		t.Fatalf("unexpected logs request: %#v", client.logsReq)
	}
	if !strings.Contains(output, "one\ntwo") {
		t.Fatalf("unexpected logs output: %q", output)
	}
}

func TestExecuteLogsRejectsNegativeTailBeforeDaemonCall(t *testing.T) {
	client := &fakeFvcClient{}

	err := executeLogs(client, []string{"--tail", "-1", "vm-1"})
	if err == nil || !strings.Contains(err.Error(), "tail must be zero or greater") {
		t.Fatalf("expected tail validation error, got %v", err)
	}
	if client.logsReq != nil {
		t.Fatalf("StreamLogs should not be called, got %#v", client.logsReq)
	}
}

func TestExecuteExecSendsCommandEnvironmentAndWorkdir(t *testing.T) {
	client := &fakeFvcClient{
		execEvents: []*proto.ExecEvent{
			{Stream: "stdout", Data: []byte("ok\n")},
			{Stream: "exit", ExitCode: 0},
		},
	}

	output := captureStdout(t, func() {
		err := executeExec(client, []string{"--env", "A=B", "-e", "C=D", "--workdir", "/app", "vm-1", "--", "/bin/sh", "-lc", "echo ok"})
		if err != nil {
			t.Fatalf("executeExec failed: %v", err)
		}
	})

	if client.execReq == nil {
		t.Fatal("expected Exec to be called")
	}
	if client.execReq.GetVmId() != "vm-1" || client.execReq.GetWorkdir() != "/app" {
		t.Fatalf("unexpected exec request: %#v", client.execReq)
	}
	if want := []string{"/bin/sh", "-lc", "echo ok"}; !reflect.DeepEqual(client.execReq.GetCommand(), want) {
		t.Fatalf("unexpected command: %#v", client.execReq.GetCommand())
	}
	if want := []string{"A=B", "C=D"}; !reflect.DeepEqual(client.execReq.GetEnv(), want) {
		t.Fatalf("unexpected env: %#v", client.execReq.GetEnv())
	}
	if output != "ok\n" {
		t.Fatalf("unexpected stdout: %q", output)
	}
}

func TestExecuteExecReturnsStreamExitCode(t *testing.T) {
	client := &fakeFvcClient{
		execEvents: []*proto.ExecEvent{{Stream: "exit", ExitCode: 7}},
	}

	err := executeExec(client, []string{"vm-1", "--", "false"})
	var exitErr cliExitError
	if !errors.As(err, &exitErr) || exitErr.code != 7 {
		t.Fatalf("expected cli exit code 7, got %T %v", err, err)
	}
}

func TestExecuteSnapshotCreateSendsRequest(t *testing.T) {
	client := &fakeFvcClient{}

	_ = captureStdout(t, func() {
		if err := executeSnapshotCreate(client, []string{"vm-1", "clean"}); err != nil {
			t.Fatalf("executeSnapshotCreate failed: %v", err)
		}
	})

	if client.snapshotCreate == nil {
		t.Fatal("expected SnapshotCreate to be called")
	}
	if client.snapshotCreate.GetVmId() != "vm-1" || client.snapshotCreate.GetName() != "clean" {
		t.Fatalf("unexpected snapshot request: %#v", client.snapshotCreate)
	}
}

func TestExecuteImageRemoveSendsForceFlag(t *testing.T) {
	client := &fakeFvcClient{}

	if err := executeImageRemove(client, []string{"--force", "ubuntu"}); err != nil {
		t.Fatalf("executeImageRemove failed: %v", err)
	}

	if client.imageRemove == nil {
		t.Fatal("expected ImageRemove to be called")
	}
	if client.imageRemove.GetImage() != "ubuntu" || !client.imageRemove.GetForce() {
		t.Fatalf("unexpected image remove request: %#v", client.imageRemove)
	}
}

func TestExecuteImageImportAndExportUseAbsolutePaths(t *testing.T) {
	client := &fakeFvcClient{}
	dir := t.TempDir()
	source := filepath.Join(dir, "rootfs.ext4")
	dest := filepath.Join(dir, "out.ext4")

	_ = captureStdout(t, func() {
		if err := executeImageImport(client, []string{source, "custom"}); err != nil {
			t.Fatalf("executeImageImport failed: %v", err)
		}
		if err := executeImageExport(client, []string{"custom", dest}); err != nil {
			t.Fatalf("executeImageExport failed: %v", err)
		}
	})

	if client.imageImport == nil || client.imageExport == nil {
		t.Fatalf("expected import and export calls, got import=%#v export=%#v", client.imageImport, client.imageExport)
	}
	if client.imageImport.GetSourcePath() != source || client.imageImport.GetImage() != "custom" {
		t.Fatalf("unexpected image import request: %#v", client.imageImport)
	}
	if client.imageExport.GetDestPath() != dest || client.imageExport.GetImage() != "custom" {
		t.Fatalf("unexpected image export request: %#v", client.imageExport)
	}
}
