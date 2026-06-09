package main

import (
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/lucaspose/fvc/internal/buildengine"
	"github.com/lucaspose/fvc/internal/buildervm"
	"github.com/lucaspose/fvc/internal/buildplan"
	"github.com/lucaspose/fvc/internal/storeio"
)

type BuildProgressFunc = buildengine.ProgressFunc

func (s *ImageStore) BuildImage(plan BuildPlan, runner CommandRunner) (string, error) {
	return s.BuildImageProgress(plan, runner, nil)
}

func (s *ImageStore) BuildImageProgress(plan BuildPlan, runner CommandRunner, progress BuildProgressFunc) (string, error) {
	if runner == nil {
		runner = realCommandRunner{}
	}
	return s.buildEngine().Build(plan, runner, progress)
}

func (s *ImageStore) applyBuildPlan(imagePath, buildDir string, plan BuildPlan, runner CommandRunner) error {
	return s.buildEngine().ApplyPlan(imagePath, buildDir, plan, runner, nil)
}

func (s *ImageStore) applyBuildPlanProgress(imagePath, buildDir string, plan BuildPlan, runner CommandRunner, progress BuildProgressFunc) error {
	return s.buildEngine().ApplyPlan(imagePath, buildDir, plan, runner, progress)
}

func (s *ImageStore) applyBuildRuns(imagePath, buildDir string, plan BuildPlan, runner CommandRunner) error {
	return s.buildEngine().ApplyRuns(imagePath, buildDir, plan, runner)
}

func (s *ImageStore) applyBuildRunsWithLocalAgent(imagePath, buildDir string, plan BuildPlan, runner CommandRunner) error {
	return s.buildEngine().ApplyRunsWithLocalAgent(imagePath, buildDir, plan, runner)
}

func (s *ImageStore) buildEngine() *buildengine.Engine {
	return buildengine.New(buildengine.Options{
		BaseDir:  s.baseDir,
		CacheDir: s.cacheDir,
		PullImage: func(imageName string, progress storeio.ProgressFunc) (string, error) {
			return s.PullImageIfNeededProgress(imageName, progress)
		},
		ImagePath:     s.cachedImagePath,
		WriteMetadata: s.WriteBuildMetadata,
		RunBackend: func(imagePath, buildDir string, plan buildplan.Plan, runner buildengine.CommandRunner) error {
			return s.applyBuildRunsWithMicroVM(imagePath, plan)
		},
	})
}

func (s *ImageStore) applyBuildRunsWithMicroVM(imagePath string, plan BuildPlan) error {
	network := NewNetworkManager(nil)
	return buildervm.New(buildervm.Options{
		BaseDir:       s.baseDir,
		DefaultKernel: s.kernelPath,
		SetupNetwork: func(id string) (NetworkConfig, error) {
			return network.Setup(id)
		},
		CleanupNetwork: func(cfg NetworkConfig) {
			_ = network.Cleanup(cfg)
		},
		Output: os.Stdout,
	}).Run(imagePath, plan)
}

func builderBootArgs(cfg NetworkConfig) string {
	return buildervm.BootArgs(cfg, os.Getenv("FVC_BUILD_AGENT_TOKEN"), os.Getenv("FVC_BUILDER_BOOT_ARGS_EXTRA"))
}

func buildAgentRequestTimeout() time.Duration {
	return buildervm.RequestTimeoutFromEnv(os.Getenv)
}

func envIntOrDefault(key string, fallback int) int {
	value := strings.TrimSpace(os.Getenv(key))
	if value == "" {
		return fallback
	}
	parsed, err := strconv.Atoi(value)
	if err != nil {
		return fallback
	}
	return parsed
}
