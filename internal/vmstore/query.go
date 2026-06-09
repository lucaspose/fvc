package vmstore

import (
	"database/sql"
	"fmt"
	"strings"
	"time"

	"github.com/lucaspose/fvc/internal"
)

// Details contains the persisted VM fields needed by ps and inspect.
type Details struct {
	ID          string
	Name        string
	PID         int32
	Status      string
	Image       string
	CPUs        int32
	MemoryMB    int32
	Ports       []string
	NetworkMode string
	Volumes     []string
	GuestIP     string
	MACAddress  string
	TapName     string
	LogPath     string
	DrivePath   string
	ConsolePath string
	ExitCode    int32
}

// StatsRecord contains persisted VM fields needed before host process sampling.
type StatsRecord struct {
	ID            string
	PID           int32
	Status        string
	MemoryMB      int32
	UptimeSeconds int64
}

// WaitStatus is the minimal status tuple used by wait loops.
type WaitStatus struct {
	Status   string
	ExitCode int32
}

type detailsScanner interface {
	Scan(dest ...any) error
}

// DetailsSelect returns the canonical SELECT fragment for VM details rows.
func DetailsSelect() string {
	return `SELECT id, COALESCE(name, ''), pid, status, image, cpus, memory_mb, COALESCE(ports, ''), COALESCE(network_mode, ''), COALESCE(volumes, ''), COALESCE(guest_ip, ''), COALESCE(mac_address, ''), COALESCE(tap_name, ''), COALESCE(log_path, ''), COALESCE(drive_path, ''), COALESCE(console_path, ''), COALESCE(exit_code, -1) FROM vms`
}

// ListDetails returns VM details, optionally restricted to running VMs.
func ListDetails(db *sql.DB, all bool) ([]Details, error) {
	query := DetailsSelect()
	if !all {
		query += " WHERE status = 'running'"
	}
	rows, err := db.Query(query)
	if err != nil {
		return nil, fmt.Errorf("failed to query vms: %v", err)
	}
	defer rows.Close()

	var vms []Details
	for rows.Next() {
		vm, err := ScanDetails(rows)
		if err != nil {
			return nil, err
		}
		vms = append(vms, vm)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("failed to read vm rows: %v", err)
	}
	return vms, nil
}

// GetDetails returns details for one VM id.
func GetDetails(db *sql.DB, id string) (Details, error) {
	return ScanDetails(db.QueryRow(DetailsSelect()+" WHERE id = ?", id))
}

// ScanDetails scans one Details row from DetailsSelect.
func ScanDetails(scanner detailsScanner) (Details, error) {
	var vm Details
	var portsValue string
	var volumesValue string
	if err := scanner.Scan(&vm.ID, &vm.Name, &vm.PID, &vm.Status, &vm.Image, &vm.CPUs, &vm.MemoryMB, &portsValue, &vm.NetworkMode, &volumesValue, &vm.GuestIP, &vm.MACAddress, &vm.TapName, &vm.LogPath, &vm.DrivePath, &vm.ConsolePath, &vm.ExitCode); err != nil {
		return Details{}, err
	}
	if vm.NetworkMode == "" {
		vm.NetworkMode = "nat"
	}
	if vm.Status != internal.VmRunning {
		vm.GuestIP = ""
		vm.MACAddress = ""
		vm.TapName = ""
	}
	vm.Ports = SplitPorts(portsValue)
	vm.Volumes = SplitList(volumesValue)
	return vm, nil
}

// ListStatsRecords returns persisted stats fields for one VM or all running VMs.
func ListStatsRecords(db *sql.DB, vmID string) ([]StatsRecord, error) {
	query := `SELECT id, pid, status, memory_mb, created_at FROM vms`
	args := []any{}
	if vmID != "" {
		query += ` WHERE id = ?`
		args = append(args, vmID)
	} else {
		query += ` WHERE status = ?`
		args = append(args, internal.VmRunning)
	}

	rows, err := db.Query(query, args...)
	if err != nil {
		return nil, fmt.Errorf("stats query failed: %v", err)
	}
	defer rows.Close()

	var stats []StatsRecord
	for rows.Next() {
		var stat StatsRecord
		var createdAt string
		if err := rows.Scan(&stat.ID, &stat.PID, &stat.Status, &stat.MemoryMB, &createdAt); err != nil {
			return nil, fmt.Errorf("stats scan failed: %v", err)
		}
		if created, err := ParseDBTime(createdAt); err == nil {
			stat.UptimeSeconds = int64(time.Since(created).Seconds())
		}
		stats = append(stats, stat)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("stats rows failed: %v", err)
	}
	return stats, nil
}

// GetWaitStatus returns the status tuple used by Wait.
func GetWaitStatus(db *sql.DB, id string) (WaitStatus, error) {
	var status WaitStatus
	err := db.QueryRow(`SELECT status, COALESCE(exit_code, -1) FROM vms WHERE id = ?`, id).Scan(&status.Status, &status.ExitCode)
	return status, err
}

// ParseDBTime parses timestamps returned by SQLite or by Go encoders.
func ParseDBTime(value string) (time.Time, error) {
	for _, layout := range []string{
		"2006-01-02 15:04:05",
		time.RFC3339Nano,
		time.RFC3339,
	} {
		parsed, err := time.ParseInLocation(layout, value, time.Local)
		if err == nil {
			return parsed, nil
		}
	}
	return time.Time{}, fmt.Errorf("unsupported time format: %s", value)
}

// SplitPorts decodes the comma-separated port list stored in SQLite.
func SplitPorts(value string) []string {
	return SplitList(value)
}

func SplitList(value string) []string {
	if strings.TrimSpace(value) == "" {
		return nil
	}
	parts := strings.Split(value, ",")
	values := make([]string, 0, len(parts))
	for _, part := range parts {
		if trimmed := strings.TrimSpace(part); trimmed != "" {
			values = append(values, trimmed)
		}
	}
	return values
}
