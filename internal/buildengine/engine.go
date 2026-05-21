package buildengine

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/lucaspose/fvc/internal"
	"github.com/lucaspose/fvc/internal/buildagent"
	"github.com/lucaspose/fvc/internal/buildplan"
	"github.com/lucaspose/fvc/internal/rootfs"
	"github.com/lucaspose/fvc/internal/storeio"
)

// CommandRunner runs host commands such as mount and umount.
type CommandRunner interface {
	Run(name string, args ...string) error
}

// ProgressFunc reports build-stage progress.
type ProgressFunc func(stage, status, message string, current, total int64) error

// EnvFunc reads environment-like configuration values.
type EnvFunc func(key string) string

// RunBackend applies [[run]] steps after copy steps have been committed.
type RunBackend func(imagePath, buildDir string, plan buildplan.Plan, runner CommandRunner) error

// Options wires the image store and daemon-specific run backend into Engine.
type Options struct {
	BaseDir        string
	CacheDir       string
	PullImage      func(imageName string, progress storeio.ProgressFunc) (string, error)
	ImagePath      func(imageName string) string
	WriteMetadata  func(plan buildplan.Plan) error
	RunBackend     RunBackend
	Env            EnvFunc
	BuildAgentPath string
	Output         *os.File
	ErrorOutput    *os.File
}

// Engine builds Firecracker rootfs images from FVC build plans.
type Engine struct {
	opts Options
}

// New creates a build engine.
func New(opts Options) *Engine {
	return &Engine{opts: opts}
}

// Build applies plan and publishes the resulting image.
func (e *Engine) Build(plan buildplan.Plan, runner CommandRunner, progress ProgressFunc) (string, error) {
	if runner == nil {
		return "", fmt.Errorf("command runner is required")
	}
	if err := internal.ValidateImageRef(plan.BaseImage); err != nil {
		return "", err
	}
	if err := internal.ValidateImageRef(plan.Tag); err != nil {
		return "", err
	}
	if e.opts.PullImage == nil {
		return "", fmt.Errorf("image resolver is required")
	}
	if e.opts.ImagePath == nil {
		return "", fmt.Errorf("image path resolver is required")
	}
	if e.opts.WriteMetadata == nil {
		return "", fmt.Errorf("metadata writer is required")
	}

	emitProgress(progress, "base", "running", "Resolving base image "+plan.BaseImage, 0, 0)
	basePath, err := e.opts.PullImage(plan.BaseImage, func(current, total int64) {
		_ = emitProgress(progress, "base", "running", "Downloading base image "+plan.BaseImage, current, total)
	})
	if err != nil {
		return "", err
	}
	emitProgress(progress, "base", "complete", "Base image ready", 0, 0)

	buildDir := filepath.Join(e.opts.BaseDir, "build")
	if err := os.MkdirAll(e.opts.CacheDir, 0755); err != nil {
		return "", fmt.Errorf("cache directory setup failed: %w", err)
	}
	if err := os.MkdirAll(buildDir, 0755); err != nil {
		return "", fmt.Errorf("build directory setup failed: %w", err)
	}

	tmpImagePath, cleanup, err := e.createTemporaryBuildImage(buildDir, plan.Tag)
	if err != nil {
		return "", err
	}
	defer cleanup()

	emitProgress(progress, "copy", "running", "Copying base filesystem", 0, 0)
	if _, err := storeio.CopyFileAtomicProgress(basePath, tmpImagePath, func(current, total int64) {
		_ = emitProgress(progress, "copy", "running", "Copying base filesystem", current, total)
	}); err != nil {
		return "", err
	}
	emitProgress(progress, "copy", "complete", "Base filesystem copied", 0, 0)
	if err := e.ApplyPlan(tmpImagePath, buildDir, plan, runner, progress); err != nil {
		return "", err
	}

	finalPath := e.opts.ImagePath(plan.Tag)
	emitProgress(progress, "publish", "running", "Publishing image "+plan.Tag, 0, 0)
	if err := os.Rename(tmpImagePath, finalPath); err != nil {
		return "", fmt.Errorf("image publish failed: %w", err)
	}
	if err := e.opts.WriteMetadata(plan); err != nil {
		return "", err
	}
	emitProgress(progress, "publish", "complete", "Image published", 0, 0)
	return finalPath, nil
}

// ApplyPlan mounts imagePath and applies copy and run steps.
func (e *Engine) ApplyPlan(imagePath, buildDir string, plan buildplan.Plan, runner CommandRunner, progress ProgressFunc) error {
	mountDir, err := os.MkdirTemp(buildDir, "mnt-*")
	if err != nil {
		return fmt.Errorf("build mount directory create failed: %w", err)
	}
	defer func() {
		_ = os.RemoveAll(mountDir)
	}()

	emitProgress(progress, "mount", "running", "Mounting build filesystem", 0, 0)
	if err := runner.Run("mount", "-o", "loop", imagePath, mountDir); err != nil {
		return fmt.Errorf("rootfs mount failed: %w", err)
	}
	emitProgress(progress, "mount", "complete", "Build filesystem mounted", 0, 0)
	mounted := true
	defer func() {
		if mounted {
			_ = runner.Run("umount", mountDir)
		}
	}()

	for i, copySpec := range plan.Copies {
		emitProgress(progress, "copy", "running", fmt.Sprintf("Copying %s to %s", copySpec.SourceRel, copySpec.DestPath), int64(i), int64(len(plan.Copies)))
		if err := rootfs.CopyInto(copySpec.SourcePath, mountDir, copySpec.DestPath, plan.ContextPath, plan.Ignore); err != nil {
			return err
		}
	}
	if len(plan.Copies) > 0 {
		emitProgress(progress, "copy", "complete", "Build context copied", int64(len(plan.Copies)), int64(len(plan.Copies)))
	}
	emitProgress(progress, "mount", "running", "Unmounting build filesystem", 0, 0)
	if err := runner.Run("umount", mountDir); err != nil {
		return fmt.Errorf("rootfs unmount failed: %w", err)
	}
	mounted = false
	emitProgress(progress, "mount", "complete", "Build filesystem unmounted", 0, 0)
	if len(plan.Runs) > 0 {
		emitProgress(progress, "run", "running", "Running build commands", 0, int64(len(plan.Runs)))
		if err := e.ApplyRuns(imagePath, buildDir, plan, runner); err != nil {
			return err
		}
		emitProgress(progress, "run", "complete", "Build commands complete", int64(len(plan.Runs)), int64(len(plan.Runs)))
	}
	return nil
}

// ApplyRuns dispatches [[run]] build steps to the configured backend.
func (e *Engine) ApplyRuns(imagePath, buildDir string, plan buildplan.Plan, runner CommandRunner) error {
	backend := e.envOrDefault("FVC_BUILD_BACKEND", "microvm")
	switch backend {
	case "agent":
		return e.ApplyRunsWithLocalAgent(imagePath, buildDir, plan, runner)
	case "microvm":
		if e.opts.RunBackend == nil {
			return fmt.Errorf("microvm build backend is not configured")
		}
		return e.opts.RunBackend(imagePath, buildDir, plan, runner)
	default:
		return fmt.Errorf("unsupported FVC_BUILD_BACKEND %q", backend)
	}
}

// ApplyRunsWithLocalAgent executes [[run]] through a host-mounted agent.
func (e *Engine) ApplyRunsWithLocalAgent(imagePath, buildDir string, plan buildplan.Plan, runner CommandRunner) error {
	if !e.envBoolOrDefault("FVC_ALLOW_INSECURE_HOST_AGENT", false) {
		return fmt.Errorf("FVC_BUILD_BACKEND=agent runs build commands on the host-mounted rootfs and requires FVC_ALLOW_INSECURE_HOST_AGENT=true")
	}
	mountDir, err := os.MkdirTemp(buildDir, "run-mnt-*")
	if err != nil {
		return fmt.Errorf("build run mount directory create failed: %w", err)
	}
	defer func() {
		_ = os.RemoveAll(mountDir)
	}()
	if err := runner.Run("mount", "-o", "loop", imagePath, mountDir); err != nil {
		return fmt.Errorf("rootfs run mount failed: %w", err)
	}
	mounted := true
	defer func() {
		if mounted {
			_ = runner.Run("umount", mountDir)
		}
	}()
	if err := e.runLocalAgent(mountDir, plan); err != nil {
		return err
	}
	if err := runner.Run("umount", mountDir); err != nil {
		return fmt.Errorf("rootfs run unmount failed: %w", err)
	}
	mounted = false
	return nil
}

func (e *Engine) createTemporaryBuildImage(buildDir, tag string) (string, func(), error) {
	tmpImage, err := os.CreateTemp(buildDir, "."+filepath.Base(e.opts.ImagePath(tag))+".tmp-*")
	if err != nil {
		return "", nil, fmt.Errorf("temporary build image create failed: %w", err)
	}
	tmpImagePath := tmpImage.Name()
	if err := tmpImage.Close(); err != nil {
		_ = os.Remove(tmpImagePath)
		return "", nil, fmt.Errorf("temporary build image close failed: %w", err)
	}
	cleanup := func() {
		_ = os.Remove(tmpImagePath)
	}
	return tmpImagePath, cleanup, nil
}

func (e *Engine) runLocalAgent(root string, plan buildplan.Plan) error {
	agentPath := strings.TrimSpace(e.opts.BuildAgentPath)
	if agentPath == "" {
		agentPath = strings.TrimSpace(e.env("FVC_BUILD_AGENT_PATH"))
	}
	if agentPath == "" {
		return fmt.Errorf("FVC_BUILD_AGENT_PATH is required when FVC_BUILD_BACKEND=agent")
	}
	data, err := json.Marshal(buildagent.FromBuildPlan(root, plan))
	if err != nil {
		return fmt.Errorf("build agent plan encode failed: %w", err)
	}
	cmd := exec.Command(agentPath)
	cmd.Stdin = strings.NewReader(string(data))
	cmd.Stdout = e.opts.Output
	if cmd.Stdout == nil {
		cmd.Stdout = os.Stdout
	}
	cmd.Stderr = e.opts.ErrorOutput
	if cmd.Stderr == nil {
		cmd.Stderr = os.Stderr
	}
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("build agent failed: %w", err)
	}
	return nil
}

func emitProgress(progress ProgressFunc, stage, status, message string, current, total int64) error {
	if progress == nil {
		return nil
	}
	return progress(stage, status, message, current, total)
}

func (e *Engine) env(key string) string {
	if e.opts.Env != nil {
		return e.opts.Env(key)
	}
	return os.Getenv(key)
}

func (e *Engine) envOrDefault(key, fallback string) string {
	if value := e.env(key); value != "" {
		return value
	}
	return fallback
}

func (e *Engine) envBoolOrDefault(key string, fallback bool) bool {
	value := strings.ToLower(strings.TrimSpace(e.env(key)))
	if value == "" {
		return fallback
	}
	return value == "1" || value == "true" || value == "yes" || value == "on"
}
