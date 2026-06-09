package main

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/lucaspose/fvc/internal/vmstore"
	"github.com/lucaspose/fvc/proto"
	"google.golang.org/grpc"
)

func (s *Server) BuildImage(ctx context.Context, req *proto.BuildImageRequest) (*proto.BuildImageResponse, error) {
	if req == nil {
		return &proto.BuildImageResponse{Success: false, Message: "build request is required"}, nil
	}
	plan, err := LoadBuildPlan(req.ContextPath, req.Tag)
	if err != nil {
		return &proto.BuildImageResponse{Success: false, Message: err.Error()}, nil
	}
	path, err := s.Store.BuildImage(plan, s.commandRunner())
	if err != nil {
		return &proto.BuildImageResponse{Success: false, Message: err.Error(), Image: plan.Tag}, nil
	}
	return &proto.BuildImageResponse{
		Success: true,
		Message: fmt.Sprintf("image built: %s", plan.Tag),
		Image:   plan.Tag,
		Path:    path,
	}, nil
}

func (s *Server) BuildImageStream(req *proto.BuildImageRequest, stream grpc.ServerStreamingServer[proto.OperationEvent]) error {
	if req == nil {
		return stream.Send(&proto.OperationEvent{Stage: "validate", Status: "error", Message: "build request is required", ErrorMessage: "build request is required"})
	}
	send := func(stage, eventStatus, message string, current, total int64) error {
		if stream.Context().Err() != nil {
			return stream.Context().Err()
		}
		return stream.Send(&proto.OperationEvent{Stage: stage, Status: eventStatus, Message: message, Current: current, Total: total})
	}
	if err := send("plan", "running", "Loading Fvcfile", 0, 0); err != nil {
		return err
	}
	plan, err := LoadBuildPlan(req.ContextPath, req.Tag)
	if err != nil {
		_ = send("plan", "error", err.Error(), 0, 0)
		return nil
	}
	if err := send("plan", "complete", fmt.Sprintf("Build plan ready: %s", plan.Tag), 0, 0); err != nil {
		return err
	}
	path, err := s.Store.BuildImageProgress(plan, s.commandRunner(), send)
	if err != nil {
		_ = stream.Send(&proto.OperationEvent{Stage: "build", Status: "error", Message: err.Error(), ErrorMessage: err.Error(), Image: plan.Tag})
		return nil
	}
	return stream.Send(&proto.OperationEvent{
		Stage:   "done",
		Status:  "complete",
		Message: fmt.Sprintf("image built: %s", plan.Tag),
		Image:   plan.Tag,
		Path:    path,
	})
}

func (s *Server) ImageInspect(ctx context.Context, req *proto.ImageInspectRequest) (*proto.ImageInspectResponse, error) {
	if req == nil || req.Image == "" {
		return &proto.ImageInspectResponse{Success: false, Message: "image is required"}, nil
	}
	info, err := s.Store.InspectImage(req.Image)
	if err != nil {
		return &proto.ImageInspectResponse{Success: false, Message: err.Error()}, nil
	}
	return &proto.ImageInspectResponse{
		Success: true,
		Message: "image found",
		Image:   imageDetails(info),
	}, nil
}

func (s *Server) ImageRemove(ctx context.Context, req *proto.ImageRemoveRequest) (*proto.ImageRemoveResponse, error) {
	if req == nil || req.Image == "" {
		return &proto.ImageRemoveResponse{Success: false, Message: "image is required"}, nil
	}
	used, err := s.imageInUse(req.Image)
	if err != nil {
		return &proto.ImageRemoveResponse{Success: false, Message: err.Error()}, nil
	}
	if used && !req.Force {
		return &proto.ImageRemoveResponse{Success: false, Message: "image is used by one or more microVMs; use --force to remove anyway"}, nil
	}
	if err := s.Store.RemoveImage(req.Image); err != nil {
		return &proto.ImageRemoveResponse{Success: false, Message: err.Error()}, nil
	}
	return &proto.ImageRemoveResponse{Success: true, Message: fmt.Sprintf("image removed: %s", req.Image)}, nil
}

func (s *Server) ImageTag(ctx context.Context, req *proto.ImageTagRequest) (*proto.ImageTagResponse, error) {
	if req == nil || req.Source == "" || req.Target == "" {
		return &proto.ImageTagResponse{Success: false, Message: "source and target images are required"}, nil
	}
	info, err := s.Store.TagImage(req.Source, req.Target)
	if err != nil {
		return &proto.ImageTagResponse{Success: false, Message: err.Error()}, nil
	}
	return &proto.ImageTagResponse{
		Success: true,
		Message: fmt.Sprintf("image tagged: %s", req.Target),
		Image:   imageDetails(info),
	}, nil
}

func (s *Server) ImageImport(ctx context.Context, req *proto.ImageImportRequest) (*proto.ImageImportResponse, error) {
	if req == nil || req.SourcePath == "" || req.Image == "" {
		return &proto.ImageImportResponse{Success: false, Message: "source path and image are required"}, nil
	}
	if err := s.validateHostImagePath(req.SourcePath, false); err != nil {
		return &proto.ImageImportResponse{Success: false, Message: err.Error()}, nil
	}
	info, err := s.Store.ImportImage(req.SourcePath, req.Image)
	if err != nil {
		return &proto.ImageImportResponse{Success: false, Message: err.Error()}, nil
	}
	return &proto.ImageImportResponse{
		Success: true,
		Message: fmt.Sprintf("image imported: %s", req.Image),
		Image:   imageDetails(info),
	}, nil
}

func (s *Server) ImageExport(ctx context.Context, req *proto.ImageExportRequest) (*proto.ImageExportResponse, error) {
	if req == nil || req.Image == "" || req.DestPath == "" {
		return &proto.ImageExportResponse{Success: false, Message: "image and destination path are required"}, nil
	}
	if err := s.validateHostImagePath(req.DestPath, true); err != nil {
		return &proto.ImageExportResponse{Success: false, Message: err.Error()}, nil
	}
	path, err := s.Store.ExportImage(req.Image, req.DestPath)
	if err != nil {
		return &proto.ImageExportResponse{Success: false, Message: err.Error()}, nil
	}
	return &proto.ImageExportResponse{Success: true, Message: fmt.Sprintf("image exported: %s", req.Image), Path: path}, nil
}

func (s *Server) validateHostImagePath(path string, forWrite bool) error {
	if s.Config.AllowHostImagePaths {
		return nil
	}
	base := strings.TrimSpace(s.Config.BaseDir)
	if base == "" {
		return fmt.Errorf("host image path access requires daemon base dir")
	}
	baseAbs, err := filepath.Abs(base)
	if err != nil {
		return fmt.Errorf("daemon base dir resolve failed: %w", err)
	}
	baseReal, err := filepath.EvalSymlinks(baseAbs)
	if err != nil {
		return fmt.Errorf("daemon base dir symlink resolve failed: %w", err)
	}

	targetAbs, err := filepath.Abs(path)
	if err != nil {
		return fmt.Errorf("host image path resolve failed: %w", err)
	}
	targetReal := targetAbs
	if forWrite {
		parentReal, err := filepath.EvalSymlinks(filepath.Dir(targetAbs))
		if err != nil {
			return fmt.Errorf("host image destination parent resolve failed: %w", err)
		}
		targetReal = filepath.Join(parentReal, filepath.Base(targetAbs))
	} else {
		targetReal, err = filepath.EvalSymlinks(targetAbs)
		if err != nil {
			return fmt.Errorf("host image source resolve failed: %w", err)
		}
	}
	if !pathWithin(baseReal, targetReal) {
		return fmt.Errorf("host image paths are restricted to %s; set FVC_ALLOW_HOST_IMAGE_PATHS=true to override", baseReal)
	}
	if forWrite {
		if info, err := os.Lstat(targetReal); err == nil && info.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("refusing to export image through symlink: %s", targetReal)
		}
	}
	return nil
}

func pathWithin(parent, child string) bool {
	rel, err := filepath.Rel(parent, child)
	if err != nil {
		return false
	}
	return rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

func (s *Server) ImageHistory(ctx context.Context, req *proto.ImageHistoryRequest) (*proto.ImageHistoryResponse, error) {
	if req == nil || req.Image == "" {
		return &proto.ImageHistoryResponse{Success: false, Message: "image is required"}, nil
	}
	history, err := s.Store.ImageHistory(req.Image)
	if err != nil {
		return &proto.ImageHistoryResponse{Success: false, Message: err.Error()}, nil
	}
	res := &proto.ImageHistoryResponse{
		Success: true,
		Message: "image history found",
		Entries: imageHistoryEntries(history),
	}
	return res, nil
}

func (s *Server) ImagePrune(ctx context.Context, req *proto.ImagePruneRequest) (*proto.ImagePruneResponse, error) {
	dryRun := req != nil && req.DryRun
	used, err := s.usedImages()
	if err != nil {
		return &proto.ImagePruneResponse{Success: false, Message: err.Error()}, nil
	}
	result, err := s.Store.PruneUnusedImages(used, dryRun)
	if err != nil {
		return &proto.ImagePruneResponse{Success: false, Message: err.Error()}, nil
	}
	message := "image prune complete"
	if dryRun {
		message = "image prune dry-run complete"
	}
	res := &proto.ImagePruneResponse{
		Success:       true,
		Message:       message,
		RemovedImages: result.RemovedImages,
		FreedBytes:    result.FreedBytes,
		DryRun:        dryRun,
		Images:        make([]*proto.ImageDetails, 0, len(result.Images)),
	}
	for _, info := range result.Images {
		res.Images = append(res.Images, imageDetails(info))
	}
	return res, nil
}

func (s *Server) vmExists(vmID string) error {
	err := vmstore.Exists(s.DB, vmID)
	if err == sql.ErrNoRows {
		return fmt.Errorf("vm not found: %s", vmID)
	}
	if err != nil {
		return fmt.Errorf("state lookup failed: %w", err)
	}
	return nil
}

func (s *Server) imageInUse(image string) (bool, error) {
	var count int
	err := s.DB.QueryRow(`SELECT COUNT(*) FROM vms WHERE image = ?`, image).Scan(&count)
	if err != nil {
		return false, fmt.Errorf("image usage lookup failed: %w", err)
	}
	return count > 0, nil
}

func (s *Server) usedImages() (map[string]bool, error) {
	rows, err := s.DB.Query(`SELECT DISTINCT image FROM vms WHERE image <> ''`)
	if err != nil {
		return nil, fmt.Errorf("image usage query failed: %w", err)
	}
	defer rows.Close()
	used := make(map[string]bool)
	for rows.Next() {
		var image string
		if err := rows.Scan(&image); err != nil {
			return nil, fmt.Errorf("image usage scan failed: %w", err)
		}
		used[image] = true
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("image usage rows failed: %w", err)
	}
	return used, nil
}
