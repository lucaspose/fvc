package main

import (
	"bufio"
	"bytes"
	"context"
	"database/sql"
	"io"
	"os"
	"strings"
	"time"

	"github.com/lucaspose/fvc/internal/vmstore"
	"github.com/lucaspose/fvc/proto"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/timestamppb"
)

func (s *Server) StreamLogs(req *proto.LogsRequest, stream grpc.ServerStreamingServer[proto.LogsResponse]) error {
	if req == nil || req.VmId == "" {
		return status.Error(codes.InvalidArgument, "vm id is required")
	}
	if req.Tail < 0 {
		return status.Error(codes.InvalidArgument, "tail must be zero or greater")
	}

	logPath, err := s.logPathForVM(req.VmId)
	if err != nil {
		return err
	}

	tail := int(req.Tail)
	if tail == 0 && !req.Follow {
		tail = 100
	}

	offset, err := sendTail(logPath, tail, stream)
	if err != nil {
		return err
	}
	if !req.Follow {
		return nil
	}
	return followLog(logPath, offset, stream)
}

func (s *Server) Diagnostics(ctx context.Context, req *proto.DiagnosticsRequest) (*proto.DiagnosticsResponse, error) {
	checks := RuntimeDiagnostics(s.Config)
	res := &proto.DiagnosticsResponse{
		Checks:         make([]*proto.DiagnosticCheck, 0, len(checks)),
		GrpcNetwork:    s.Config.GRPCNetwork,
		GrpcAddress:    s.Config.GRPCAddr,
		DataDir:        s.Config.BaseDir,
		RuntimeDir:     s.Config.RuntimeDir,
		NetworkEnabled: s.Config.NetworkEnabled,
	}
	for _, check := range checks {
		res.Checks = append(res.Checks, &proto.DiagnosticCheck{
			Name:    check.Name,
			Ok:      check.OK,
			Message: check.Message,
		})
	}
	return res, nil
}

func (s *Server) logPathForVM(vmID string) (string, error) {
	resolved, err := s.resolveVMRef(vmID)
	if err == sql.ErrNoRows {
		return "", status.Errorf(codes.NotFound, "vm %s not found", vmID)
	}
	if err != nil {
		return "", status.Errorf(codes.Internal, "failed to resolve vm %s: %v", vmID, err)
	}
	logPath, err := vmstore.LogPath(s.DB, resolved)
	if err == sql.ErrNoRows {
		return "", status.Errorf(codes.NotFound, "vm %s not found", vmID)
	}
	if err != nil {
		return "", status.Errorf(codes.Internal, "failed to read vm log path: %v", err)
	}
	if logPath == "" {
		return "", status.Errorf(codes.NotFound, "vm %s has no log file registered", vmID)
	}
	return logPath, nil
}

func sendTail(path string, tail int, stream grpc.ServerStreamingServer[proto.LogsResponse]) (int64, error) {
	lines, offset, err := readTailLines(path, tail)
	if err != nil {
		if os.IsNotExist(err) {
			return 0, status.Errorf(codes.NotFound, "log file not found: %s", path)
		}
		return 0, status.Errorf(codes.Internal, "failed to read log file: %v", err)
	}
	for _, line := range lines {
		if err := sendLogLine(stream, line); err != nil {
			return offset, err
		}
	}
	return offset, nil
}

func readTailLines(path string, tail int) ([]string, int64, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, 0, err
	}
	offset := int64(len(data))
	lines := splitLogLines(string(data))
	if tail > 0 && len(lines) > tail {
		lines = lines[len(lines)-tail:]
	}
	return lines, offset, nil
}

func splitLogLines(data string) []string {
	scanner := bufio.NewScanner(bytes.NewBufferString(data))
	lines := make([]string, 0)
	for scanner.Scan() {
		lines = append(lines, scanner.Text())
	}
	return lines
}

func followLog(path string, offset int64, stream grpc.ServerStreamingServer[proto.LogsResponse]) error {
	ticker := time.NewTicker(250 * time.Millisecond)
	defer ticker.Stop()

	for {
		select {
		case <-stream.Context().Done():
			return stream.Context().Err()
		case <-ticker.C:
			nextOffset, err := sendNewLogLines(path, offset, stream)
			if err != nil {
				return err
			}
			offset = nextOffset
		}
	}
}

func sendNewLogLines(path string, offset int64, stream grpc.ServerStreamingServer[proto.LogsResponse]) (int64, error) {
	file, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			return offset, status.Errorf(codes.NotFound, "log file not found: %s", path)
		}
		return offset, status.Errorf(codes.Internal, "failed to open log file: %v", err)
	}
	defer file.Close()

	info, err := file.Stat()
	if err != nil {
		return offset, status.Errorf(codes.Internal, "failed to stat log file: %v", err)
	}
	if info.Size() < offset {
		offset = 0
	}
	if _, err := file.Seek(offset, io.SeekStart); err != nil {
		return offset, status.Errorf(codes.Internal, "failed to seek log file: %v", err)
	}

	reader := bufio.NewReader(file)
	for {
		line, err := reader.ReadString('\n')
		if line != "" {
			line = strings.TrimRight(line, "\r\n")
			if err := sendLogLine(stream, line); err != nil {
				return offset, err
			}
		}
		current, seekErr := file.Seek(0, io.SeekCurrent)
		if seekErr == nil {
			offset = current
		}
		if err == io.EOF {
			break
		}
		if err != nil {
			return offset, status.Errorf(codes.Internal, "failed to read log file: %v", err)
		}
	}
	return offset, nil
}

func sendLogLine(stream grpc.ServerStreamingServer[proto.LogsResponse], line string) error {
	return stream.Send(&proto.LogsResponse{
		Line:      line,
		Timestamp: timestamppb.Now(),
	})
}
