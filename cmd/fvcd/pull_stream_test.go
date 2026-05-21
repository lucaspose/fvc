package main

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/lucaspose/fvc/proto"
	"google.golang.org/grpc/metadata"
)

type operationEventStream struct {
	ctx    context.Context
	events []*proto.OperationEvent
}

func newOperationEventStream() *operationEventStream {
	return &operationEventStream{ctx: context.Background()}
}

func (s *operationEventStream) Send(event *proto.OperationEvent) error {
	s.events = append(s.events, event)
	return nil
}

func (s *operationEventStream) SetHeader(metadata.MD) error {
	return nil
}

func (s *operationEventStream) SendHeader(metadata.MD) error {
	return nil
}

func (s *operationEventStream) SetTrailer(metadata.MD) {}

func (s *operationEventStream) Context() context.Context {
	if s.ctx == nil {
		return context.Background()
	}
	return s.ctx
}

func (s *operationEventStream) SendMsg(any) error {
	return nil
}

func (s *operationEventStream) RecvMsg(any) error {
	return nil
}

func TestPullImageStreamRejectsMissingImage(t *testing.T) {
	server := Server{}
	stream := newOperationEventStream()

	if err := server.PullImageStream(nil, stream); err != nil {
		t.Fatalf("PullImageStream returned transport error: %v", err)
	}

	event := findOperationEvent(stream.events, "validate", "error")
	if event == nil {
		t.Fatalf("expected validate error event, got %#v", stream.events)
	}
	if event.GetErrorMessage() != "image is required" {
		t.Fatalf("unexpected error message: %q", event.GetErrorMessage())
	}
}

func TestPullImageStreamReportsKernelErrorAfterCachedImageReady(t *testing.T) {
	dir := t.TempDir()
	store := NewImageStore(DaemonConfig{
		BaseDir:      dir,
		CacheDir:     filepath.Join(dir, "cache"),
		ActiveDir:    filepath.Join(dir, "active"),
		SnapshotDir:  filepath.Join(dir, "snapshots"),
		KernelPath:   filepath.Join(dir, "kernel", "vmlinux.bin"),
		ImageBaseURL: "file:///not-http",
	})
	if err := store.Init(); err != nil {
		t.Fatalf("store init failed: %v", err)
	}
	imagePath := store.cachedImagePath("ubuntu")
	if err := os.WriteFile(imagePath, []byte("rootfs"), 0644); err != nil {
		t.Fatalf("failed to seed cached image: %v", err)
	}

	server := Server{Store: store}
	stream := newOperationEventStream()
	if err := server.PullImageStream(&proto.PullImageRequest{Image: "ubuntu"}, stream); err != nil {
		t.Fatalf("PullImageStream returned transport error: %v", err)
	}

	if event := findOperationEvent(stream.events, "image", "complete"); event == nil {
		t.Fatalf("expected image complete before kernel failure, got %#v", stream.events)
	}
	event := findOperationEvent(stream.events, "kernel", "error")
	if event == nil {
		t.Fatalf("expected kernel error event, got %#v", stream.events)
	}
	if !strings.Contains(event.GetErrorMessage(), "image base URL must use http or https") {
		t.Fatalf("unexpected kernel error message: %q", event.GetErrorMessage())
	}
	if event.GetPath() != imagePath {
		t.Fatalf("expected cached image path %q, got %q", imagePath, event.GetPath())
	}
}

func TestPullImageStreamReportsDockerTargetErrorWithoutNetwork(t *testing.T) {
	dir := t.TempDir()
	store := NewImageStore(DaemonConfig{
		BaseDir:     dir,
		CacheDir:    filepath.Join(dir, "cache"),
		ActiveDir:   filepath.Join(dir, "active"),
		SnapshotDir: filepath.Join(dir, "snapshots"),
	})
	if err := store.Init(); err != nil {
		t.Fatalf("store init failed: %v", err)
	}

	server := Server{Store: store}
	stream := newOperationEventStream()
	req := &proto.PullImageRequest{
		Image:  "hello-world:latest",
		Source: "docker",
		Target: "../bad",
	}
	if err := server.PullImageStream(req, stream); err != nil {
		t.Fatalf("PullImageStream returned transport error: %v", err)
	}

	if event := findOperationEvent(stream.events, "docker", "running"); event == nil {
		t.Fatalf("expected docker running event, got %#v", stream.events)
	}
	event := findOperationEvent(stream.events, "docker", "error")
	if event == nil {
		t.Fatalf("expected docker error event, got %#v", stream.events)
	}
	if !strings.Contains(event.GetErrorMessage(), "target image") {
		t.Fatalf("unexpected docker error message: %q", event.GetErrorMessage())
	}
}

func findOperationEvent(events []*proto.OperationEvent, stage, status string) *proto.OperationEvent {
	for _, event := range events {
		if event.GetStage() == stage && event.GetStatus() == status {
			return event
		}
	}
	return nil
}
