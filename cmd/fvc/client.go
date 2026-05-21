package main

import (
	"fmt"
	"net"
	"os"
	"strings"
	"time"

	"github.com/lucaspose/fvc/proto"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

func newClient() (proto.FvcServiceClient, *grpc.ClientConn, error) {
	target := grpcTarget()
	conn, err := grpc.NewClient(target, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		return nil, nil, fmt.Errorf("daemon is not reachable at %s: %w", target, err)
	}
	client := proto.NewFvcServiceClient(conn)
	return client, conn, nil
}

func grpcTarget() string {
	if target := strings.TrimSpace(os.Getenv("FVC_GRPC_TARGET")); target != "" {
		return target
	}
	network := strings.TrimSpace(os.Getenv("FVC_GRPC_NETWORK"))
	addr := strings.TrimSpace(os.Getenv("FVC_GRPC_ADDR"))
	if network == "unix" {
		if addr == "" {
			addr = "/run/fvc/fvcd.sock"
		}
		return "unix://" + addr
	}
	if network == "tcp" {
		if addr == "" {
			addr = "127.0.0.1:50051"
		}
		return addr
	}
	if addr != "" {
		if strings.HasPrefix(addr, "/") {
			return "unix://" + addr
		}
		return addr
	}
	if _, err := os.Stat("/run/fvc/fvcd.sock"); err == nil {
		return "unix:///run/fvc/fvcd.sock"
	}
	return "127.0.0.1:50051"
}

func daemonReachable(target string) bool {
	conn, err := net.DialTimeout(targetNetwork(target), targetAddress(target), 500*time.Millisecond)
	if err != nil {
		return false
	}
	_ = conn.Close()
	return true
}

func targetNetwork(target string) string {
	if strings.HasPrefix(target, "unix://") {
		return "unix"
	}
	return "tcp"
}

func targetAddress(target string) string {
	return strings.TrimPrefix(target, "unix://")
}
