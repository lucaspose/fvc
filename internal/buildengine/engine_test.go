package buildengine

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/lucaspose/fvc/internal/buildplan"
	"github.com/lucaspose/fvc/internal/storeio"
)

type fakeRunner struct {
	calls []string
}

func (r *fakeRunner) Run(name string, args ...string) error {
	r.calls = append(r.calls, name+" "+strings.Join(args, " "))
	return nil
}

func testEnv(values map[string]string) EnvFunc {
	return func(key string) string {
		return values[key]
	}
}

func TestBuildRequiresDependencies(t *testing.T) {
	engine := New(Options{})
	_, err := engine.Build(buildplan.Plan{BaseImage: "ubuntu", Tag: "out"}, &fakeRunner{}, nil)
	if err == nil || !strings.Contains(err.Error(), "image resolver") {
		t.Fatalf("expected image resolver error, got %v", err)
	}
}

func TestApplyRunsUsesMicroVMBackendByDefault(t *testing.T) {
	called := false
	engine := New(Options{
		Env: testEnv(map[string]string{}),
		RunBackend: func(imagePath, buildDir string, plan buildplan.Plan, runner CommandRunner) error {
			called = true
			if imagePath != "image.ext4" || buildDir != "/tmp/build" || len(plan.Runs) != 1 {
				t.Fatalf("unexpected backend args image=%s build=%s plan=%#v", imagePath, buildDir, plan)
			}
			return nil
		},
	})
	err := engine.ApplyRuns("image.ext4", "/tmp/build", buildplan.Plan{Runs: []buildplan.Run{{Command: "true"}}}, &fakeRunner{})
	if err != nil {
		t.Fatalf("ApplyRuns failed: %v", err)
	}
	if !called {
		t.Fatal("expected microvm backend to be called")
	}
}

func TestApplyRunsRejectsUnknownBackend(t *testing.T) {
	engine := New(Options{Env: testEnv(map[string]string{"FVC_BUILD_BACKEND": "bogus"})})
	err := engine.ApplyRuns("image.ext4", "/tmp/build", buildplan.Plan{Runs: []buildplan.Run{{Command: "true"}}}, &fakeRunner{})
	if err == nil || !strings.Contains(err.Error(), "unsupported FVC_BUILD_BACKEND") {
		t.Fatalf("expected unsupported backend error, got %v", err)
	}
}

func TestLocalAgentRequiresOptIn(t *testing.T) {
	engine := New(Options{Env: testEnv(map[string]string{"FVC_BUILD_BACKEND": "agent"})})
	err := engine.ApplyRunsWithLocalAgent("image.ext4", t.TempDir(), buildplan.Plan{Runs: []buildplan.Run{{Command: "true"}}}, &fakeRunner{})
	if err == nil || !strings.Contains(err.Error(), "FVC_ALLOW_INSECURE_HOST_AGENT") {
		t.Fatalf("expected opt-in error, got %v", err)
	}
}

func TestBuildPublishesImageAndWritesMetadata(t *testing.T) {
	dir := t.TempDir()
	basePath := filepath.Join(dir, "base.ext4")
	finalPath := filepath.Join(dir, "out.ext4")
	if err := os.WriteFile(basePath, []byte("base"), 0644); err != nil {
		t.Fatalf("failed to seed base image: %v", err)
	}
	metadataWritten := false
	engine := New(Options{
		BaseDir:  dir,
		CacheDir: filepath.Join(dir, "cache"),
		PullImage: func(imageName string, progress storeio.ProgressFunc) (string, error) {
			if imageName != "ubuntu" {
				t.Fatalf("unexpected base image: %s", imageName)
			}
			return basePath, nil
		},
		ImagePath: func(imageName string) string {
			if imageName != "out" {
				t.Fatalf("unexpected image path name: %s", imageName)
			}
			return finalPath
		},
		WriteMetadata: func(plan buildplan.Plan) error {
			metadataWritten = true
			if plan.Tag != "out" {
				t.Fatalf("unexpected metadata plan: %#v", plan)
			}
			return nil
		},
	})

	runner := &fakeRunner{}
	path, err := engine.Build(buildplan.Plan{BaseImage: "ubuntu", Tag: "out"}, runner, nil)
	if err != nil {
		t.Fatalf("Build failed: %v", err)
	}
	if path != finalPath {
		t.Fatalf("unexpected final path: %s", path)
	}
	if got, err := os.ReadFile(finalPath); err != nil || string(got) != "base" {
		t.Fatalf("unexpected final image contents %q err=%v", got, err)
	}
	if !metadataWritten {
		t.Fatal("expected metadata writer to be called")
	}
}
