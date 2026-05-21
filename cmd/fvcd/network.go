package main

import (
	"github.com/lucaspose/fvc/internal/hostnet"
)

type NetworkConfig = hostnet.NetworkConfig
type CommandRunner = hostnet.CommandRunner
type NetworkManager = hostnet.NetworkManager

type realCommandRunner = hostnet.ExecRunner

func NewNetworkManager(runner CommandRunner) *NetworkManager {
	return hostnet.NewNetworkManager(runner)
}

func BuildNetworkConfig(vmID string) NetworkConfig {
	return hostnet.BuildNetworkConfig(vmID)
}

func withNetworkPermissionHint(err error) error {
	return hostnet.WithPermissionHint(err)
}
