package buildagent

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/lucaspose/fvc/internal/buildplan"
)

func TestFromBuildPlanCopiesRuntimeAndRuns(t *testing.T) {
	plan := FromBuildPlan("/mnt/target", buildplan.Plan{
		Runtime: buildplan.RuntimeConfig{Env: []string{"A=B"}},
		Runs: []buildplan.Run{{
			Command:        "echo ok",
			Env:            []string{"C=D"},
			Workdir:        "/srv",
			TimeoutSeconds: 12,
		}},
	})
	if plan.Root != "/mnt/target" || plan.Env[0] != "A=B" {
		t.Fatalf("unexpected plan root/env: %#v", plan)
	}
	if len(plan.Runs) != 1 || plan.Runs[0].Command != "echo ok" || plan.Runs[0].TimeoutSeconds != 12 {
		t.Fatalf("unexpected runs: %#v", plan.Runs)
	}
}

func TestWaitHealthy(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/healthz" {
			t.Fatalf("unexpected path %s", r.URL.Path)
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	if err := WaitHealthy(server.URL, time.Second); err != nil {
		t.Fatalf("WaitHealthy failed: %v", err)
	}
}

func TestPostPlanSendsTokenAndWritesOutput(t *testing.T) {
	var gotToken string
	var gotPlan Plan
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotToken = r.Header.Get("X-FVC-Build-Token")
		if err := json.NewDecoder(r.Body).Decode(&gotPlan); err != nil {
			t.Fatal(err)
		}
		_, _ = w.Write([]byte("ok\n"))
	}))
	defer server.Close()

	var output bytes.Buffer
	if err := PostPlan(server.URL, Plan{Root: "/target"}, PostOptions{Token: "secret", Timeout: time.Second, Output: &output}); err != nil {
		t.Fatalf("PostPlan failed: %v", err)
	}
	if gotToken != "secret" {
		t.Fatalf("expected token header, got %q", gotToken)
	}
	if gotPlan.Root != "/target" {
		t.Fatalf("unexpected decoded plan: %#v", gotPlan)
	}
	if output.String() != "ok\n" {
		t.Fatalf("unexpected output %q", output.String())
	}
}
