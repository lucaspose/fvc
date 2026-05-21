package main

import (
	"strings"
	"testing"

	"github.com/lucaspose/fvc/proto"
)

func TestValidateExecRequest(t *testing.T) {
	req, err := validateExecRequest(&proto.ExecRequest{
		VmId:    "vm-1",
		Command: []string{"/bin/echo", "hello"},
		Env:     []string{"A=B"},
		Workdir: "/tmp",
	})
	if err != nil {
		t.Fatalf("validateExecRequest failed: %v", err)
	}
	if req.VmId != "vm-1" || req.Command[0] != "/bin/echo" || req.Env[0] != "A=B" || req.Workdir != "/tmp" {
		t.Fatalf("unexpected request: %#v", req)
	}
}

func TestValidateExecRequestRejectsBadInput(t *testing.T) {
	tests := []struct {
		name string
		req  *proto.ExecRequest
		want string
	}{
		{name: "missing vm", req: &proto.ExecRequest{Command: []string{"true"}}, want: "vm id"},
		{name: "missing command", req: &proto.ExecRequest{VmId: "vm-1"}, want: "command"},
		{name: "empty arg", req: &proto.ExecRequest{VmId: "vm-1", Command: []string{" "}}, want: "invalid argument"},
		{name: "bad env", req: &proto.ExecRequest{VmId: "vm-1", Command: []string{"true"}, Env: []string{"=bad"}}, want: "invalid exec env"},
		{name: "relative workdir", req: &proto.ExecRequest{VmId: "vm-1", Command: []string{"true"}, Workdir: "tmp"}, want: "absolute"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := validateExecRequest(tt.req)
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("expected %q error, got %v", tt.want, err)
			}
		})
	}
}

func TestGuestAgentURL(t *testing.T) {
	if got := guestAgentURL(guestAgentEndpoint{Transport: "tcp", GuestIP: "172.16.0.2", Port: 9100}, "/exec"); got != "http://172.16.0.2:9100/exec" {
		t.Fatalf("unexpected URL: %s", got)
	}
	if got := guestAgentURL(guestAgentEndpoint{Transport: "vsock", VSockPath: "/run/fvc/vm.vsock", Port: 9100}, "/exec"); got != "http://fvc-vsock/exec" {
		t.Fatalf("unexpected URL: %s", got)
	}
}
