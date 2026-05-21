package main

import (
	"fmt"
	"strings"
	"testing"
)

func TestBootArgsIncludesNetworkWhenConfigured(t *testing.T) {
	cfg := BuildNetworkConfig("vm-boot")
	args := bootArgs(&cfg)
	for _, part := range []string{cfg.GuestIP, cfg.HostIP, cfg.Netmask, "eth0", "random.trust_cpu=on"} {
		if !strings.Contains(args, part) {
			t.Fatalf("expected boot args %q to contain %q", args, part)
		}
	}
}

func TestNetworkPermissionHint(t *testing.T) {
	err := withNetworkPermissionHint(fmt.Errorf("ioctl(TUNSETIFF): Operation not permitted"))
	if err == nil || !strings.Contains(err.Error(), "fvcd must run with CAP_NET_ADMIN/root") {
		t.Fatalf("expected permission hint, got %v", err)
	}
}
