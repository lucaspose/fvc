package main

import (
	"crypto/sha1"
	"encoding/hex"
	"fmt"
	"os/exec"
	"strings"
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

type realCommandRunner struct{}

func (realCommandRunner) Run(name string, args ...string) error {
	output, err := exec.Command(name, args...).CombinedOutput()
	if err != nil {
		return fmt.Errorf("%s %v failed: %v: %s", name, args, err, string(output))
	}
	return nil
}

type NetworkManager struct {
	runner CommandRunner
}

func NewNetworkManager(runner CommandRunner) *NetworkManager {
	if runner == nil {
		runner = realCommandRunner{}
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
		return cfg, fmt.Errorf("tap create failed: %w", withNetworkPermissionHint(err))
	}
	if err := n.runner.Run("ip", "addr", "replace", cfg.HostIP+"/30", "dev", cfg.TapName); err != nil {
		_ = n.Cleanup(cfg)
		return cfg, fmt.Errorf("tap address setup failed: %w", err)
	}
	if err := n.runner.Run("ip", "link", "set", cfg.TapName, "up"); err != nil {
		_ = n.Cleanup(cfg)
		return cfg, fmt.Errorf("tap activation failed: %w", err)
	}
	if err := n.runner.Run("sysctl", "-w", "net.ipv4.ip_forward=1"); err != nil {
		_ = n.Cleanup(cfg)
		return cfg, fmt.Errorf("ip forwarding setup failed: %w", err)
	}
	if err := n.runner.Run("iptables", "-t", "nat", "-C", "POSTROUTING", "-s", cfg.GuestIP+"/32", "-j", "MASQUERADE"); err != nil {
		if addErr := n.runner.Run("iptables", "-t", "nat", "-A", "POSTROUTING", "-s", cfg.GuestIP+"/32", "-j", "MASQUERADE"); addErr != nil {
			_ = n.Cleanup(cfg)
			return cfg, fmt.Errorf("nat setup failed: %w", addErr)
		}
	}
	return cfg, nil
}

func withNetworkPermissionHint(err error) error {
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
	_ = n.runner.Run("iptables", "-t", "nat", "-D", "POSTROUTING", "-s", cfg.GuestIP+"/32", "-j", "MASQUERADE")
	_ = n.runner.Run("ip", "link", "del", cfg.TapName)
	return nil
}
