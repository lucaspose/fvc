package main

import (
	"context"
	"strings"
	"testing"

	"github.com/lucaspose/fvc/proto"
	"google.golang.org/grpc/metadata"
)

func TestValidateRunRequestDefaults(t *testing.T) {
	cfg, err := validateRunRequest(&proto.RunRequest{})
	if err != nil {
		t.Fatalf("expected default request to be valid, got %v", err)
	}
	if cfg.ImageName != "ubuntu" || cfg.CPUs != 1 || cfg.MemoryMB != 512 {
		t.Fatalf("unexpected defaults: %#v", cfg)
	}
}

func TestValidateRunRequestRejectsUnsafeImage(t *testing.T) {
	_, err := validateRunRequest(&proto.RunRequest{Source: "../debian"})
	if err == nil {
		t.Fatal("expected unsafe image ref to be rejected")
	}
}

func TestValidateRunRequestRejectsInvalidResources(t *testing.T) {
	_, err := validateRunRequest(&proto.RunRequest{
		Source: "debian",
		Config: &proto.VmConfig{Cpus: 0, MemoryMb: 64},
	})
	if err == nil || !strings.Contains(err.Error(), "cpu") {
		t.Fatalf("expected resource validation error, got %v", err)
	}
}

func TestValidateRunRequestAcceptsNameAndPorts(t *testing.T) {
	cfg, err := validateRunRequest(&proto.RunRequest{
		Source:       "debian",
		ImageSource:  "docker",
		DockerSource: "debian:bookworm",
		PullPolicy:   "never",
		Name:         "api",
		AutoRemove:   true,
		Config:       &proto.VmConfig{Cpus: 2, MemoryMb: 512, Ports: []string{"8080:80", "443"}, NetworkMode: "nat", Volumes: []string{"data:/var/lib/app", "cache:/cache:ro"}},
	})
	if err != nil {
		t.Fatalf("expected request to be valid, got %v", err)
	}
	if cfg.Name != "api" || strings.Join(cfg.Ports, ",") != "8080:80,443:443" || len(cfg.Volumes) != 2 || !cfg.Volumes[1].ReadOnly || !cfg.AutoRemove || cfg.ImageSource != "docker" || cfg.DockerSource != "debian:bookworm" || cfg.PullPolicy != "never" || cfg.NetworkMode != "nat" {
		t.Fatalf("unexpected run config: %#v", cfg)
	}
}

func TestValidateRunRequestRejectsInvalidVolume(t *testing.T) {
	_, err := validateRunRequest(&proto.RunRequest{Source: "debian", Config: &proto.VmConfig{Cpus: 1, MemoryMb: 512, Volumes: []string{"/host:/guest"}}})
	if err == nil || !strings.Contains(err.Error(), "volume") {
		t.Fatalf("expected volume validation error, got %v", err)
	}
}

func TestValidateRunRequestRejectsUnsupportedImageSource(t *testing.T) {
	_, err := validateRunRequest(&proto.RunRequest{Source: "debian", ImageSource: "oci"})
	if err == nil || !strings.Contains(err.Error(), "unsupported image source") {
		t.Fatalf("expected image source validation error, got %v", err)
	}
}

func TestValidateRunRequestRejectsUnsupportedNetworkMode(t *testing.T) {
	_, err := validateRunRequest(&proto.RunRequest{Source: "debian", Config: &proto.VmConfig{Cpus: 1, MemoryMb: 512, NetworkMode: "bridge"}})
	if err == nil || !strings.Contains(err.Error(), "unsupported network mode") {
		t.Fatalf("expected network mode validation error, got %v", err)
	}
}

func TestEffectiveNetworkModeTracksDaemonDefault(t *testing.T) {
	if got := effectiveNetworkMode("", true); got != "nat" {
		t.Fatalf("expected nat, got %s", got)
	}
	if got := effectiveNetworkMode("", false); got != "none" {
		t.Fatalf("expected none, got %s", got)
	}
	if got := effectiveNetworkMode("none", true); got != "none" {
		t.Fatalf("expected explicit none, got %s", got)
	}
}

func TestRunMicroVMEmitsValidationError(t *testing.T) {
	server := Server{Config: DaemonConfig{NetworkEnabled: false}}
	var events []*proto.RunEvent

	res, err := server.runMicroVM(context.Background(), &proto.RunRequest{Source: "../ubuntu"}, func(event *proto.RunEvent) error {
		events = append(events, event)
		return nil
	})
	if err != nil {
		t.Fatalf("runMicroVM returned transport error: %v", err)
	}
	if res.GetStatus() != "failed" {
		t.Fatalf("expected failed response, got %s", res.GetStatus())
	}
	if len(events) != 1 {
		t.Fatalf("expected one event, got %d", len(events))
	}
	if events[0].GetStatus() != "error" || events[0].GetStage() != "validate" {
		t.Fatalf("unexpected event: %#v", events[0])
	}
}

func TestRunStreamReportsValidationError(t *testing.T) {
	server := Server{Config: DaemonConfig{NetworkEnabled: false}}
	stream := newRunEventStream()

	if err := server.RunStream(&proto.RunRequest{Source: "../ubuntu"}, stream); err != nil {
		t.Fatalf("RunStream returned transport error: %v", err)
	}

	event := findRunEvent(stream.events, "validate", "error")
	if event == nil {
		t.Fatalf("expected validate error event, got %#v", stream.events)
	}
	if !strings.Contains(event.GetErrorMessage(), "invalid request") {
		t.Fatalf("unexpected error message: %q", event.GetErrorMessage())
	}
}

func TestRunStreamReportsPortPublishingConfigError(t *testing.T) {
	server := Server{Config: DaemonConfig{NetworkEnabled: false}}
	stream := newRunEventStream()
	req := &proto.RunRequest{
		Source: "ubuntu",
		Config: &proto.VmConfig{Cpus: 1, MemoryMb: 512, Ports: []string{"8080:80"}},
	}

	if err := server.RunStream(req, stream); err != nil {
		t.Fatalf("RunStream returned transport error: %v", err)
	}

	event := findRunEvent(stream.events, "validate", "error")
	if event == nil {
		t.Fatalf("expected validate error event, got %#v", stream.events)
	}
	if !strings.Contains(event.GetErrorMessage(), "port publishing requires") {
		t.Fatalf("unexpected error message: %q", event.GetErrorMessage())
	}
}

type runEventStream struct {
	ctx    context.Context
	events []*proto.RunEvent
}

func newRunEventStream() *runEventStream {
	return &runEventStream{ctx: context.Background()}
}

func (s *runEventStream) Send(event *proto.RunEvent) error {
	s.events = append(s.events, event)
	return nil
}

func (s *runEventStream) SetHeader(metadata.MD) error {
	return nil
}

func (s *runEventStream) SendHeader(metadata.MD) error {
	return nil
}

func (s *runEventStream) SetTrailer(metadata.MD) {}

func (s *runEventStream) Context() context.Context {
	if s.ctx == nil {
		return context.Background()
	}
	return s.ctx
}

func (s *runEventStream) SendMsg(any) error {
	return nil
}

func (s *runEventStream) RecvMsg(any) error {
	return nil
}

func findRunEvent(events []*proto.RunEvent, stage, status string) *proto.RunEvent {
	for _, event := range events {
		if event.GetStage() == stage && event.GetStatus() == status {
			return event
		}
	}
	return nil
}
