package main

import (
	"fmt"
	"strings"
	"testing"
)

type fakeRunner struct {
	calls []string
	fail  map[string]bool
}

func (f *fakeRunner) Run(name string, args ...string) error {
	call := name + " " + strings.Join(args, " ")
	f.calls = append(f.calls, call)
	if f.fail != nil && f.fail[call] {
		return fmt.Errorf("forced failure")
	}
	return nil
}

func TestBuildNetworkConfigIsDeterministicAndValid(t *testing.T) {
	first := BuildNetworkConfig("vm-123")
	second := BuildNetworkConfig("vm-123")
	if first != second {
		t.Fatalf("expected deterministic config, got %#v and %#v", first, second)
	}
	if !strings.HasPrefix(first.TapName, "fvc") || len(first.TapName) > 15 {
		t.Fatalf("invalid tap name: %s", first.TapName)
	}
	if !strings.HasPrefix(first.MAC, "02:FC:") {
		t.Fatalf("expected locally administered fvc mac, got %s", first.MAC)
	}
	if first.HostIP == first.GuestIP {
		t.Fatal("host and guest IPs must differ")
	}
}

func TestNetworkSetupRunsExpectedCommands(t *testing.T) {
	runner := &fakeRunner{}
	manager := NewNetworkManager(runner)
	cfg, err := manager.Setup("vm-setup")
	if err != nil {
		t.Fatalf("Setup failed: %v", err)
	}

	expectedParts := []string{
		"ip tuntap add dev " + cfg.TapName + " mode tap",
		"ip addr replace " + cfg.HostIP + "/30 dev " + cfg.TapName,
		"ip link set " + cfg.TapName + " up",
		"sysctl -w net.ipv4.ip_forward=1",
		"iptables -t nat -C POSTROUTING -s " + cfg.GuestIP + "/32 -j MASQUERADE",
	}
	for _, expected := range expectedParts {
		if !containsCall(runner.calls, expected) {
			t.Fatalf("missing command %q in calls %#v", expected, runner.calls)
		}
	}
}

func TestNetworkSetupAddsNatRuleWhenMissing(t *testing.T) {
	cfg := BuildNetworkConfig("vm-nat")
	checkCall := "iptables -t nat -C POSTROUTING -s " + cfg.GuestIP + "/32 -j MASQUERADE"
	addCall := "iptables -t nat -A POSTROUTING -s " + cfg.GuestIP + "/32 -j MASQUERADE"
	runner := &fakeRunner{fail: map[string]bool{checkCall: true}}
	manager := NewNetworkManager(runner)

	if _, err := manager.Setup("vm-nat"); err != nil {
		t.Fatalf("Setup failed: %v", err)
	}
	if !containsCall(runner.calls, addCall) {
		t.Fatalf("expected NAT add command %q in calls %#v", addCall, runner.calls)
	}
}

func TestBootArgsIncludesNetworkWhenConfigured(t *testing.T) {
	cfg := BuildNetworkConfig("vm-boot")
	args := bootArgs(&cfg)
	for _, part := range []string{cfg.GuestIP, cfg.HostIP, cfg.Netmask, "eth0"} {
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

func containsCall(calls []string, expected string) bool {
	for _, call := range calls {
		if call == expected {
			return true
		}
	}
	return false
}
