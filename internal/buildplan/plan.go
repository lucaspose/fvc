package buildplan

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/BurntSushi/toml"
	"github.com/lucaspose/fvc/internal"
)

type FvcfileConfig struct {
	Image  FvcfileImage         `toml:"image"`
	Config FvcfileRuntimeConfig `toml:"config"`
	Labels map[string]string    `toml:"labels"`
	Copy   []FvcfileCopy        `toml:"copy"`
	Run    []FvcfileRun         `toml:"run"`
}

type FvcfileImage struct {
	From string `toml:"from"`
	Tag  string `toml:"tag"`
}

type FvcfileCopy struct {
	Src  string `toml:"src"`
	Dest string `toml:"dest"`
}

type FvcfileRun struct {
	Command        string   `toml:"command"`
	Env            []string `toml:"env"`
	Workdir        string   `toml:"workdir"`
	TimeoutSeconds int32    `toml:"timeout_seconds"`
}

type FvcfileRuntimeConfig struct {
	Workdir string   `toml:"workdir"`
	Cmd     []string `toml:"cmd"`
	Env     []string `toml:"env"`
	Expose  []int32  `toml:"expose"`
}

type RuntimeConfig struct {
	Workdir      string
	Cmd          []string
	Env          []string
	ExposedPorts []int32
}

type Plan struct {
	ContextPath string
	BaseImage   string
	Tag         string
	Copies      []Copy
	Runs        []Run
	Runtime     RuntimeConfig
	Labels      map[string]string
	Ignore      Ignore
}

type Copy struct {
	SourcePath string
	SourceRel  string
	DestPath   string
}

type Run struct {
	Command        string
	Env            []string
	Workdir        string
	TimeoutSeconds int32
}

// Load reads and validates the Fvcfile in contextPath.
func Load(contextPath, tagOverride string) (Plan, error) {
	if strings.TrimSpace(contextPath) == "" {
		contextPath = "."
	}
	absContext, err := filepath.Abs(contextPath)
	if err != nil {
		return Plan{}, fmt.Errorf("build context resolve failed: %w", err)
	}
	info, err := os.Stat(absContext)
	if err != nil {
		return Plan{}, fmt.Errorf("build context read failed: %w", err)
	}
	if !info.IsDir() {
		return Plan{}, fmt.Errorf("build context must be a directory")
	}

	var config FvcfileConfig
	fvcfilePath := filepath.Join(absContext, "Fvcfile")
	if _, err := toml.DecodeFile(fvcfilePath, &config); err != nil {
		return Plan{}, fmt.Errorf("Fvcfile parse failed: %w", err)
	}
	ignore, err := LoadIgnore(absContext)
	if err != nil {
		return Plan{}, err
	}

	baseImage := strings.TrimSpace(config.Image.From)
	if err := internal.ValidateImageRef(baseImage); err != nil {
		return Plan{}, fmt.Errorf("image.from: %w", err)
	}

	tag := strings.TrimSpace(tagOverride)
	if tag == "" {
		tag = strings.TrimSpace(config.Image.Tag)
	}
	if err := internal.ValidateImageRef(tag); err != nil {
		return Plan{}, fmt.Errorf("image tag: %w", err)
	}

	runtime, err := validateRuntimeConfig(config.Config)
	if err != nil {
		return Plan{}, err
	}
	labels, err := validateLabels(config.Labels)
	if err != nil {
		return Plan{}, err
	}

	copies := make([]Copy, 0, len(config.Copy))
	for _, copySpec := range config.Copy {
		sourcePath, err := resolveSource(absContext, copySpec.Src)
		if err != nil {
			return Plan{}, err
		}
		sourceRel, err := filepath.Rel(absContext, sourcePath)
		if err != nil {
			return Plan{}, fmt.Errorf("copy source path resolve failed: %w", err)
		}
		sourceRel = filepath.ToSlash(sourceRel)
		if ignore.Matches(sourceRel) {
			return Plan{}, fmt.Errorf("copy.src is excluded by .fvcignore: %s", copySpec.Src)
		}
		destPath, err := ResolveDest(copySpec.Dest)
		if err != nil {
			return Plan{}, err
		}
		copies = append(copies, Copy{SourcePath: sourcePath, SourceRel: sourceRel, DestPath: destPath})
	}
	runs, err := validateRuns(config.Run, runtime.Workdir)
	if err != nil {
		return Plan{}, err
	}

	return Plan{
		ContextPath: absContext,
		BaseImage:   baseImage,
		Tag:         tag,
		Copies:      copies,
		Runs:        runs,
		Runtime:     runtime,
		Labels:      labels,
		Ignore:      ignore,
	}, nil
}

func validateRuntimeConfig(config FvcfileRuntimeConfig) (RuntimeConfig, error) {
	workdir := strings.TrimSpace(config.Workdir)
	if workdir != "" {
		clean, err := ResolveDest(workdir)
		if err != nil {
			return RuntimeConfig{}, fmt.Errorf("config.workdir: %w", err)
		}
		workdir = clean
	}
	cmd := make([]string, 0, len(config.Cmd))
	for _, arg := range config.Cmd {
		arg = strings.TrimSpace(arg)
		if arg == "" {
			return RuntimeConfig{}, fmt.Errorf("config.cmd cannot contain empty arguments")
		}
		cmd = append(cmd, arg)
	}
	env := make([]string, 0, len(config.Env))
	for _, value := range config.Env {
		value = strings.TrimSpace(value)
		if !strings.Contains(value, "=") || strings.HasPrefix(value, "=") {
			return RuntimeConfig{}, fmt.Errorf("config.env must use KEY=VALUE entries")
		}
		env = append(env, value)
	}
	exposed := make([]int32, 0, len(config.Expose))
	for _, port := range config.Expose {
		if port < 1 || port > 65535 {
			return RuntimeConfig{}, fmt.Errorf("config.expose port %d must be between 1 and 65535", port)
		}
		exposed = append(exposed, port)
	}
	return RuntimeConfig{Workdir: workdir, Cmd: cmd, Env: env, ExposedPorts: exposed}, nil
}

func validateLabels(labels map[string]string) (map[string]string, error) {
	if len(labels) == 0 {
		return nil, nil
	}
	out := make(map[string]string, len(labels))
	for key, value := range labels {
		key = strings.TrimSpace(key)
		if key == "" || strings.ContainsAny(key, " \t\r\n=") {
			return nil, fmt.Errorf("label key %q is invalid", key)
		}
		out[key] = strings.TrimSpace(value)
	}
	return out, nil
}

func validateRuns(runs []FvcfileRun, defaultWorkdir string) ([]Run, error) {
	out := make([]Run, 0, len(runs))
	for _, run := range runs {
		command := strings.TrimSpace(run.Command)
		if command == "" {
			return nil, fmt.Errorf("run.command is required")
		}
		workdir := strings.TrimSpace(run.Workdir)
		if workdir == "" {
			workdir = defaultWorkdir
		}
		if workdir != "" {
			clean, err := ResolveDest(workdir)
			if err != nil {
				return nil, fmt.Errorf("run.workdir: %w", err)
			}
			workdir = clean
		}
		env := make([]string, 0, len(run.Env))
		for _, value := range run.Env {
			value = strings.TrimSpace(value)
			if !strings.Contains(value, "=") || strings.HasPrefix(value, "=") {
				return nil, fmt.Errorf("run.env must use KEY=VALUE entries")
			}
			env = append(env, value)
		}
		if run.TimeoutSeconds < 0 {
			return nil, fmt.Errorf("run.timeout_seconds must be zero or greater")
		}
		out = append(out, Run{Command: command, Env: env, Workdir: workdir, TimeoutSeconds: run.TimeoutSeconds})
	}
	return out, nil
}

func resolveSource(contextPath, src string) (string, error) {
	src = strings.TrimSpace(src)
	if src == "" {
		return "", fmt.Errorf("copy.src is required")
	}
	if filepath.IsAbs(src) {
		return "", fmt.Errorf("copy.src must be relative to the build context")
	}
	clean := filepath.Clean(src)
	if clean == "." || strings.HasPrefix(clean, ".."+string(filepath.Separator)) || clean == ".." {
		return "", fmt.Errorf("copy.src escapes the build context: %s", src)
	}
	sourcePath := filepath.Join(contextPath, clean)
	realContext, err := filepath.EvalSymlinks(contextPath)
	if err != nil {
		return "", fmt.Errorf("build context symlink resolve failed: %w", err)
	}
	realSource, err := filepath.EvalSymlinks(sourcePath)
	if err != nil {
		return "", fmt.Errorf("copy source unavailable %s: %w", src, err)
	}
	if !pathWithin(realContext, realSource) {
		return "", fmt.Errorf("copy.src escapes the build context: %s", src)
	}
	if _, err := os.Stat(sourcePath); err != nil {
		return "", fmt.Errorf("copy source unavailable %s: %w", src, err)
	}
	return sourcePath, nil
}

// ResolveDest validates an absolute guest path used by copy destinations,
// workdir, and run workdir values.
func ResolveDest(dest string) (string, error) {
	dest = strings.TrimSpace(dest)
	if dest == "" {
		return "", fmt.Errorf("copy.dest is required")
	}
	if !filepath.IsAbs(dest) {
		return "", fmt.Errorf("copy.dest must be an absolute guest path")
	}
	clean := filepath.Clean(dest)
	for _, segment := range strings.Split(clean, string(filepath.Separator)) {
		if segment == ".." {
			return "", fmt.Errorf("copy.dest contains an invalid path segment")
		}
	}
	if clean == string(filepath.Separator) {
		return "", fmt.Errorf("copy.dest cannot be the guest root")
	}
	return clean, nil
}

func pathWithin(parent, child string) bool {
	rel, err := filepath.Rel(parent, child)
	if err != nil {
		return false
	}
	return rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}
