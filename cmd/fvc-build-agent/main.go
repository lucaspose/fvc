package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

const maxBuildPlanBytes = 1 << 20

type AgentPlan struct {
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

func main() {
	if len(os.Args) > 1 && (os.Args[1] == "-h" || os.Args[1] == "--help" || os.Args[1] == "help") {
		usage(os.Stdout)
		return
	}
	if len(os.Args) > 1 && os.Args[1] == "serve" {
		if err := serve(os.Args[2:], os.Stdout, os.Stderr); err != nil {
			fmt.Fprintf(os.Stderr, "[ERR] %v\n", err)
			os.Exit(1)
		}
		return
	}
	if err := run(os.Stdin, os.Stdout, os.Stderr); err != nil {
		fmt.Fprintf(os.Stderr, "[ERR] %v\n", err)
		os.Exit(1)
	}
}

func usage(out io.Writer) {
	fmt.Fprintln(out, "Usage:")
	fmt.Fprintln(out, "  fvc-build-agent < plan.json")
	fmt.Fprintln(out, "  fvc-build-agent serve [--addr :9090]")
}

func serve(args []string, stdout, stderr io.Writer) error {
	fs := flag.NewFlagSet("serve", flag.ContinueOnError)
	fs.SetOutput(stderr)
	addr := fs.String("addr", ":9090", "HTTP listen address")
	if err := fs.Parse(args); err != nil {
		return err
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		_, _ = w.Write([]byte("ok\n"))
	})
	mux.HandleFunc("/build", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		if err := authorizeBuildRequest(r); err != nil {
			http.Error(w, err.Error(), http.StatusUnauthorized)
			return
		}
		body := http.MaxBytesReader(w, r.Body, maxBuildPlanBytes)
		var output bytes.Buffer
		if err := run(body, &output, stderr); err != nil {
			http.Error(w, strings.TrimSpace(output.String()+"\n"+err.Error()), http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		_, _ = w.Write(output.Bytes())
		_, _ = w.Write([]byte("[OK] build complete\n"))
	})
	fmt.Fprintf(stdout, "[SERVE] fvc-build-agent listening on %s\n", *addr)
	server := &http.Server{
		Addr:              *addr,
		Handler:           mux,
		ReadHeaderTimeout: 5 * time.Second,
	}
	return server.ListenAndServe()
}

func authorizeBuildRequest(r *http.Request) error {
	token := strings.TrimSpace(os.Getenv("FVC_BUILD_AGENT_TOKEN"))
	if token == "" {
		return nil
	}
	got := strings.TrimSpace(r.Header.Get("X-FVC-Build-Token"))
	if got == "" || got != token {
		return errors.New("unauthorized build request")
	}
	return nil
}

func run(input io.Reader, stdout, stderr io.Writer) error {
	var plan AgentPlan
	if err := json.NewDecoder(input).Decode(&plan); err != nil {
		return fmt.Errorf("plan decode failed: %w", err)
	}
	if plan.TargetDevice != "" {
		if err := mountTarget(plan); err != nil {
			return err
		}
		defer func() {
			_ = exec.Command("umount", plan.Root).Run()
		}()
	}
	root, err := cleanRoot(plan.Root)
	if err != nil {
		return err
	}
	useChroot := strings.TrimSpace(plan.TargetDevice) != ""
	for _, step := range plan.Runs {
		if err := runStep(root, plan.Env, step, useChroot, stdout, stderr); err != nil {
			return err
		}
	}
	return nil
}

func mountTarget(plan AgentPlan) error {
	if strings.TrimSpace(plan.Root) == "" {
		return fmt.Errorf("root is required when target_device is set")
	}
	if err := os.MkdirAll(plan.Root, 0755); err != nil {
		return fmt.Errorf("target mount directory create failed: %w", err)
	}
	args := []string{}
	if plan.MountFSType != "" {
		args = append(args, "-t", plan.MountFSType)
	}
	if plan.MountOptions != "" {
		args = append(args, "-o", plan.MountOptions)
	}
	args = append(args, plan.TargetDevice, plan.Root)
	output, err := exec.Command("mount", args...).CombinedOutput()
	if err != nil {
		return fmt.Errorf("target mount failed: %w: %s", err, strings.TrimSpace(string(output)))
	}
	return nil
}

func runStep(root string, baseEnv []string, step RunStep, useChroot bool, stdout, stderr io.Writer) error {
	command := strings.TrimSpace(step.Command)
	if command == "" {
		return fmt.Errorf("run command is required")
	}
	workdir, err := resolveWorkdir(root, step.Workdir)
	if err != nil {
		return err
	}
	timeout := time.Duration(step.TimeoutSeconds) * time.Second
	if timeout <= 0 {
		timeout = 30 * time.Minute
	}
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	fmt.Fprintf(stdout, "[RUN] %s\n", command)
	cmd := exec.CommandContext(ctx, "/bin/sh", "-lc", command)
	cmd.Dir = workdir
	if useChroot {
		guestWorkdir := "/" + strings.TrimPrefix(strings.TrimPrefix(workdir, root), string(filepath.Separator))
		if guestWorkdir == "/" {
			cmd = exec.CommandContext(ctx, "chroot", root, "/bin/sh", "-lc", command)
		} else {
			cmd = exec.CommandContext(ctx, "chroot", root, "/bin/sh", "-lc", "cd "+shellQuote(guestWorkdir)+" && "+command)
		}
	}
	cmd.Env = append(os.Environ(), append(baseEnv, step.Env...)...)
	cmd.Stdout = stdout
	cmd.Stderr = stderr
	if err := cmd.Run(); err != nil {
		if ctx.Err() == context.DeadlineExceeded {
			return fmt.Errorf("run timed out after %s: %s", timeout, command)
		}
		return fmt.Errorf("run failed: %s: %w", command, err)
	}
	return nil
}

func shellQuote(value string) string {
	return "'" + strings.ReplaceAll(value, "'", "'\"'\"'") + "'"
}

func cleanRoot(root string) (string, error) {
	if strings.TrimSpace(root) == "" {
		root = "/"
	}
	abs, err := filepath.Abs(root)
	if err != nil {
		return "", fmt.Errorf("root resolve failed: %w", err)
	}
	info, err := os.Stat(abs)
	if err != nil {
		return "", fmt.Errorf("root stat failed: %w", err)
	}
	if !info.IsDir() {
		return "", fmt.Errorf("root must be a directory")
	}
	return abs, nil
}

func resolveWorkdir(root, workdir string) (string, error) {
	if strings.TrimSpace(workdir) == "" {
		return root, nil
	}
	if !filepath.IsAbs(workdir) {
		return "", fmt.Errorf("workdir must be absolute: %s", workdir)
	}
	target := filepath.Join(root, strings.TrimPrefix(filepath.Clean(workdir), string(filepath.Separator)))
	if !pathWithin(root, target) {
		return "", fmt.Errorf("workdir escapes root: %s", workdir)
	}
	if err := os.MkdirAll(target, 0755); err != nil {
		return "", fmt.Errorf("workdir create failed: %w", err)
	}
	return target, nil
}

func pathWithin(parent, child string) bool {
	rel, err := filepath.Rel(parent, child)
	if err != nil {
		return false
	}
	return rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}
