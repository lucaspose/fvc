package main

import (
	"github.com/lucaspose/fvc/proto"
	"google.golang.org/protobuf/types/known/timestamppb"
)

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
		Env:       append([]string(nil), info.Metadata.Env...),
		Cmd:       append([]string(nil), info.Metadata.Cmd...),
		Workdir:   info.Metadata.Workdir,
	}
	details.ExposedPorts = append([]int32(nil), info.Metadata.ExposedPorts...)
	if !info.Metadata.CreatedAt.IsZero() {
		details.CreatedAt = timestamppb.New(info.Metadata.CreatedAt)
	}
	return details
}

func imageHistoryEntries(history []ImageHistory) []*proto.ImageHistoryEntry {
	entries := make([]*proto.ImageHistoryEntry, 0, len(history))
	for _, entry := range history {
		entries = append(entries, &proto.ImageHistoryEntry{
			Action:    entry.Action,
			Message:   entry.Message,
			CreatedAt: timestamppb.New(entry.CreatedAt),
		})
	}
	return entries
}
