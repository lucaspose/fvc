package main

import (
	"fmt"
	"time"

	"github.com/lucaspose/fvc/internal/fcapi"
)

func sendFCConfig(socketPath, method, path, jsonBody string) error {
	return fcapi.SendConfig(socketPath, method, path, jsonBody)
}

func waitForSocket(socketPath string, timeout time.Duration) error {
	return fcapi.WaitForSocket(socketPath, timeout)
}

func bootArgs(cfg *NetworkConfig) string {
	args := "console=ttyS0 reboot=k panic=1 pci=off random.trust_cpu=on"
	if cfg == nil {
		return args
	}
	return fmt.Sprintf("%s ip=%s::%s:%s::eth0:off", args, cfg.GuestIP, cfg.HostIP, cfg.Netmask)
}

func configureFirecrackerNetwork(socketPath string, cfg NetworkConfig) error {
	if err := fcapi.ConfigureNetwork(socketPath, "eth0", cfg.MAC, cfg.TapName); err != nil {
		return fmt.Errorf("network config failed: %w", err)
	}
	return nil
}
