package main

import (
	"context"
	"fmt"
	"path/filepath"

	"github.com/lucaspose/fvc/internal"
	"github.com/lucaspose/fvc/internal/dockerimport"
)

type DockerImportProgressFunc = dockerimport.ProgressFunc

func (s *ImageStore) ImportDockerImage(ctx context.Context, sourceRef, target string, runner CommandRunner, progress DockerImportProgressFunc) (ImageInfo, error) {
	if err := internal.ValidateImageRef(target); err != nil {
		return ImageInfo{}, fmt.Errorf("target image: %w", err)
	}
	if runner == nil {
		runner = realCommandRunner{}
	}
	result, err := dockerimport.Convert(ctx, dockerimport.Options{
		SourceRef: sourceRef,
		Target:    target,
		WorkDir:   filepath.Join(s.baseDir, "build"),
		Runner:    runner,
		Progress:  progress,
	})
	if err != nil {
		return ImageInfo{}, err
	}
	if result.Cleanup != nil {
		defer result.Cleanup()
	}

	emitDockerImportProgress(progress, "import", "running", "Importing converted image "+target, 0, 0)
	info, err := s.ImportImageWithMetadata(result.ImagePath, target, imageMetadataFromDockerImport(result.Metadata))
	if err != nil {
		return ImageInfo{}, err
	}
	emitDockerImportProgress(progress, "import", "complete", "Converted image imported", 0, 0)
	return info, nil
}

func emitDockerImportProgress(progress DockerImportProgressFunc, stage, status, message string, current, total int64) error {
	if progress == nil {
		return nil
	}
	return progress(stage, status, message, current, total)
}

func imageMetadataFromDockerImport(metadata dockerimport.Metadata) ImageMetadata {
	history := make([]ImageHistory, 0, len(metadata.History))
	for _, entry := range metadata.History {
		history = append(history, ImageHistory{
			Action:    entry.Action,
			Message:   entry.Message,
			CreatedAt: entry.CreatedAt,
		})
	}
	return ImageMetadata{
		Name:         metadata.Name,
		Source:       metadata.Source,
		CreatedAt:    metadata.CreatedAt,
		Labels:       cloneLabels(metadata.Labels),
		Env:          append([]string(nil), metadata.Env...),
		Cmd:          append([]string(nil), metadata.Cmd...),
		Workdir:      metadata.Workdir,
		ExposedPorts: append([]int32(nil), metadata.ExposedPorts...),
		History:      history,
	}
}

func cloneLabels(labels map[string]string) map[string]string {
	if len(labels) == 0 {
		return nil
	}
	out := make(map[string]string, len(labels))
	for key, value := range labels {
		out[key] = value
	}
	return out
}
