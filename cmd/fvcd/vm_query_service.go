package main

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/lucaspose/fvc/internal"
	"github.com/lucaspose/fvc/internal/vmstore"
	"github.com/lucaspose/fvc/proto"
)

func processExists(pid int) bool {
	return syscall.Kill(pid, 0) == nil
}

func processMatches(pid int, expectedStartTime string) bool {
	if pid <= 0 || !processExists(pid) {
		return false
	}
	if expectedStartTime == "" {
		return true
	}
	return processStartTimeValue(pid) == expectedStartTime
}

func processStartTimeValue(pid int) string {
	startTime, err := readProcessStartTicks(pid)
	if err != nil {
		return ""
	}
	return strconv.FormatInt(startTime, 10)
}

func readProcessStartTicks(pid int) (int64, error) {
	data, err := os.ReadFile(filepath.Join("/proc", strconv.Itoa(pid), "stat"))
	if err != nil {
		return 0, err
	}
	line := string(data)
	endComm := strings.LastIndex(line, ")")
	if endComm == -1 || endComm+2 >= len(line) {
		return 0, fmt.Errorf("unexpected proc stat format")
	}
	fields := strings.Fields(line[endComm+2:])
	if len(fields) < 20 {
		return 0, fmt.Errorf("unexpected proc stat field count")
	}
	return strconv.ParseInt(fields[19], 10, 64)
}

func (s *Server) Ps(ctx context.Context, req *proto.PsRequest) (*proto.PsResponse, error) {
	records, err := vmstore.ListDetails(s.DB, req != nil && req.All)
	if err != nil {
		return nil, err
	}
	vms := make([]*proto.VmDetails, 0, len(records))
	for _, record := range records {
		vms = append(vms, vmDetailsProto(record))
	}
	return &proto.PsResponse{Vms: vms}, nil
}

func (s *Server) Inspect(ctx context.Context, req *proto.InspectRequest) (*proto.InspectResponse, error) {
	if req == nil || req.VmId == "" {
		return &proto.InspectResponse{Success: false, Message: "vm id is required"}, nil
	}
	vmID, err := s.resolveVMRef(req.VmId)
	if err == sql.ErrNoRows {
		return &proto.InspectResponse{Success: false, Message: fmt.Sprintf("vm not found: %s", req.VmId)}, nil
	}
	if err != nil {
		return &proto.InspectResponse{Success: false, Message: fmt.Sprintf("vm lookup failed: %v", err)}, nil
	}

	vm, err := vmstore.GetDetails(s.DB, vmID)
	if err == sql.ErrNoRows {
		return &proto.InspectResponse{Success: false, Message: fmt.Sprintf("vm not found: %s", req.VmId)}, nil
	}
	if err != nil {
		return &proto.InspectResponse{Success: false, Message: fmt.Sprintf("state lookup failed: %v", err)}, nil
	}
	return &proto.InspectResponse{Success: true, Message: "vm found", Vm: vmDetailsProto(vm)}, nil
}

func (s *Server) Stats(ctx context.Context, req *proto.StatsRequest) (*proto.StatsResponse, error) {
	vmID := ""
	if req != nil && req.VmId != "" {
		resolved, err := s.resolveVMRef(req.VmId)
		if err != nil {
			if err == sql.ErrNoRows {
				return &proto.StatsResponse{}, nil
			}
			return nil, fmt.Errorf("vm lookup failed: %v", err)
		}
		vmID = resolved
	}

	records, err := vmstore.ListStatsRecords(s.DB, vmID)
	if err != nil {
		return nil, err
	}

	stats := make([]*proto.VmStats, 0, len(records))
	for _, record := range records {
		stat := &proto.VmStats{
			VmId:          record.ID,
			Pid:           record.PID,
			Status:        record.Status,
			MemoryMb:      record.MemoryMB,
			UptimeSeconds: record.UptimeSeconds,
		}
		if record.Status == internal.VmRunning && record.PID > 0 && processExists(int(record.PID)) {
			cpuPercent, rssBytes, err := sampleProcessStats(int(record.PID), 100*time.Millisecond)
			if err == nil {
				stat.CpuPercent = cpuPercent
				stat.RssBytes = rssBytes
			}
		}
		stats = append(stats, stat)
	}
	return &proto.StatsResponse{Stats: stats}, nil
}

func (s *Server) Wait(ctx context.Context, req *proto.WaitRequest) (*proto.WaitResponse, error) {
	if req == nil || req.VmId == "" {
		return &proto.WaitResponse{Success: false, Message: "vm id is required"}, nil
	}
	vmID, err := s.resolveVMRef(req.VmId)
	if err == sql.ErrNoRows {
		return &proto.WaitResponse{Success: false, Message: fmt.Sprintf("vm not found: %s", req.VmId)}, nil
	}
	if err != nil {
		return &proto.WaitResponse{Success: false, Message: fmt.Sprintf("vm lookup failed: %v", err)}, nil
	}
	timeout := time.Duration(req.TimeoutSeconds) * time.Second
	var deadline time.Time
	if timeout > 0 {
		deadline = time.Now().Add(timeout)
	}

	for {
		status, err := vmstore.GetWaitStatus(s.DB, vmID)
		if err == sql.ErrNoRows {
			return &proto.WaitResponse{Success: false, Message: fmt.Sprintf("vm not found: %s", req.VmId)}, nil
		}
		if err != nil {
			return &proto.WaitResponse{Success: false, Message: fmt.Sprintf("state lookup failed: %v", err)}, nil
		}
		if status.Status != internal.VmRunning {
			return &proto.WaitResponse{Success: true, Message: fmt.Sprintf("microVM stopped: %s", vmID), Status: status.Status, ExitCode: status.ExitCode}, nil
		}
		if ctx != nil && ctx.Err() != nil {
			return nil, ctx.Err()
		}
		if !deadline.IsZero() && time.Now().After(deadline) {
			return &proto.WaitResponse{Success: false, Message: fmt.Sprintf("wait timeout: %s is still running", vmID), Status: status.Status, ExitCode: status.ExitCode}, nil
		}
		time.Sleep(250 * time.Millisecond)
	}
}

func sampleProcessStats(pid int, interval time.Duration) (float64, int64, error) {
	first, err := readProcessCPUTicks(pid)
	if err != nil {
		return 0, 0, err
	}
	time.Sleep(interval)
	second, err := readProcessCPUTicks(pid)
	if err != nil {
		return 0, 0, err
	}
	rssBytes, err := readProcessRSSBytes(pid)
	if err != nil {
		return 0, 0, err
	}

	const clockTicksPerSecond = 100.0
	deltaTicks := second - first
	if deltaTicks < 0 {
		deltaTicks = 0
	}
	cpuPercent := (float64(deltaTicks) / clockTicksPerSecond / interval.Seconds()) * 100
	return cpuPercent, rssBytes, nil
}

func readProcessCPUTicks(pid int) (int64, error) {
	data, err := os.ReadFile(filepath.Join("/proc", strconv.Itoa(pid), "stat"))
	if err != nil {
		return 0, err
	}
	line := string(data)
	endComm := strings.LastIndex(line, ")")
	if endComm == -1 || endComm+2 >= len(line) {
		return 0, fmt.Errorf("unexpected proc stat format")
	}
	fields := strings.Fields(line[endComm+2:])
	if len(fields) < 13 {
		return 0, fmt.Errorf("unexpected proc stat field count")
	}
	utime, err := strconv.ParseInt(fields[11], 10, 64)
	if err != nil {
		return 0, err
	}
	stime, err := strconv.ParseInt(fields[12], 10, 64)
	if err != nil {
		return 0, err
	}
	return utime + stime, nil
}

func readProcessRSSBytes(pid int) (int64, error) {
	data, err := os.ReadFile(filepath.Join("/proc", strconv.Itoa(pid), "status"))
	if err != nil {
		return 0, err
	}
	for _, line := range strings.Split(string(data), "\n") {
		if !strings.HasPrefix(line, "VmRSS:") {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) < 2 {
			return 0, fmt.Errorf("unexpected VmRSS format")
		}
		kb, err := strconv.ParseInt(fields[1], 10, 64)
		if err != nil {
			return 0, err
		}
		return kb * 1024, nil
	}
	return 0, nil
}

func vmDetailsProto(vm vmstore.Details) *proto.VmDetails {
	return &proto.VmDetails{
		VmId:        vm.ID,
		Name:        vm.Name,
		Pid:         vm.PID,
		Status:      vm.Status,
		Image:       vm.Image,
		GuestIp:     vm.GuestIP,
		MacAddress:  vm.MACAddress,
		TapName:     vm.TapName,
		LogPath:     vm.LogPath,
		DrivePath:   vm.DrivePath,
		ConsolePath: vm.ConsolePath,
		ExitCode:    vm.ExitCode,
		Config: &proto.VmConfig{
			Cpus:     vm.CPUs,
			MemoryMb: vm.MemoryMB,
			Ports:    append([]string(nil), vm.Ports...),
		},
	}
}

func splitStoredPorts(value string) []string {
	return vmstore.SplitPorts(value)
}
