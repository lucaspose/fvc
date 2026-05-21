package vmstore

import (
	"database/sql"
	"strings"

	"github.com/lucaspose/fvc/internal"
)

// NetworkFields contains VM network state persisted in SQLite.
type NetworkFields struct {
	TapName string
	GuestIP string
	MAC     string
	Ports   []string
}

// StopState contains the persisted fields needed to stop a VM.
type StopState struct {
	PID              int
	ProcessStartTime string
	Status           string
	VsockPath        string
	Network          NetworkFields
}

// KillState contains the persisted fields needed to kill a VM.
type KillState struct {
	StopState
	ConsolePath string
}

// StartState contains the persisted fields needed to restart a stopped VM.
type StartState struct {
	Status     string
	Image      string
	CPUs       int32
	MemoryMB   int32
	LogPath    string
	DrivePath  string
	Ports      []string
	AgentToken string
}

// RemoveState contains the persisted fields needed to remove a VM.
type RemoveState struct {
	Status      string
	PID         int
	DrivePath   string
	LogPath     string
	ConsolePath string
	VsockPath   string
	Network     NetworkFields
}

// ConsoleState contains the persisted fields needed to attach to a VM console.
type ConsoleState struct {
	Status      string
	LogPath     string
	ConsolePath string
}

// ExecAgentState contains the persisted fields needed to reach the guest agent.
type ExecAgentState struct {
	Status     string
	GuestIP    string
	AgentToken string
	VsockPath  string
}

// SnapshotDriveState contains the VM fields needed before snapshot operations.
type SnapshotDriveState struct {
	Status    string
	DrivePath string
}

// RunRecord contains the fields persisted when a new VM starts.
type RunRecord struct {
	ID               string
	Name             string
	PID              int32
	ProcessStartTime string
	Image            string
	CPUs             int32
	MemoryMB         int32
	Ports            []string
	LogPath          string
	DrivePath        string
	ConsolePath      string
	Network          NetworkFields
	AgentToken       string
	VsockPath        string
}

// RunningUpdate contains the fields updated when an existing VM restarts.
type RunningUpdate struct {
	ID               string
	PID              int32
	ProcessStartTime string
	ConsolePath      string
	Network          NetworkFields
	AgentToken       string
	VsockPath        string
}

func GetStopState(db *sql.DB, id string) (StopState, error) {
	var state StopState
	var portsValue string
	err := db.QueryRow(`SELECT pid, COALESCE(process_start_time, ''), status, COALESCE(tap_name, ''), COALESCE(guest_ip, ''), COALESCE(mac_address, ''), COALESCE(ports, ''), COALESCE(vsock_path, '') FROM vms WHERE id = ?`, id).
		Scan(&state.PID, &state.ProcessStartTime, &state.Status, &state.Network.TapName, &state.Network.GuestIP, &state.Network.MAC, &portsValue, &state.VsockPath)
	state.Network.Ports = SplitPorts(portsValue)
	return state, err
}

func GetKillState(db *sql.DB, id string) (KillState, error) {
	var state KillState
	var portsValue string
	err := db.QueryRow(`SELECT pid, COALESCE(process_start_time, ''), status, COALESCE(tap_name, ''), COALESCE(guest_ip, ''), COALESCE(mac_address, ''), COALESCE(console_path, ''), COALESCE(vsock_path, ''), COALESCE(ports, '') FROM vms WHERE id = ?`, id).
		Scan(&state.PID, &state.ProcessStartTime, &state.Status, &state.Network.TapName, &state.Network.GuestIP, &state.Network.MAC, &state.ConsolePath, &state.VsockPath, &portsValue)
	state.Network.Ports = SplitPorts(portsValue)
	return state, err
}

func GetStartState(db *sql.DB, id string) (StartState, error) {
	var state StartState
	var portsValue string
	err := db.QueryRow(`SELECT status, image, cpus, memory_mb, COALESCE(log_path, ''), COALESCE(drive_path, ''), COALESCE(ports, ''), COALESCE(agent_token, '') FROM vms WHERE id = ?`, id).
		Scan(&state.Status, &state.Image, &state.CPUs, &state.MemoryMB, &state.LogPath, &state.DrivePath, &portsValue, &state.AgentToken)
	state.Ports = SplitPorts(portsValue)
	return state, err
}

func GetRemoveState(db *sql.DB, id string) (RemoveState, error) {
	var state RemoveState
	var portsValue string
	err := db.QueryRow(`SELECT status, pid, COALESCE(drive_path, ''), COALESCE(log_path, ''), COALESCE(console_path, ''), COALESCE(vsock_path, ''), COALESCE(tap_name, ''), COALESCE(guest_ip, ''), COALESCE(mac_address, ''), COALESCE(ports, '') FROM vms WHERE id = ?`, id).
		Scan(&state.Status, &state.PID, &state.DrivePath, &state.LogPath, &state.ConsolePath, &state.VsockPath, &state.Network.TapName, &state.Network.GuestIP, &state.Network.MAC, &portsValue)
	state.Network.Ports = SplitPorts(portsValue)
	return state, err
}

func GetConsoleState(db *sql.DB, id string) (ConsoleState, error) {
	var state ConsoleState
	err := db.QueryRow(`SELECT status, COALESCE(log_path, ''), COALESCE(console_path, '') FROM vms WHERE id = ?`, id).
		Scan(&state.Status, &state.LogPath, &state.ConsolePath)
	return state, err
}

func GetExecAgentState(db *sql.DB, id string) (ExecAgentState, error) {
	var state ExecAgentState
	err := db.QueryRow(`SELECT status, COALESCE(guest_ip, ''), COALESCE(agent_token, ''), COALESCE(vsock_path, '') FROM vms WHERE id = ?`, id).
		Scan(&state.Status, &state.GuestIP, &state.AgentToken, &state.VsockPath)
	return state, err
}

func LogPath(db *sql.DB, id string) (string, error) {
	var logPath string
	err := db.QueryRow(`SELECT log_path FROM vms WHERE id = ?`, id).Scan(&logPath)
	return logPath, err
}

func GetSnapshotDriveState(db *sql.DB, id string) (SnapshotDriveState, error) {
	var state SnapshotDriveState
	err := db.QueryRow(`SELECT status, COALESCE(drive_path, '') FROM vms WHERE id = ?`, id).Scan(&state.Status, &state.DrivePath)
	return state, err
}

func Exists(db *sql.DB, id string) error {
	var found string
	return db.QueryRow(`SELECT id FROM vms WHERE id = ?`, id).Scan(&found)
}

func InsertRunning(db *sql.DB, record RunRecord) error {
	query := `INSERT INTO vms (id, name, pid, process_start_time, status, image, cpus, memory_mb, ports, log_path, drive_path, console_path, tap_name, guest_ip, mac_address, exit_code, agent_token, vsock_path) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`
	_, err := db.Exec(query, record.ID, record.Name, record.PID, record.ProcessStartTime, internal.VmRunning, record.Image, record.CPUs, record.MemoryMB, strings.Join(record.Ports, ","), record.LogPath, record.DrivePath, record.ConsolePath, record.Network.TapName, record.Network.GuestIP, record.Network.MAC, -1, record.AgentToken, record.VsockPath)
	return err
}

func MarkRunning(db *sql.DB, update RunningUpdate) error {
	_, err := db.Exec(`UPDATE vms SET status = ?, pid = ?, exit_code = -1, process_start_time = ?, console_path = ?, tap_name = ?, guest_ip = ?, mac_address = ?, agent_token = ?, vsock_path = ? WHERE id = ?`,
		internal.VmRunning, update.PID, update.ProcessStartTime, update.ConsolePath, update.Network.TapName, update.Network.GuestIP, update.Network.MAC, update.AgentToken, update.VsockPath, update.ID)
	return err
}

func MarkStopped(db *sql.DB, id string) error {
	_, err := db.Exec(`UPDATE vms SET status = ?, pid = 0, exit_code = -1, process_start_time = '', tap_name = '', guest_ip = '', mac_address = '' WHERE id = ?`, internal.VmStopped, id)
	return err
}

func MarkStoppedIfRunning(db *sql.DB, id string, exitCode int32) error {
	status := internal.VmStopped
	if exitCode >= 0 {
		status = internal.VmExited
	}
	_, err := db.Exec(`UPDATE vms SET status = ?, pid = 0, exit_code = ?, process_start_time = '', tap_name = '', guest_ip = '', mac_address = '' WHERE id = ? AND status = ?`, status, exitCode, id, internal.VmRunning)
	return err
}

func MarkStaleStopped(db *sql.DB, id string) error {
	_, err := db.Exec(`UPDATE vms SET status = ?, pid = 0, process_start_time = '', tap_name = '', guest_ip = '', mac_address = '' WHERE id = ?`, internal.VmStopped, id)
	return err
}

func Delete(db *sql.DB, id string) error {
	_, err := db.Exec(`DELETE FROM vms WHERE id = ?`, id)
	return err
}
