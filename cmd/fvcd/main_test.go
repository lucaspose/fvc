package main

import (
	"strings"
	"testing"

	"github.com/lucaspose/fvc/proto"
)

func TestValidateRunRequestDefaults(t *testing.T) {
	image, cpus, memory, err := validateRunRequest(&proto.RunRequest{})
	if err != nil {
		t.Fatalf("expected default request to be valid, got %v", err)
	}
	if image != "ubuntu" || cpus != 1 || memory != 512 {
		t.Fatalf("unexpected defaults: image=%s cpus=%d memory=%d", image, cpus, memory)
	}
}

func TestValidateRunRequestRejectsUnsafeImage(t *testing.T) {
	_, _, _, err := validateRunRequest(&proto.RunRequest{Source: "../debian"})
	if err == nil {
		t.Fatal("expected unsafe image ref to be rejected")
	}
}

func TestValidateRunRequestRejectsInvalidResources(t *testing.T) {
	_, _, _, err := validateRunRequest(&proto.RunRequest{
		Source: "debian",
		Config: &proto.VmConfig{Cpus: 0, MemoryMb: 64},
	})
	if err == nil || !strings.Contains(err.Error(), "cpu") {
		t.Fatalf("expected resource validation error, got %v", err)
	}
}
