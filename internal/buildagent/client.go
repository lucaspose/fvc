package buildagent

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/lucaspose/fvc/internal/buildplan"
)

type Plan struct {
	Root         string    `json:"root"`
	TargetDevice string    `json:"target_device,omitempty"`
	MountFSType  string    `json:"mount_fstype,omitempty"`
	MountOptions string    `json:"mount_options,omitempty"`
	Env          []string  `json:"env,omitempty"`
	Runs         []RunStep `json:"runs"`
}

type RunStep struct {
	Command        string   `json:"command"`
	Env            []string `json:"env,omitempty"`
	Workdir        string   `json:"workdir,omitempty"`
	TimeoutSeconds int32    `json:"timeout_seconds,omitempty"`
}

// FromBuildPlan converts an FVC build plan to the JSON payload understood by
// fvc-build-agent.
func FromBuildPlan(root string, plan buildplan.Plan) Plan {
	agentPlan := Plan{
		Root: root,
		Env:  append([]string(nil), plan.Runtime.Env...),
		Runs: make([]RunStep, 0, len(plan.Runs)),
	}
	for _, run := range plan.Runs {
		agentPlan.Runs = append(agentPlan.Runs, RunStep{
			Command:        run.Command,
			Env:            append([]string(nil), run.Env...),
			Workdir:        run.Workdir,
			TimeoutSeconds: run.TimeoutSeconds,
		})
	}
	return agentPlan
}

func WaitHealthy(baseURL string, timeout time.Duration) error {
	client := http.Client{Timeout: 1 * time.Second}
	deadline := time.Now().Add(timeout)
	var lastErr error
	for time.Now().Before(deadline) {
		resp, err := client.Get(baseURL + "/healthz")
		if err == nil {
			_ = resp.Body.Close()
			if resp.StatusCode == http.StatusOK {
				return nil
			}
			lastErr = fmt.Errorf("bad status: %s", resp.Status)
		} else {
			lastErr = err
		}
		time.Sleep(250 * time.Millisecond)
	}
	if lastErr == nil {
		lastErr = fmt.Errorf("timeout")
	}
	return lastErr
}

type PostOptions struct {
	Token   string
	Timeout time.Duration
	Output  io.Writer
}

func PostPlan(baseURL string, plan Plan, opts PostOptions) error {
	data, err := json.Marshal(plan)
	if err != nil {
		return fmt.Errorf("build agent plan encode failed: %w", err)
	}
	timeout := opts.Timeout
	if timeout <= 0 {
		timeout = time.Hour
	}
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	client := http.Client{}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, baseURL+"/build", bytes.NewReader(data))
	if err != nil {
		return fmt.Errorf("http request build failed: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	if token := strings.TrimSpace(opts.Token); token != "" {
		req.Header.Set("X-FVC-Build-Token", token)
	}
	resp, err := client.Do(req)
	if err != nil {
		if ctx.Err() == context.DeadlineExceeded {
			return fmt.Errorf("http request timed out after %s: %w", timeout, err)
		}
		return fmt.Errorf("http request failed: %w", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("bad status %s: %s", resp.Status, strings.TrimSpace(string(body)))
	}
	if len(body) > 0 && opts.Output != nil {
		_, _ = opts.Output.Write(body)
	}
	return nil
}
