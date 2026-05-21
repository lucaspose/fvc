package main

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"strings"
	"time"

	"github.com/lucaspose/fvc/internal/fcvsock"
)

const guestAgentVsockPort = fcvsock.GuestAgentPort

type guestAgentEndpoint struct {
	Transport string
	GuestIP   string
	VSockPath string
	Port      int
}

func (e guestAgentEndpoint) available() bool {
	switch e.Transport {
	case "vsock":
		return strings.TrimSpace(e.VSockPath) != "" && e.Port > 0
	case "tcp":
		return net.ParseIP(e.GuestIP) != nil && e.Port > 0
	default:
		return false
	}
}

func (e guestAgentEndpoint) label() string {
	switch e.Transport {
	case "vsock":
		return fmt.Sprintf("vsock:%s:%d", e.VSockPath, e.Port)
	case "tcp":
		return fmt.Sprintf("%s:%d", e.GuestIP, e.Port)
	default:
		return "unavailable"
	}
}

func guestAgentEndpointFor(mode, guestIP, vsockPath string) guestAgentEndpoint {
	mode = strings.ToLower(strings.TrimSpace(mode))
	switch mode {
	case "", "vsock":
		if strings.TrimSpace(vsockPath) != "" {
			return guestAgentEndpoint{Transport: "vsock", VSockPath: vsockPath, Port: guestAgentVsockPort}
		}
	case "tcp":
		return guestAgentEndpoint{Transport: "tcp", GuestIP: guestIP, Port: guestExecAgentPort}
	case "auto":
		if net.ParseIP(strings.TrimSpace(guestIP)) != nil {
			return guestAgentEndpoint{Transport: "tcp", GuestIP: guestIP, Port: guestExecAgentPort}
		}
		if strings.TrimSpace(vsockPath) != "" {
			return guestAgentEndpoint{Transport: "vsock", VSockPath: vsockPath, Port: guestAgentVsockPort}
		}
	}
	return guestAgentEndpoint{}
}

func shouldConfigureVsock(mode string) bool {
	return fcvsock.ShouldConfigure(mode)
}

func guestAgentCID(vmID string) uint32 {
	return fcvsock.GuestCID(vmID)
}

func guestAgentHTTPClient(endpoint guestAgentEndpoint) *http.Client {
	transport := &http.Transport{
		MaxIdleConns:        16,
		MaxIdleConnsPerHost: 4,
		IdleConnTimeout:     30 * time.Second,
	}
	if endpoint.Transport == "vsock" {
		transport.DialContext = func(ctx context.Context, _, _ string) (net.Conn, error) {
			return dialFirecrackerVsock(ctx, endpoint.VSockPath, endpoint.Port)
		}
	} else {
		transport.DialContext = (&net.Dialer{
			Timeout:   2 * time.Second,
			KeepAlive: 30 * time.Second,
		}).DialContext
	}
	return &http.Client{Transport: transport}
}

func dialFirecrackerVsock(ctx context.Context, udsPath string, port int) (net.Conn, error) {
	return fcvsock.Dial(ctx, udsPath, port)
}

func configureFirecrackerVsock(socketPath, udsPath string, guestCID uint32) error {
	return fcvsock.Configure(socketPath, udsPath, guestCID)
}

func vsockConfigPayload(udsPath string, guestCID uint32) string {
	return fcvsock.ConfigPayload(udsPath, guestCID)
}
