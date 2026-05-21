package fcvsock

import (
	"strings"
	"testing"
)

func TestGuestCIDIsStableAndValid(t *testing.T) {
	first := GuestCID("vm-1")
	second := GuestCID("vm-1")
	if first != second {
		t.Fatalf("expected stable cid, got %d and %d", first, second)
	}
	if first < 3 {
		t.Fatalf("cid must be at least 3, got %d", first)
	}
}

func TestConfigPayload(t *testing.T) {
	payload := ConfigPayload("/run/fvc/vm.vsock", 42)
	if !strings.Contains(payload, `"guest_cid":42`) || !strings.Contains(payload, `"uds_path":"/run/fvc/vm.vsock"`) {
		t.Fatalf("unexpected payload: %s", payload)
	}
}

func TestShouldConfigure(t *testing.T) {
	for _, mode := range []string{"", "vsock", "auto", " VSOCK "} {
		if !ShouldConfigure(mode) {
			t.Fatalf("expected mode %q to configure vsock", mode)
		}
	}
	if ShouldConfigure("tcp") {
		t.Fatal("tcp mode should not configure vsock")
	}
}
