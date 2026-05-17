package main

import (
	"context"
	"database/sql"
	"fmt"
	"os"

	"github.com/lucaspose/fvc/internal"
	"github.com/lucaspose/fvc/proto"
	"google.golang.org/protobuf/types/known/timestamppb"
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
	path, err := s.Store.ExportImage(req.Image, req.DestPath)
	if err != nil {
		return &proto.ImageExportResponse{Success: false, Message: err.Error()}, nil
	}
	return &proto.ImageExportResponse{Success: true, Message: fmt.Sprintf("image exported: %s", req.Image), Path: path}, nil
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
		Entries: make([]*proto.ImageHistoryEntry, 0, len(history)),
	}
	for _, entry := range history {
		res.Entries = append(res.Entries, &proto.ImageHistoryEntry{
			Action:    entry.Action,
			Message:   entry.Message,
			CreatedAt: timestamppb.New(entry.CreatedAt),
		})
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

func (s *Server) SnapshotCreate(ctx context.Context, req *proto.SnapshotCreateRequest) (*proto.SnapshotCreateResponse, error) {
	if req == nil || req.VmId == "" || req.Name == "" {
		return &proto.SnapshotCreateResponse{Success: false, Message: "vm id and snapshot name are required"}, nil
	}
	drivePath, err := s.snapshotDrivePath(req.VmId)
	if err != nil {
		return &proto.SnapshotCreateResponse{Success: false, Message: err.Error()}, nil
	}
	path, size, err := s.Store.CreateSnapshot(req.VmId, drivePath, req.Name)
	if err != nil {
		return &proto.SnapshotCreateResponse{Success: false, Message: err.Error()}, nil
	}
	return &proto.SnapshotCreateResponse{
		Success:  true,
		Message:  fmt.Sprintf("snapshot created: %s", req.Name),
		Snapshot: snapshotDetails(req.Name, path, size),
	}, nil
}

func (s *Server) SnapshotList(ctx context.Context, req *proto.SnapshotListRequest) (*proto.SnapshotListResponse, error) {
	if req == nil || req.VmId == "" {
		return &proto.SnapshotListResponse{Success: false, Message: "vm id is required"}, nil
	}
	if err := s.vmExists(req.VmId); err != nil {
		return &proto.SnapshotListResponse{Success: false, Message: err.Error()}, nil
	}
	snapshots, err := s.Store.ListSnapshots(req.VmId)
	if err != nil {
		return &proto.SnapshotListResponse{Success: false, Message: err.Error()}, nil
	}
	res := &proto.SnapshotListResponse{
		Success:   true,
		Message:   "snapshots listed",
		Snapshots: make([]*proto.SnapshotDetails, 0, len(snapshots)),
	}
	for _, snapshot := range snapshots {
		res.Snapshots = append(res.Snapshots, snapshotDetails(snapshot.Name, snapshot.Path, snapshot.SizeBytes))
	}
	return res, nil
}

func (s *Server) SnapshotRestore(ctx context.Context, req *proto.SnapshotRestoreRequest) (*proto.SnapshotRestoreResponse, error) {
	if req == nil || req.VmId == "" || req.Name == "" {
		return &proto.SnapshotRestoreResponse{Success: false, Message: "vm id and snapshot name are required"}, nil
	}
	drivePath, err := s.snapshotDrivePath(req.VmId)
	if err != nil {
		return &proto.SnapshotRestoreResponse{Success: false, Message: err.Error()}, nil
	}
	path, size, err := s.Store.RestoreSnapshot(req.VmId, drivePath, req.Name)
	if err != nil {
		return &proto.SnapshotRestoreResponse{Success: false, Message: err.Error()}, nil
	}
	return &proto.SnapshotRestoreResponse{
		Success:  true,
		Message:  fmt.Sprintf("snapshot restored: %s", req.Name),
		Snapshot: snapshotDetails(req.Name, path, size),
	}, nil
}

func (s *Server) SnapshotRemove(ctx context.Context, req *proto.SnapshotRemoveRequest) (*proto.SnapshotRemoveResponse, error) {
	if req == nil || req.VmId == "" || req.Name == "" {
		return &proto.SnapshotRemoveResponse{Success: false, Message: "vm id and snapshot name are required"}, nil
	}
	if err := s.vmExists(req.VmId); err != nil {
		return &proto.SnapshotRemoveResponse{Success: false, Message: err.Error()}, nil
	}
	if _, err := s.Store.RemoveSnapshot(req.VmId, req.Name); err != nil {
		return &proto.SnapshotRemoveResponse{Success: false, Message: err.Error()}, nil
	}
	return &proto.SnapshotRemoveResponse{Success: true, Message: fmt.Sprintf("snapshot removed: %s", req.Name)}, nil
}

func (s *Server) snapshotDrivePath(vmID string) (string, error) {
	var statusValue, drivePath string
	err := s.DB.QueryRow(`SELECT status, COALESCE(drive_path, '') FROM vms WHERE id = ?`, vmID).Scan(&statusValue, &drivePath)
	if err == sql.ErrNoRows {
		return "", fmt.Errorf("vm not found: %s", vmID)
	}
	if err != nil {
		return "", fmt.Errorf("state lookup failed: %w", err)
	}
	if statusValue == internal.VmRunning {
		return "", fmt.Errorf("snapshot requires a stopped microVM")
	}
	if drivePath == "" {
		return "", fmt.Errorf("snapshot unavailable: missing drive path")
	}
	if _, err := os.Stat(drivePath); err != nil {
		return "", fmt.Errorf("snapshot unavailable: drive not found: %w", err)
	}
	return drivePath, nil
}

func (s *Server) vmExists(vmID string) error {
	var id string
	err := s.DB.QueryRow(`SELECT id FROM vms WHERE id = ?`, vmID).Scan(&id)
	if err == sql.ErrNoRows {
		return fmt.Errorf("vm not found: %s", vmID)
	}
	if err != nil {
		return fmt.Errorf("state lookup failed: %w", err)
	}
	return nil
}

func snapshotDetails(name, path string, size int64) *proto.SnapshotDetails {
	return &proto.SnapshotDetails{
		Name:      name,
		Path:      path,
		SizeBytes: size,
	}
}

func imageDetails(info ImageInfo) *proto.ImageDetails {
	details := &proto.ImageDetails{
		Image:     info.Name,
		Path:      info.Path,
		SizeBytes: info.SizeBytes,
		Digest:    info.Digest,
		Source:    info.Metadata.Source,
		Labels:    info.Metadata.Labels,
	}
	if !info.Metadata.CreatedAt.IsZero() {
		details.CreatedAt = timestamppb.New(info.Metadata.CreatedAt)
	}
	return details
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
