package main

import (
	"context"
	"database/sql"
	"fmt"
	"os"

	"github.com/lucaspose/fvc/internal"
	"github.com/lucaspose/fvc/internal/vmstore"
	"github.com/lucaspose/fvc/proto"
)

func (s *Server) SnapshotCreate(ctx context.Context, req *proto.SnapshotCreateRequest) (*proto.SnapshotCreateResponse, error) {
	if req == nil || req.VmId == "" || req.Name == "" {
		return &proto.SnapshotCreateResponse{Success: false, Message: "vm id and snapshot name are required"}, nil
	}
	vmID, err := s.resolveVMRef(req.VmId)
	if err != nil {
		return &proto.SnapshotCreateResponse{Success: false, Message: fmt.Sprintf("vm not found: %s", req.VmId)}, nil
	}
	drivePath, err := s.snapshotDrivePath(vmID)
	if err != nil {
		return &proto.SnapshotCreateResponse{Success: false, Message: err.Error()}, nil
	}
	path, size, err := s.Store.CreateSnapshot(vmID, drivePath, req.Name)
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
	vmID, err := s.resolveVMRef(req.VmId)
	if err != nil {
		return &proto.SnapshotListResponse{Success: false, Message: err.Error()}, nil
	}
	snapshots, err := s.Store.ListSnapshots(vmID)
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
	vmID, err := s.resolveVMRef(req.VmId)
	if err != nil {
		return &proto.SnapshotRestoreResponse{Success: false, Message: fmt.Sprintf("vm not found: %s", req.VmId)}, nil
	}
	drivePath, err := s.snapshotDrivePath(vmID)
	if err != nil {
		return &proto.SnapshotRestoreResponse{Success: false, Message: err.Error()}, nil
	}
	path, size, err := s.Store.RestoreSnapshot(vmID, drivePath, req.Name)
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
	vmID, err := s.resolveVMRef(req.VmId)
	if err != nil {
		return &proto.SnapshotRemoveResponse{Success: false, Message: err.Error()}, nil
	}
	if _, err := s.Store.RemoveSnapshot(vmID, req.Name); err != nil {
		return &proto.SnapshotRemoveResponse{Success: false, Message: err.Error()}, nil
	}
	return &proto.SnapshotRemoveResponse{Success: true, Message: fmt.Sprintf("snapshot removed: %s", req.Name)}, nil
}

func (s *Server) snapshotDrivePath(vmID string) (string, error) {
	state, err := vmstore.GetSnapshotDriveState(s.DB, vmID)
	if err == sql.ErrNoRows {
		return "", fmt.Errorf("vm not found: %s", vmID)
	}
	if err != nil {
		return "", fmt.Errorf("state lookup failed: %w", err)
	}
	if state.Status == internal.VmRunning {
		return "", fmt.Errorf("snapshot requires a stopped microVM")
	}
	if state.DrivePath == "" {
		return "", fmt.Errorf("snapshot unavailable: missing drive path")
	}
	if _, err := os.Stat(state.DrivePath); err != nil {
		return "", fmt.Errorf("snapshot unavailable: drive not found: %w", err)
	}
	return state.DrivePath, nil
}
