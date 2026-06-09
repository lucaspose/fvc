package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/lucaspose/fvc/internal"
	"github.com/lucaspose/fvc/internal/guestruntime"
	"github.com/lucaspose/fvc/internal/vmstore"
	"github.com/lucaspose/fvc/proto"
	"google.golang.org/protobuf/types/known/timestamppb"
)

const defaultVolumeSizeMB = 128

type preparedVolume struct {
	Spec     internal.VolumeSpec
	DriveID  string
	Device   string
	HostPath string
}

type volumeUsage struct {
	UsedBy            int32
	AttachedToRunning bool
}

func parseVolumeSpecs(values []string) ([]internal.VolumeSpec, error) {
	volumes := make([]internal.VolumeSpec, 0, len(values))
	targets := map[string]bool{}
	for _, value := range values {
		volume, err := internal.ParseVolumeSpec(value)
		if err != nil {
			return nil, err
		}
		if targets[volume.Target] {
			return nil, fmt.Errorf("volume target %q is used more than once", volume.Target)
		}
		targets[volume.Target] = true
		volumes = append(volumes, volume)
	}
	return volumes, nil
}

func formatVolumeSpecs(volumes []internal.VolumeSpec) []string {
	values := make([]string, 0, len(volumes))
	for _, volume := range volumes {
		values = append(values, internal.FormatVolumeSpec(volume))
	}
	return values
}

func (s *Server) prepareVolumeDrives(volumes []internal.VolumeSpec) ([]preparedVolume, error) {
	if len(volumes) > 25 {
		return nil, fmt.Errorf("at most 25 volumes can be attached")
	}
	volumeDir := filepath.Join(s.Config.BaseDir, "volumes")
	if err := os.MkdirAll(volumeDir, 0750); err != nil {
		return nil, fmt.Errorf("volume directory setup failed: %w", err)
	}
	prepared := make([]preparedVolume, 0, len(volumes))
	for i, volume := range volumes {
		hostPath := filepath.Join(volumeDir, volume.Name+".ext4")
		if err := s.ensureVolumeImage(hostPath, defaultVolumeSizeMB); err != nil {
			return nil, fmt.Errorf("volume %s setup failed: %w", volume.Name, err)
		}
		prepared = append(prepared, preparedVolume{
			Spec:     volume,
			DriveID:  fmt.Sprintf("vol%d", i),
			Device:   volumeDeviceName(i),
			HostPath: hostPath,
		})
	}
	return prepared, nil
}

func (s *Server) VolumeCreate(ctx context.Context, req *proto.VolumeCreateRequest) (*proto.VolumeCreateResponse, error) {
	if req == nil || strings.TrimSpace(req.Name) == "" {
		return &proto.VolumeCreateResponse{Success: false, Message: "volume name is required"}, nil
	}
	name := strings.TrimSpace(req.Name)
	if err := internal.ValidateVolumeName(name); err != nil {
		return &proto.VolumeCreateResponse{Success: false, Message: err.Error()}, nil
	}
	sizeMB := req.SizeMb
	if sizeMB == 0 {
		sizeMB = defaultVolumeSizeMB
	}
	if err := internal.ValidateVolumeSizeMB(sizeMB); err != nil {
		return &proto.VolumeCreateResponse{Success: false, Message: err.Error()}, nil
	}
	path, err := s.volumePath(name)
	if err != nil {
		return &proto.VolumeCreateResponse{Success: false, Message: err.Error()}, nil
	}
	if _, err := os.Stat(path); err == nil {
		return &proto.VolumeCreateResponse{Success: false, Message: fmt.Sprintf("volume already exists: %s", name)}, nil
	} else if !os.IsNotExist(err) {
		return &proto.VolumeCreateResponse{Success: false, Message: fmt.Sprintf("volume stat failed: %v", err)}, nil
	}
	if err := s.ensureVolumeImage(path, sizeMB); err != nil {
		return &proto.VolumeCreateResponse{Success: false, Message: fmt.Sprintf("volume create failed: %v", err)}, nil
	}
	details, err := s.volumeDetails(name)
	if err != nil {
		return &proto.VolumeCreateResponse{Success: false, Message: err.Error()}, nil
	}
	return &proto.VolumeCreateResponse{Success: true, Message: fmt.Sprintf("volume created: %s", name), Volume: details}, nil
}

func (s *Server) VolumeList(ctx context.Context, req *proto.VolumeListRequest) (*proto.VolumeListResponse, error) {
	volumes, err := s.listVolumeDetails()
	if err != nil {
		return &proto.VolumeListResponse{Success: false, Message: err.Error()}, nil
	}
	return &proto.VolumeListResponse{Success: true, Message: "volumes listed", Volumes: volumes}, nil
}

func (s *Server) VolumeInspect(ctx context.Context, req *proto.VolumeInspectRequest) (*proto.VolumeInspectResponse, error) {
	if req == nil || strings.TrimSpace(req.Name) == "" {
		return &proto.VolumeInspectResponse{Success: false, Message: "volume name is required"}, nil
	}
	details, err := s.volumeDetails(strings.TrimSpace(req.Name))
	if err != nil {
		return &proto.VolumeInspectResponse{Success: false, Message: err.Error()}, nil
	}
	return &proto.VolumeInspectResponse{Success: true, Message: "volume found", Volume: details}, nil
}

func (s *Server) VolumeRemove(ctx context.Context, req *proto.VolumeRemoveRequest) (*proto.VolumeRemoveResponse, error) {
	if req == nil || strings.TrimSpace(req.Name) == "" {
		return &proto.VolumeRemoveResponse{Success: false, Message: "volume name is required"}, nil
	}
	name := strings.TrimSpace(req.Name)
	details, err := s.volumeDetails(name)
	if err != nil {
		return &proto.VolumeRemoveResponse{Success: false, Message: err.Error()}, nil
	}
	if details.AttachedToRunning {
		return &proto.VolumeRemoveResponse{Success: false, Message: "volume is attached to a running microVM"}, nil
	}
	if details.UsedBy > 0 && !req.Force {
		return &proto.VolumeRemoveResponse{Success: false, Message: "volume is used by one or more microVMs; use --force to remove anyway"}, nil
	}
	if err := removeVolumeFile(details.Path); err != nil {
		return &proto.VolumeRemoveResponse{Success: false, Message: fmt.Sprintf("volume remove failed: %v", err)}, nil
	}
	return &proto.VolumeRemoveResponse{Success: true, Message: fmt.Sprintf("volume removed: %s", name), Volume: details}, nil
}

func (s *Server) VolumePrune(ctx context.Context, req *proto.VolumePruneRequest) (*proto.VolumePruneResponse, error) {
	dryRun := req != nil && req.DryRun
	volumes, err := s.listVolumeDetails()
	if err != nil {
		return &proto.VolumePruneResponse{Success: false, Message: err.Error()}, nil
	}
	res := &proto.VolumePruneResponse{
		Success: true,
		Message: "volume prune complete",
		DryRun:  dryRun,
		Volumes: make([]*proto.VolumeDetails, 0),
	}
	if dryRun {
		res.Message = "volume prune dry-run complete"
	}
	for _, volume := range volumes {
		if volume.UsedBy > 0 || volume.AttachedToRunning {
			continue
		}
		res.Volumes = append(res.Volumes, volume)
		res.FreedBytes += volume.SizeBytes
		res.RemovedVolumes++
		if !dryRun {
			if err := removeVolumeFile(volume.Path); err != nil {
				return &proto.VolumePruneResponse{Success: false, Message: fmt.Sprintf("volume prune failed for %s: %v", volume.Name, err)}, nil
			}
		}
	}
	return res, nil
}

func (s *Server) ensureVolumeImage(path string, sizeMB int64) error {
	if _, err := os.Stat(path); err == nil {
		return nil
	} else if !os.IsNotExist(err) {
		return err
	}
	tmp := path + ".tmp"
	_ = os.Remove(tmp)
	runner := s.commandRunner()
	if err := runner.Run("truncate", "-s", fmt.Sprintf("%dM", sizeMB), tmp); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	if err := runner.Run("mkfs.ext4", "-q", "-F", tmp); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	return nil
}

func (s *Server) volumePath(name string) (string, error) {
	if err := internal.ValidateVolumeName(name); err != nil {
		return "", err
	}
	volumeDir := filepath.Join(s.Config.BaseDir, "volumes")
	if err := os.MkdirAll(volumeDir, 0750); err != nil {
		return "", fmt.Errorf("volume directory setup failed: %w", err)
	}
	return filepath.Join(volumeDir, name+".ext4"), nil
}

func (s *Server) volumeDetails(name string) (*proto.VolumeDetails, error) {
	path, err := s.volumePath(name)
	if err != nil {
		return nil, err
	}
	info, err := volumeFileInfo(path)
	if os.IsNotExist(err) {
		return nil, fmt.Errorf("volume not found: %s", name)
	}
	if err != nil {
		return nil, err
	}
	usage, err := s.volumeUsage()
	if err != nil {
		return nil, err
	}
	return volumeDetailsProto(name, path, info, usage[name]), nil
}

func (s *Server) listVolumeDetails() ([]*proto.VolumeDetails, error) {
	volumeDir := filepath.Join(s.Config.BaseDir, "volumes")
	if err := os.MkdirAll(volumeDir, 0750); err != nil {
		return nil, fmt.Errorf("volume directory setup failed: %w", err)
	}
	entries, err := os.ReadDir(volumeDir)
	if err != nil {
		return nil, fmt.Errorf("volume directory read failed: %w", err)
	}
	usage, err := s.volumeUsage()
	if err != nil {
		return nil, err
	}
	volumes := make([]*proto.VolumeDetails, 0, len(entries))
	for _, entry := range entries {
		name, ok := strings.CutSuffix(entry.Name(), ".ext4")
		if !ok {
			continue
		}
		if err := internal.ValidateVolumeName(name); err != nil {
			continue
		}
		path := filepath.Join(volumeDir, entry.Name())
		info, err := volumeFileInfo(path)
		if err != nil {
			return nil, err
		}
		volumes = append(volumes, volumeDetailsProto(name, path, info, usage[name]))
	}
	sort.Slice(volumes, func(i, j int) bool {
		return volumes[i].Name < volumes[j].Name
	})
	return volumes, nil
}

func (s *Server) volumeUsage() (map[string]volumeUsage, error) {
	rows, err := s.DB.Query(`SELECT COALESCE(volumes, ''), status FROM vms WHERE COALESCE(volumes, '') <> ''`)
	if err != nil {
		return nil, fmt.Errorf("volume usage query failed: %w", err)
	}
	defer rows.Close()
	usage := map[string]volumeUsage{}
	for rows.Next() {
		var encoded string
		var status string
		if err := rows.Scan(&encoded, &status); err != nil {
			return nil, fmt.Errorf("volume usage scan failed: %w", err)
		}
		for _, value := range vmstore.SplitList(encoded) {
			volume, err := internal.ParseVolumeSpec(value)
			if err != nil {
				continue
			}
			current := usage[volume.Name]
			current.UsedBy++
			if status == internal.VmRunning {
				current.AttachedToRunning = true
			}
			usage[volume.Name] = current
		}
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("volume usage rows failed: %w", err)
	}
	return usage, nil
}

func volumeFileInfo(path string) (os.FileInfo, error) {
	info, err := os.Lstat(path)
	if err != nil {
		return nil, err
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return nil, fmt.Errorf("refusing symlink volume path: %s", path)
	}
	if !info.Mode().IsRegular() {
		return nil, fmt.Errorf("volume path is not a regular file: %s", path)
	}
	return info, nil
}

func removeVolumeFile(path string) error {
	if _, err := volumeFileInfo(path); err != nil {
		return err
	}
	return os.Remove(path)
}

func volumeDetailsProto(name string, path string, info os.FileInfo, usage volumeUsage) *proto.VolumeDetails {
	details := &proto.VolumeDetails{
		Name:              name,
		Path:              path,
		SizeBytes:         info.Size(),
		UsedBy:            usage.UsedBy,
		AttachedToRunning: usage.AttachedToRunning,
	}
	if !info.ModTime().IsZero() {
		details.CreatedAt = timestamppb.New(info.ModTime().Truncate(time.Second))
	}
	return details
}

func volumeDeviceName(index int) string {
	return "/dev/vd" + string(rune('b'+index))
}

func runtimeVolumes(volumes []preparedVolume) []guestruntime.Volume {
	runtimeVolumes := make([]guestruntime.Volume, 0, len(volumes))
	for _, volume := range volumes {
		runtimeVolumes = append(runtimeVolumes, guestruntime.Volume{
			Device:   volume.Device,
			Target:   volume.Spec.Target,
			ReadOnly: volume.Spec.ReadOnly,
		})
	}
	return runtimeVolumes
}

func storedVolumeSpecs(values []string) []string {
	normalized := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value != "" {
			normalized = append(normalized, value)
		}
	}
	return normalized
}
