package hostnet

import (
	"crypto/sha1"
	"encoding/hex"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/lucaspose/fvc/internal"
)

const (
	fvcPreroutingChain  = "FVC-PREROUTING"
	fvcOutputChain      = "FVC-OUTPUT"
	fvcPostroutingChain = "FVC-POSTROUTING"
	fvcForwardChain     = "FVC-FORWARD"
)

type NetworkConfig struct {
	TapName string
	HostIP  string
	GuestIP string
	Netmask string
	MAC     string
}

type CommandRunner interface {
	Run(name string, args ...string) error
}

type ExecRunner struct{}

var allowedHostCommands = map[string]bool{
	"ip":        true,
	"iptables":  true,
	"mkfs.ext4": true,
	"mount":     true,
	"sysctl":    true,
	"truncate":  true,
	"umount":    true,
}

func (ExecRunner) Run(name string, args ...string) error {
	path, err := resolveAllowedHostCommand(name)
	if err != nil {
		return err
	}
	output, err := exec.Command(path, args...).CombinedOutput()
	if err != nil {
		return fmt.Errorf("%s %v failed: %v: %s", name, args, err, string(output))
	}
	return nil
}

func resolveAllowedHostCommand(name string) (string, error) {
	base := filepath.Base(name)
	if !allowedHostCommands[base] {
		return "", fmt.Errorf("host command %q is not allowed", name)
	}
	path, err := exec.LookPath(name)
	if err != nil {
		return "", err
	}
	return path, nil
}

type NetworkManager struct {
	runner CommandRunner
}

func NewNetworkManager(runner CommandRunner) *NetworkManager {
	if runner == nil {
		runner = ExecRunner{}
	}
	return &NetworkManager{runner: runner}
}

func BuildNetworkConfig(vmID string) NetworkConfig {
	sum := sha1.Sum([]byte(vmID))
	third := 16 + int(sum[0])%16
	block := int(sum[1]) % 64
	base := block * 4
	short := hex.EncodeToString(sum[:])[:11]

	return NetworkConfig{
		TapName: fmt.Sprintf("fvc%s", short),
		HostIP:  fmt.Sprintf("172.%d.%d.%d", third, int(sum[2]), base+1),
		GuestIP: fmt.Sprintf("172.%d.%d.%d", third, int(sum[2]), base+2),
		Netmask: "255.255.255.252",
		MAC:     fmt.Sprintf("02:FC:%02X:%02X:%02X:%02X", sum[3], sum[4], sum[5], sum[6]),
	}
}

func (n *NetworkManager) Setup(vmID string) (NetworkConfig, error) {
	cfg := BuildNetworkConfig(vmID)
	if err := n.runner.Run("ip", "tuntap", "add", "dev", cfg.TapName, "mode", "tap"); err != nil {
		return cfg, fmt.Errorf("tap create failed: %w", WithPermissionHint(err))
	}
	if err := n.runner.Run("ip", "addr", "replace", cfg.HostIP+"/30", "dev", cfg.TapName); err != nil {
		_ = n.Cleanup(cfg)
		return cfg, fmt.Errorf("tap address setup failed: %w", err)
	}
	if err := n.runner.Run("ip", "link", "set", cfg.TapName, "up"); err != nil {
		_ = n.Cleanup(cfg)
		return cfg, fmt.Errorf("tap activation failed: %w", err)
	}
	if err := n.ensureIPForwarding(); err != nil {
		_ = n.Cleanup(cfg)
		return cfg, fmt.Errorf("ip forwarding setup failed: %w", err)
	}
	if err := n.ensureBaseChains(); err != nil {
		_ = n.Cleanup(cfg)
		return cfg, err
	}
	if err := n.runner.Run("iptables", "-t", "nat", "-C", fvcPostroutingChain, "-s", cfg.GuestIP+"/32", "-j", "MASQUERADE"); err != nil {
		if addErr := n.runner.Run("iptables", "-t", "nat", "-A", fvcPostroutingChain, "-s", cfg.GuestIP+"/32", "-j", "MASQUERADE"); addErr != nil {
			_ = n.Cleanup(cfg)
			return cfg, fmt.Errorf("nat setup failed: %w", addErr)
		}
	}
	return cfg, nil
}

func (n *NetworkManager) ensureIPForwarding() error {
	enabled, err := ipForwardingEnabled()
	if err == nil && enabled {
		return nil
	}
	if err := n.runner.Run("sysctl", "-w", "net.ipv4.ip_forward=1"); err != nil {
		return err
	}
	return nil
}

func ipForwardingEnabled() (bool, error) {
	data, err := os.ReadFile("/proc/sys/net/ipv4/ip_forward")
	if err != nil {
		return false, err
	}
	return strings.TrimSpace(string(data)) == "1", nil
}

func (n *NetworkManager) ensureBaseChains() error {
	natHooks := []struct {
		hook  string
		chain string
	}{
		{hook: "PREROUTING", chain: fvcPreroutingChain},
		{hook: "OUTPUT", chain: fvcOutputChain},
		{hook: "POSTROUTING", chain: fvcPostroutingChain},
	}
	for _, hook := range natHooks {
		if err := n.ensureChain("nat", hook.chain); err != nil {
			return err
		}
		if err := n.ensureJump("nat", hook.hook, hook.chain); err != nil {
			return err
		}
	}
	if err := n.ensureChain("", fvcForwardChain); err != nil {
		return err
	}
	return n.ensureJump("", "FORWARD", fvcForwardChain)
}

func (n *NetworkManager) ensureChain(table, chain string) error {
	args := iptablesArgs(table, "-L", chain)
	if err := n.runner.Run("iptables", args...); err == nil {
		return nil
	}
	args = iptablesArgs(table, "-N", chain)
	if err := n.runner.Run("iptables", args...); err != nil {
		return fmt.Errorf("iptables chain %s setup failed: %w", chain, err)
	}
	return nil
}

func (n *NetworkManager) ensureJump(table, from, to string) error {
	check := iptablesArgs(table, "-C", from, "-j", to)
	if err := n.runner.Run("iptables", check...); err == nil {
		return nil
	}
	// Append, never insert: rules already in the host chains (firewall
	// policies, DROP rules) keep precedence over FVC rules.
	add := iptablesArgs(table, "-A", from, "-j", to)
	if err := n.runner.Run("iptables", add...); err != nil {
		return fmt.Errorf("iptables jump %s -> %s setup failed: %w", from, to, err)
	}
	return nil
}

func iptablesArgs(table string, args ...string) []string {
	if table == "" {
		return args
	}
	out := []string{"-t", table}
	out = append(out, args...)
	return out
}

func WithPermissionHint(err error) error {
	if err == nil {
		return nil
	}
	message := err.Error()
	if strings.Contains(message, "Operation not permitted") || strings.Contains(message, "permission denied") {
		return fmt.Errorf("%w; fvcd must run with CAP_NET_ADMIN/root and /dev/net/tun must be available", err)
	}
	return err
}

func (n *NetworkManager) Cleanup(cfg NetworkConfig) error {
	if cfg.TapName == "" {
		return nil
	}
	_ = n.runner.Run("iptables", "-t", "nat", "-D", fvcPostroutingChain, "-s", cfg.GuestIP+"/32", "-j", "MASQUERADE")
	_ = n.runner.Run("ip", "link", "del", cfg.TapName)
	return nil
}

func (n *NetworkManager) PublishPorts(cfg NetworkConfig, ports []string) error {
	if len(ports) > 0 {
		if err := n.ensureBaseChains(); err != nil {
			return err
		}
		if err := n.runner.Run("sysctl", "-w", "net.ipv4.conf."+cfg.TapName+".route_localnet=1"); err != nil {
			return fmt.Errorf("port publish route_localnet setup failed: %w", err)
		}
	}
	for _, spec := range ports {
		hostPort, guestPort, err := internal.ParsePortSpec(spec)
		if err != nil {
			return err
		}
		if err := n.ensurePortRule(cfg, hostPort, guestPort); err != nil {
			_ = n.CleanupPublishedPorts(cfg, ports)
			return err
		}
	}
	return nil
}

func (n *NetworkManager) ensurePortRule(cfg NetworkConfig, hostPort, guestPort int) error {
	host := strconv.Itoa(hostPort)
	guest := fmt.Sprintf("%s:%d", cfg.GuestIP, guestPort)
	rules := [][]string{
		{"-t", "nat", "-C", fvcPreroutingChain, "-p", "tcp", "--dport", host, "-j", "DNAT", "--to-destination", guest},
		{"-t", "nat", "-C", fvcOutputChain, "-p", "tcp", "-o", "lo", "--dport", host, "-j", "DNAT", "--to-destination", guest},
		{"-t", "nat", "-C", fvcPostroutingChain, "-p", "tcp", "-d", cfg.GuestIP, "--dport", strconv.Itoa(guestPort), "-j", "SNAT", "--to-source", cfg.HostIP},
		{"-C", fvcForwardChain, "-p", "tcp", "-d", cfg.GuestIP, "--dport", strconv.Itoa(guestPort), "-j", "ACCEPT"},
	}
	addRules := [][]string{
		{"-t", "nat", "-A", fvcPreroutingChain, "-p", "tcp", "--dport", host, "-j", "DNAT", "--to-destination", guest},
		{"-t", "nat", "-A", fvcOutputChain, "-p", "tcp", "-o", "lo", "--dport", host, "-j", "DNAT", "--to-destination", guest},
		{"-t", "nat", "-A", fvcPostroutingChain, "-p", "tcp", "-d", cfg.GuestIP, "--dport", strconv.Itoa(guestPort), "-j", "SNAT", "--to-source", cfg.HostIP},
		{"-A", fvcForwardChain, "-p", "tcp", "-d", cfg.GuestIP, "--dport", strconv.Itoa(guestPort), "-j", "ACCEPT"},
	}
	for i, check := range rules {
		if err := n.runner.Run("iptables", check...); err != nil {
			if addErr := n.runner.Run("iptables", addRules[i]...); addErr != nil {
				return fmt.Errorf("port publish failed for %d:%d: %w", hostPort, guestPort, addErr)
			}
		}
	}
	return nil
}

func (n *NetworkManager) CleanupPublishedPorts(cfg NetworkConfig, ports []string) error {
	for _, spec := range ports {
		hostPort, guestPort, err := internal.ParsePortSpec(spec)
		if err != nil {
			continue
		}
		host := strconv.Itoa(hostPort)
		guest := fmt.Sprintf("%s:%d", cfg.GuestIP, guestPort)
		_ = n.runner.Run("iptables", "-t", "nat", "-D", fvcPreroutingChain, "-p", "tcp", "--dport", host, "-j", "DNAT", "--to-destination", guest)
		_ = n.runner.Run("iptables", "-t", "nat", "-D", fvcOutputChain, "-p", "tcp", "-o", "lo", "--dport", host, "-j", "DNAT", "--to-destination", guest)
		_ = n.runner.Run("iptables", "-t", "nat", "-D", fvcPostroutingChain, "-p", "tcp", "-d", cfg.GuestIP, "--dport", strconv.Itoa(guestPort), "-j", "SNAT", "--to-source", cfg.HostIP)
		_ = n.runner.Run("iptables", "-D", fvcForwardChain, "-p", "tcp", "-d", cfg.GuestIP, "--dport", strconv.Itoa(guestPort), "-j", "ACCEPT")
	}
	return nil
}
