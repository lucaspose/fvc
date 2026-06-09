package main

import (
	"context"
	"fmt"
	"strings"

	"github.com/lucaspose/fvc/internal/hostprune"
	"github.com/lucaspose/fvc/proto"
	"google.golang.org/grpc"
)

func (s *Server) PullImage(ctx context.Context, req *proto.PullImageRequest) (*proto.PullImageResponse, error) {
	if req == nil || req.Image == "" {
		return &proto.PullImageResponse{Success: false, Message: "image is required"}, nil
	}
	if strings.TrimSpace(req.GetSource()) == "docker" {
		target, err := dockerImportTarget(req.Image, req.GetTarget())
		if err != nil {
			return &proto.PullImageResponse{Success: false, Message: err.Error(), Image: req.Image}, nil
		}
		info, err := s.Store.ImportDockerImage(ctx, req.Image, target, s.commandRunner(), nil)
		if err != nil {
			return &proto.PullImageResponse{Success: false, Message: fmt.Sprintf("docker image conversion failed: %v", err), Image: target}, nil
		}
		return &proto.PullImageResponse{Success: true, Message: "docker image converted", Image: target, Path: info.Path}, nil
	}
	path, err := s.Store.PullImageIfNeeded(req.Image)
	if err != nil {
		return &proto.PullImageResponse{Success: false, Message: fmt.Sprintf("image pull failed: %v", err), Image: req.Image}, nil
	}
	if _, err := s.Store.PullKernelIfNeeded(); err != nil {
		return &proto.PullImageResponse{Success: false, Message: fmt.Sprintf("kernel pull failed: %v", err), Image: req.Image, Path: path}, nil
	}
	return &proto.PullImageResponse{
		Success: true,
		Message: "image ready",
		Image:   req.Image,
		Path:    path,
	}, nil
}

func (s *Server) PullImageStream(req *proto.PullImageRequest, stream grpc.ServerStreamingServer[proto.OperationEvent]) error {
	if req == nil || req.Image == "" {
		return stream.Send(&proto.OperationEvent{Stage: "validate", Status: "error", Message: "image is required", ErrorMessage: "image is required"})
	}
	targetImage := req.Image
	if strings.TrimSpace(req.GetSource()) == "docker" {
		target, err := dockerImportTarget(req.Image, req.GetTarget())
		if err != nil {
			return stream.Send(&proto.OperationEvent{Stage: "validate", Status: "error", Message: err.Error(), ErrorMessage: err.Error(), Image: req.Image})
		}
		targetImage = target
	}
	send := func(stage, eventStatus, message string, current, total int64) error {
		if stream.Context().Err() != nil {
			return stream.Context().Err()
		}
		return stream.Send(&proto.OperationEvent{Stage: stage, Status: eventStatus, Message: message, Current: current, Total: total, Image: targetImage})
	}
	if strings.TrimSpace(req.GetSource()) == "docker" {
		if err := send("docker", "running", fmt.Sprintf("Converting Docker image %s", req.Image), 0, 0); err != nil {
			return err
		}
		info, err := s.Store.ImportDockerImage(stream.Context(), req.Image, targetImage, s.commandRunner(), func(stage, eventStatus, message string, current, total int64) error {
			return send(stage, eventStatus, message, current, total)
		})
		if err != nil {
			_ = stream.Send(&proto.OperationEvent{Stage: "docker", Status: "error", Message: fmt.Sprintf("docker image conversion failed: %v", err), ErrorMessage: err.Error(), Image: targetImage})
			return nil
		}
		if err := send("docker", "complete", "Docker image converted", 0, 0); err != nil {
			return err
		}
		return stream.Send(&proto.OperationEvent{Stage: "done", Status: "complete", Message: "image ready", Image: targetImage, Path: info.Path})
	}
	if err := send("image", "running", fmt.Sprintf("Resolving image %s", req.Image), 0, 0); err != nil {
		return err
	}
	path, err := s.Store.PullImageIfNeededProgress(req.Image, func(current, total int64) {
		_ = send("image", "running", fmt.Sprintf("Downloading image %s", req.Image), current, total)
	})
	if err != nil {
		_ = stream.Send(&proto.OperationEvent{Stage: "image", Status: "error", Message: fmt.Sprintf("image pull failed: %v", err), ErrorMessage: err.Error(), Image: req.Image})
		return nil
	}
	if err := send("image", "complete", "Image ready", 0, 0); err != nil {
		return err
	}
	if err := send("kernel", "running", "Resolving kernel", 0, 0); err != nil {
		return err
	}
	if _, err := s.Store.PullKernelIfNeededProgress(func(current, total int64) {
		_ = send("kernel", "running", "Downloading kernel", current, total)
	}); err != nil {
		_ = stream.Send(&proto.OperationEvent{Stage: "kernel", Status: "error", Message: fmt.Sprintf("kernel pull failed: %v", err), ErrorMessage: err.Error(), Image: req.Image, Path: path})
		return nil
	}
	if err := send("kernel", "complete", "Kernel ready", 0, 0); err != nil {
		return err
	}
	return stream.Send(&proto.OperationEvent{Stage: "done", Status: "complete", Message: "image ready", Image: req.Image, Path: path})
}

func (s *Server) ListImages(ctx context.Context, req *proto.ListImagesRequest) (*proto.ListImagesResponse, error) {
	images, err := s.Store.ListImages()
	if err != nil {
		return nil, err
	}
	res := &proto.ListImagesResponse{Images: make([]*proto.ImageDetails, 0, len(images))}
	for _, image := range images {
		info, err := s.Store.InspectCachedImage(image.Name, image.Path, false)
		if err != nil {
			return nil, err
		}
		res.Images = append(res.Images, imageDetails(info))
	}
	return res, nil
}

func (s *Server) Prune(ctx context.Context, req *proto.PruneRequest) (*proto.PruneResponse, error) {
	dryRun := req != nil && req.DryRun
	imageResult, err := s.Store.PruneImageCache(dryRun)
	if err != nil {
		return &proto.PruneResponse{Success: false, Message: err.Error()}, nil
	}
	activeResult, err := s.pruneOrphanActiveDrives(dryRun)
	if err != nil {
		return &proto.PruneResponse{Success: false, Message: err.Error(), RemovedFiles: imageResult.RemovedFiles, FreedBytes: imageResult.FreedBytes}, nil
	}
	runtimeResult, err := s.pruneRuntimeFiles(dryRun)
	if err != nil {
		return &proto.PruneResponse{
			Success:      false,
			Message:      err.Error(),
			RemovedFiles: imageResult.RemovedFiles + activeResult.RemovedFiles,
			FreedBytes:   imageResult.FreedBytes + activeResult.FreedBytes,
		}, nil
	}

	removed := imageResult.RemovedFiles + activeResult.RemovedFiles + runtimeResult.RemovedFiles
	freed := imageResult.FreedBytes + activeResult.FreedBytes + runtimeResult.FreedBytes
	items := pruneProtoItems(imageResult, activeResult, runtimeResult)
	message := "prune complete"
	if dryRun {
		message = "prune dry-run complete"
	}
	return &proto.PruneResponse{
		Success:      true,
		Message:      message,
		RemovedFiles: removed,
		FreedBytes:   freed,
		Items:        items,
		DryRun:       dryRun,
	}, nil
}

func pruneProtoItems(results ...PruneResult) []*proto.PruneItem {
	var items []*proto.PruneItem
	for _, result := range results {
		for _, item := range result.Items {
			items = append(items, &proto.PruneItem{
				Kind:      item.Kind,
				Path:      item.Path,
				SizeBytes: item.SizeBytes,
			})
		}
	}
	return items
}

func (s *Server) pruneOrphanActiveDrives(dryRun bool) (PruneResult, error) {
	result, err := hostprune.OrphanActiveDrives(s.DB, s.Config.ActiveDir, dryRun)
	return pruneResultFromHost(result), err
}

func (s *Server) pruneRuntimeFiles(dryRun bool) (PruneResult, error) {
	result, err := hostprune.RuntimeFiles(s.DB, s.Config.RuntimeDir, dryRun)
	return pruneResultFromHost(result), err
}

func pruneResultFromHost(result hostprune.Result) PruneResult {
	items := make([]PruneItem, 0, len(result.Items))
	for _, item := range result.Items {
		items = append(items, PruneItem{
			Kind:      item.Kind,
			Path:      item.Path,
			SizeBytes: item.SizeBytes,
		})
	}
	return PruneResult{
		RemovedFiles: result.RemovedFiles,
		FreedBytes:   result.FreedBytes,
		Items:        items,
	}
}
