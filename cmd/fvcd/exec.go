package main

import (
	"bufio"
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"path/filepath"
	"strings"
	"time"

	"github.com/lucaspose/fvc/internal"
	"github.com/lucaspose/fvc/internal/vmstore"
	"github.com/lucaspose/fvc/proto"
	"google.golang.org/grpc"
)

const guestExecAgentPort = 9100

type agentExecRequest struct {
	Command []string `json:"command"`
	Env     []string `json:"env,omitempty"`
	Workdir string   `json:"workdir,omitempty"`
}

type agentExecEvent struct {
	Stream       string `json:"stream,omitempty"`
	Data         []byte `json:"data,omitempty"`
	ExitCode     int32  `json:"exit_code,omitempty"`
	ErrorMessage string `json:"error_message,omitempty"`
}

func (s *Server) Exec(req *proto.ExecRequest, stream grpc.ServerStreamingServer[proto.ExecEvent]) error {
	execReq, err := validateExecRequest(req)
	if err != nil {
		return stream.Send(&proto.ExecEvent{Stream: "exit", ExitCode: 127, ErrorMessage: err.Error()})
	}
	vmID, err := s.resolveVMRef(execReq.VmId)
	if err == sql.ErrNoRows {
		return stream.Send(&proto.ExecEvent{Stream: "exit", ExitCode: 127, ErrorMessage: fmt.Sprintf("vm not found: %s", execReq.VmId)})
	}
	if err != nil {
		return stream.Send(&proto.ExecEvent{Stream: "exit", ExitCode: 127, ErrorMessage: fmt.Sprintf("vm lookup failed: %v", err)})
	}

	state, err := vmstore.GetExecAgentState(s.DB, vmID)
	if err == sql.ErrNoRows {
		return stream.Send(&proto.ExecEvent{Stream: "exit", ExitCode: 127, ErrorMessage: fmt.Sprintf("vm not found: %s", execReq.VmId)})
	}
	if err != nil {
		return stream.Send(&proto.ExecEvent{Stream: "exit", ExitCode: 127, ErrorMessage: fmt.Sprintf("state lookup failed: %v", err)})
	}
	if state.Status != internal.VmRunning {
		return stream.Send(&proto.ExecEvent{Stream: "exit", ExitCode: 126, ErrorMessage: fmt.Sprintf("microVM is not running: %s", state.Status)})
	}
	endpoint := guestAgentEndpointFor(s.Config.GuestAgentMode, state.GuestIP, state.VsockPath)
	if !endpoint.available() || strings.TrimSpace(state.AgentToken) == "" {
		return stream.Send(&proto.ExecEvent{Stream: "exit", ExitCode: 126, ErrorMessage: "guest agent is unavailable for this microVM"})
	}

	if err := s.waitForGuestAgent(stream.Context(), endpoint, state.AgentToken, 5*time.Second); err != nil {
		return stream.Send(&proto.ExecEvent{Stream: "exit", ExitCode: 126, ErrorMessage: err.Error()})
	}
	return s.streamGuestExec(stream.Context(), endpoint, state.AgentToken, agentExecRequest{
		Command: execReq.Command,
		Env:     execReq.Env,
		Workdir: execReq.Workdir,
	}, stream)
}

func validateExecRequest(req *proto.ExecRequest) (*proto.ExecRequest, error) {
	if req == nil {
		return nil, fmt.Errorf("exec request is required")
	}
	vmID := strings.TrimSpace(req.GetVmId())
	if vmID == "" {
		return nil, fmt.Errorf("vm id is required")
	}
	if len(req.GetCommand()) == 0 {
		return nil, fmt.Errorf("exec command is required")
	}
	command := make([]string, 0, len(req.GetCommand()))
	for _, arg := range req.GetCommand() {
		if strings.TrimSpace(arg) == "" || strings.Contains(arg, "\x00") {
			return nil, fmt.Errorf("exec command contains an invalid argument")
		}
		command = append(command, arg)
	}
	env := make([]string, 0, len(req.GetEnv()))
	for _, entry := range req.GetEnv() {
		if !strings.Contains(entry, "=") || strings.HasPrefix(entry, "=") || strings.ContainsAny(entry, "\x00\r\n") {
			return nil, fmt.Errorf("invalid exec env entry: %q", entry)
		}
		env = append(env, entry)
	}
	workdir := strings.TrimSpace(req.GetWorkdir())
	if workdir != "" && (!filepath.IsAbs(workdir) || strings.Contains(workdir, "\x00")) {
		return nil, fmt.Errorf("exec workdir must be an absolute path")
	}
	return &proto.ExecRequest{VmId: vmID, Command: command, Env: env, Workdir: workdir}, nil
}

func (s *Server) waitForGuestAgent(ctx context.Context, endpoint guestAgentEndpoint, token string, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if err := guestAgentHealth(ctx, endpoint, token); err == nil {
			return nil
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("guest agent unavailable at %s", endpoint.label())
		}
		time.Sleep(100 * time.Millisecond)
	}
}

func guestAgentHealth(ctx context.Context, endpoint guestAgentEndpoint, token string) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, guestAgentURL(endpoint, "/health"), nil)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err := guestAgentHTTPClient(endpoint).Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNoContent {
		return fmt.Errorf("guest agent health returned %s", resp.Status)
	}
	return nil
}

func (s *Server) streamGuestExec(ctx context.Context, endpoint guestAgentEndpoint, token string, execReq agentExecRequest, stream grpc.ServerStreamingServer[proto.ExecEvent]) error {
	body, err := json.Marshal(execReq)
	if err != nil {
		return stream.Send(&proto.ExecEvent{Stream: "exit", ExitCode: 127, ErrorMessage: fmt.Sprintf("exec request encode failed: %v", err)})
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, guestAgentURL(endpoint, "/exec"), bytes.NewReader(body))
	if err != nil {
		return stream.Send(&proto.ExecEvent{Stream: "exit", ExitCode: 127, ErrorMessage: fmt.Sprintf("exec request build failed: %v", err)})
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")

	resp, err := guestAgentHTTPClient(endpoint).Do(req)
	if err != nil {
		return stream.Send(&proto.ExecEvent{Stream: "exit", ExitCode: 126, ErrorMessage: fmt.Sprintf("guest exec request failed: %v", err)})
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return stream.Send(&proto.ExecEvent{Stream: "exit", ExitCode: 126, ErrorMessage: fmt.Sprintf("guest exec rejected: %s", resp.Status)})
	}

	scanner := bufio.NewScanner(resp.Body)
	scanner.Buffer(make([]byte, 64*1024), 1024*1024)
	for scanner.Scan() {
		var event agentExecEvent
		if err := json.Unmarshal(scanner.Bytes(), &event); err != nil {
			return stream.Send(&proto.ExecEvent{Stream: "exit", ExitCode: 127, ErrorMessage: fmt.Sprintf("guest exec event parse failed: %v", err)})
		}
		if event.Stream == "" {
			continue
		}
		if err := stream.Send(&proto.ExecEvent{
			Stream:       event.Stream,
			Data:         event.Data,
			ExitCode:     event.ExitCode,
			ErrorMessage: event.ErrorMessage,
		}); err != nil {
			return err
		}
	}
	if err := scanner.Err(); err != nil {
		return stream.Send(&proto.ExecEvent{Stream: "exit", ExitCode: 126, ErrorMessage: fmt.Sprintf("guest exec stream failed: %v", err)})
	}
	return nil
}

func guestAgentURL(endpoint guestAgentEndpoint, path string) string {
	if endpoint.Transport == "vsock" {
		return "http://fvc-vsock" + path
	}
	return fmt.Sprintf("http://%s:%d%s", endpoint.GuestIP, endpoint.Port, path)
}
