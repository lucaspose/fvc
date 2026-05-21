package main

import (
	"strings"
	"testing"
)

func TestGuestAgentEndpointPrefersVsock(t *testing.T) {
	endpoint := guestAgentEndpointFor("vsock", "172.16.0.2", "/run/fvc/vm.vsock")
	if endpoint.Transport != "vsock" || !endpoint.available() {
		t.Fatalf("unexpected endpoint: %#v", endpoint)
	}
}

func TestGuestAgentEndpointAutoFallsBackToTCP(t *testing.T) {
	endpoint := guestAgentEndpointFor("auto", "172.16.0.2", "")
	if endpoint.Transport != "tcp" || !endpoint.available() {
		t.Fatalf("unexpected endpoint: %#v", endpoint)
	}
}

func TestGuestAgentEndpointAutoPrefersTCPWhenAvailable(t *testing.T) {
	endpoint := guestAgentEndpointFor("auto", "172.16.0.2", "/run/fvc/vm.vsock")
	if endpoint.Transport != "tcp" || !endpoint.available() {
		t.Fatalf("unexpected endpoint: %#v", endpoint)
	}
}

func TestGuestAgentEndpointRejectsMissingVsock(t *testing.T) {
	endpoint := guestAgentEndpointFor("vsock", "172.16.0.2", "")
	if endpoint.available() {
		t.Fatalf("expected endpoint to be unavailable: %#v", endpoint)
	}
}

func TestGuestAgentCIDIsStableAndValid(t *testing.T) {
	first := guestAgentCID("vm-1")
	second := guestAgentCID("vm-1")
	if first != second {
		t.Fatalf("expected stable cid, got %d and %d", first, second)
	}
	if first < 3 {
		t.Fatalf("cid must be at least 3, got %d", first)
	}
}

func TestConfigureFirecrackerVsockPayload(t *testing.T) {
	payload := vsockConfigPayload("/run/fvc/vm.vsock", 42)
	if !strings.Contains(payload, `"guest_cid":42`) || !strings.Contains(payload, `"uds_path":"/run/fvc/vm.vsock"`) {
		t.Fatalf("unexpected payload: %s", payload)
	}
}
