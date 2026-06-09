package main

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestAgentRunsCommandInRootWorkdir(t *testing.T) {
	root := t.TempDir()
	plan := AgentPlan{
		Root: root,
		Env:  []string{"VALUE=hello"},
		Runs: []RunStep{{
			Command: "printf %s \"$VALUE\" > value.txt",
			Workdir: "/app",
		}},
	}
	data, err := json.Marshal(plan)
	if err != nil {
		t.Fatalf("marshal failed: %v", err)
	}

	var stdout, stderr bytes.Buffer
	if err := run(bytes.NewReader(data), &stdout, &stderr); err != nil {
		t.Fatalf("run failed: %v stderr=%s", err, stderr.String())
	}
	got, err := os.ReadFile(filepath.Join(root, "app", "value.txt"))
	if err != nil {
		t.Fatalf("failed to read generated file: %v", err)
	}
	if string(got) != "hello" {
		t.Fatalf("unexpected file content: %q", got)
	}
	if !strings.Contains(stdout.String(), "[RUN]") {
		t.Fatalf("expected run log, got %q", stdout.String())
	}
}

func TestAgentRejectsRelativeWorkdir(t *testing.T) {
	plan := AgentPlan{
		Root: t.TempDir(),
		Runs: []RunStep{{Command: "true", Workdir: "relative"}},
	}
	data, err := json.Marshal(plan)
	if err != nil {
		t.Fatalf("marshal failed: %v", err)
	}
	if err := run(bytes.NewReader(data), &bytes.Buffer{}, &bytes.Buffer{}); err == nil {
		t.Fatal("expected relative workdir to be rejected")
	}
}

func TestAgentServeBuildEndpoint(t *testing.T) {
	t.Setenv("FVC_BUILD_AGENT_TOKEN", "secret")
	root := t.TempDir()
	var stdout, stderr bytes.Buffer
	done := make(chan error, 1)
	go func() {
		done <- serve([]string{"--addr", "127.0.0.1:19090"}, &stdout, &stderr)
	}()
	for i := 0; i < 40; i++ {
		resp, err := http.Get("http://127.0.0.1:19090/healthz")
		if err == nil {
			_ = resp.Body.Close()
			break
		}
		time.Sleep(25 * time.Millisecond)
	}
	plan := AgentPlan{
		Root: root,
		Runs: []RunStep{{Command: "printf ok > served.txt"}},
	}
	data, err := json.Marshal(plan)
	if err != nil {
		t.Fatalf("marshal failed: %v", err)
	}
	req, err := http.NewRequest(http.MethodPost, "http://127.0.0.1:19090/build", bytes.NewReader(data))
	if err != nil {
		t.Fatalf("request build failed: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-FVC-Build-Token", "secret")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("post failed: %v", err)
	}
	body, _ := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("unexpected status %s body=%s stderr=%s", resp.Status, string(body), stderr.String())
	}
	got, err := os.ReadFile(filepath.Join(root, "served.txt"))
	if err != nil {
		t.Fatalf("failed to read generated file: %v", err)
	}
	if string(got) != "ok" {
		t.Fatalf("unexpected file content: %q", got)
	}
	select {
	case err := <-done:
		t.Fatalf("server exited unexpectedly: %v", err)
	default:
	}
}

func TestAgentServeBuildEndpointReturnsErrorStatus(t *testing.T) {
	t.Setenv("FVC_BUILD_AGENT_TOKEN", "secret")
	root := t.TempDir()
	var stdout, stderr bytes.Buffer
	done := make(chan error, 1)
	go func() {
		done <- serve([]string{"--addr", "127.0.0.1:19091"}, &stdout, &stderr)
	}()
	for i := 0; i < 40; i++ {
		resp, err := http.Get("http://127.0.0.1:19091/healthz")
		if err == nil {
			_ = resp.Body.Close()
			break
		}
		time.Sleep(25 * time.Millisecond)
	}
	plan := AgentPlan{
		Root: root,
		Runs: []RunStep{{Command: "exit 7"}},
	}
	data, err := json.Marshal(plan)
	if err != nil {
		t.Fatalf("marshal failed: %v", err)
	}
	req, err := http.NewRequest(http.MethodPost, "http://127.0.0.1:19091/build", bytes.NewReader(data))
	if err != nil {
		t.Fatalf("request build failed: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-FVC-Build-Token", "secret")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("post failed: %v", err)
	}
	body, _ := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusInternalServerError {
		t.Fatalf("unexpected status %s body=%s stderr=%s", resp.Status, string(body), stderr.String())
	}
	if !strings.Contains(string(body), "run failed") {
		t.Fatalf("expected run failure body, got %s", string(body))
	}
	select {
	case err := <-done:
		t.Fatalf("server exited unexpectedly: %v", err)
	default:
	}
}

func TestAuthorizeBuildRequestRequiresToken(t *testing.T) {
	t.Setenv("FVC_BUILD_AGENT_TOKEN", "")
	req, err := http.NewRequest(http.MethodPost, "http://127.0.0.1/build", nil)
	if err != nil {
		t.Fatalf("request build failed: %v", err)
	}
	if err := authorizeBuildRequest(req); err == nil || !strings.Contains(err.Error(), "FVC_BUILD_AGENT_TOKEN") {
		t.Fatalf("expected missing token config error, got %v", err)
	}
}
