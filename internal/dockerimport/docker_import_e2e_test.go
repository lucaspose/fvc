package dockerimport

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"testing"
)

type testCommandRunner struct{}

func (testCommandRunner) Run(name string, args ...string) error {
	output, err := exec.Command(name, args...).CombinedOutput()
	if err != nil {
		return fmt.Errorf("%s %v failed: %w: %s", name, args, err, string(output))
	}
	return nil
}

func TestDockerImportFromDockerHubE2E(t *testing.T) {
	if os.Getenv("FVC_DOCKER_IMPORT_E2E") != "1" {
		t.Skip("set FVC_DOCKER_IMPORT_E2E=1 to pull a real Docker Hub image")
	}
	result, err := Convert(context.Background(), Options{
		SourceRef: "hello-world:latest",
		Target:    "hello-world:docker",
		WorkDir:   t.TempDir(),
		Runner:    testCommandRunner{},
	})
	if err != nil {
		t.Fatalf("Convert failed: %v", err)
	}
	defer result.Cleanup()
	info, err := os.Stat(result.ImagePath)
	if err != nil {
		t.Fatal(err)
	}
	if info.Size() == 0 {
		t.Fatalf("converted image is empty")
	}
	if result.Metadata.Name != "hello-world:docker" || result.Metadata.Source != "docker:hello-world:latest" {
		t.Fatalf("unexpected metadata: %#v", result.Metadata)
	}
}
