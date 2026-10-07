package hostnet

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
		"iptables -t nat -L FVC-PREROUTING",
		"iptables -t nat -L FVC-OUTPUT",
		"iptables -t nat -L FVC-POSTROUTING",
		"iptables -L FVC-FORWARD",
		"iptables -t nat -C PREROUTING -j FVC-PREROUTING",
		"iptables -t nat -C OUTPUT -j FVC-OUTPUT",
		"iptables -t nat -C POSTROUTING -j FVC-POSTROUTING",
		"iptables -C FORWARD -j FVC-FORWARD",
		"iptables -t nat -C FVC-POSTROUTING -s " + cfg.GuestIP + "/32 -j MASQUERADE",
	}
	for _, expected := range expectedParts {
		if !containsCall(runner.calls, expected) {
			t.Fatalf("missing command %q in calls %#v", expected, runner.calls)
		}
	}
}

func TestNetworkSetupAddsNatRuleWhenMissing(t *testing.T) {
	cfg := BuildNetworkConfig("vm-nat")
	checkCall := "iptables -t nat -C FVC-POSTROUTING -s " + cfg.GuestIP + "/32 -j MASQUERADE"
	addCall := "iptables -t nat -A FVC-POSTROUTING -s " + cfg.GuestIP + "/32 -j MASQUERADE"
	runner := &fakeRunner{fail: map[string]bool{checkCall: true}}
	manager := NewNetworkManager(runner)

	if _, err := manager.Setup("vm-nat"); err != nil {
		t.Fatalf("Setup failed: %v", err)
	}
	if !containsCall(runner.calls, addCall) {
		t.Fatalf("expected NAT add command %q in calls %#v", addCall, runner.calls)
	}
}

func TestNetworkSetupCreatesMissingDedicatedChainsAndJumps(t *testing.T) {
	runner := &fakeRunner{fail: map[string]bool{
		"iptables -t nat -L FVC-PREROUTING":                 true,
		"iptables -t nat -L FVC-OUTPUT":                     true,
		"iptables -t nat -L FVC-POSTROUTING":                true,
		"iptables -L FVC-FORWARD":                           true,
		"iptables -t nat -C PREROUTING -j FVC-PREROUTING":   true,
		"iptables -t nat -C OUTPUT -j FVC-OUTPUT":           true,
		"iptables -t nat -C POSTROUTING -j FVC-POSTROUTING": true,
		"iptables -C FORWARD -j FVC-FORWARD":                true,
	}}
	manager := NewNetworkManager(runner)

	if _, err := manager.Setup("vm-chain"); err != nil {
		t.Fatalf("Setup failed: %v", err)
	}
	for _, expected := range []string{
		"iptables -t nat -N FVC-PREROUTING",
		"iptables -t nat -N FVC-OUTPUT",
		"iptables -t nat -N FVC-POSTROUTING",
		"iptables -N FVC-FORWARD",
		"iptables -t nat -A PREROUTING -j FVC-PREROUTING",
		"iptables -t nat -A OUTPUT -j FVC-OUTPUT",
		"iptables -t nat -A POSTROUTING -j FVC-POSTROUTING",
		"iptables -A FORWARD -j FVC-FORWARD",
	} {
		if !containsCall(runner.calls, expected) {
			t.Fatalf("missing command %q in calls %#v", expected, runner.calls)
		}
	}
}

func TestPublishPortsAddsExpectedRules(t *testing.T) {
	cfg := BuildNetworkConfig("vm-port")
	checkCall := "iptables -t nat -C FVC-PREROUTING -p tcp --dport 8080 -j DNAT --to-destination " + cfg.GuestIP + ":80"
	addCall := "iptables -t nat -A FVC-PREROUTING -p tcp --dport 8080 -j DNAT --to-destination " + cfg.GuestIP + ":80"
	runner := &fakeRunner{fail: map[string]bool{checkCall: true}}
	manager := NewNetworkManager(runner)

	if err := manager.PublishPorts(cfg, []string{"8080:80"}); err != nil {
		t.Fatalf("PublishPorts failed: %v", err)
	}
	routeLocalnetCall := "sysctl -w net.ipv4.conf." + cfg.TapName + ".route_localnet=1"
	if !containsCall(runner.calls, routeLocalnetCall) {
		t.Fatalf("expected route_localnet setup %q in calls %#v", routeLocalnetCall, runner.calls)
	}
	if !containsCall(runner.calls, addCall) {
		t.Fatalf("expected publish add rule %q in calls %#v", addCall, runner.calls)
	}
	outputCall := "iptables -t nat -C FVC-OUTPUT -p tcp -o lo --dport 8080 -j DNAT --to-destination " + cfg.GuestIP + ":80"
	if !containsCall(runner.calls, outputCall) {
		t.Fatalf("expected output DNAT check rule %q in calls %#v", outputCall, runner.calls)
	}
	snatCall := "iptables -t nat -C FVC-POSTROUTING -p tcp -d " + cfg.GuestIP + " --dport 80 -j SNAT --to-source " + cfg.HostIP
	if !containsCall(runner.calls, snatCall) {
		t.Fatalf("expected SNAT check rule %q in calls %#v", snatCall, runner.calls)
	}
	forwardCall := "iptables -C FVC-FORWARD -p tcp -d " + cfg.GuestIP + " --dport 80 -j ACCEPT"
	if !containsCall(runner.calls, forwardCall) {
		t.Fatalf("expected forward check rule %q in calls %#v", forwardCall, runner.calls)
	}
}

func TestCleanupPublishedPortsDeletesRules(t *testing.T) {
	cfg := BuildNetworkConfig("vm-clean-port")
	runner := &fakeRunner{}
	manager := NewNetworkManager(runner)

	if err := manager.CleanupPublishedPorts(cfg, []string{"8080:80"}); err != nil {
		t.Fatalf("CleanupPublishedPorts failed: %v", err)
	}
	expected := "iptables -t nat -D FVC-PREROUTING -p tcp --dport 8080 -j DNAT --to-destination " + cfg.GuestIP + ":80"
	if !containsCall(runner.calls, expected) {
		t.Fatalf("expected cleanup rule %q in calls %#v", expected, runner.calls)
	}
	outputExpected := "iptables -t nat -D FVC-OUTPUT -p tcp -o lo --dport 8080 -j DNAT --to-destination " + cfg.GuestIP + ":80"
	if !containsCall(runner.calls, outputExpected) {
		t.Fatalf("expected output DNAT cleanup rule %q in calls %#v", outputExpected, runner.calls)
	}
	snatExpected := "iptables -t nat -D FVC-POSTROUTING -p tcp -d " + cfg.GuestIP + " --dport 80 -j SNAT --to-source " + cfg.HostIP
	if !containsCall(runner.calls, snatExpected) {
		t.Fatalf("expected SNAT cleanup rule %q in calls %#v", snatExpected, runner.calls)
	}
	forwardExpected := "iptables -D FVC-FORWARD -p tcp -d " + cfg.GuestIP + " --dport 80 -j ACCEPT"
	if !containsCall(runner.calls, forwardExpected) {
		t.Fatalf("expected forward cleanup rule %q in calls %#v", forwardExpected, runner.calls)
	}
}

func TestNetworkPermissionHint(t *testing.T) {
	err := WithPermissionHint(fmt.Errorf("ioctl(TUNSETIFF): Operation not permitted"))
	if err == nil || !strings.Contains(err.Error(), "fvcd must run with CAP_NET_ADMIN/root") {
		t.Fatalf("expected permission hint, got %v", err)
	}
}

func TestResolveAllowedHostCommandRejectsUnexpectedCommand(t *testing.T) {
	if _, err := resolveAllowedHostCommand("sh"); err == nil {
		t.Fatal("expected shell command to be rejected")
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
