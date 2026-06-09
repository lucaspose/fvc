package vmstore

import (
	"database/sql"
	"fmt"
	"strings"

	"github.com/lucaspose/fvc/internal"
)

// StaleVM is a running VM record whose Firecracker process no longer matches
// the host process table.
type StaleVM struct {
	ID         string
	TapName    string
	GuestIP    string
	MAC        string
	PortsValue string
}

// ResolveRef resolves a VM by full id, name, or unique id prefix.
func ResolveRef(db *sql.DB, ref string) (string, error) {
	ref = strings.TrimSpace(ref)
	if ref == "" {
		return "", fmt.Errorf("vm id or name is required")
	}
	var id string
	err := db.QueryRow(`SELECT id FROM vms WHERE id = ?`, ref).Scan(&id)
	if err == nil {
		return id, nil
	}
	if err != sql.ErrNoRows {
		return "", err
	}
	err = db.QueryRow(`SELECT id FROM vms WHERE name = ?`, ref).Scan(&id)
	if err == nil {
		return id, nil
	}
	if err != sql.ErrNoRows {
		return "", err
	}
	rows, err := db.Query(`SELECT id FROM vms WHERE id LIKE ?`, ref+"%")
	if err != nil {
		return "", err
	}
	defer rows.Close()
	var matches []string
	for rows.Next() {
		var match string
		if err := rows.Scan(&match); err != nil {
			return "", err
		}
		matches = append(matches, match)
	}
	if err := rows.Err(); err != nil {
		return "", err
	}
	switch len(matches) {
	case 0:
		return "", sql.ErrNoRows
	case 1:
		return matches[0], nil
	default:
		return "", fmt.Errorf("ambiguous vm reference %q", ref)
	}
}

// NameExists reports whether name is already used by a VM other than ignoreID.
func NameExists(db *sql.DB, name, ignoreID string) (bool, error) {
	if strings.TrimSpace(name) == "" {
		return false, nil
	}
	var id string
	err := db.QueryRow(`SELECT id FROM vms WHERE name = ? AND id <> ?`, name, ignoreID).Scan(&id)
	if err == sql.ErrNoRows {
		return false, nil
	}
	return err == nil, err
}

// ProcessMatchesFunc validates that pid still belongs to the VM process.
type ProcessMatchesFunc func(pid int, expectedStartTime string) bool

// StaleVMFunc is called before a stale VM is marked stopped.
type StaleVMFunc func(StaleVM)

// ConfigureDB applies SQLite settings expected by the daemon.
func ConfigureDB(db *sql.DB) error {
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)
	if _, err := db.Exec(`PRAGMA busy_timeout = 5000`); err != nil {
		return fmt.Errorf("busy timeout setup failed: %w", err)
	}
	return nil
}

// EnsureSchema creates and migrates the VM state table.
func EnsureSchema(db *sql.DB) error {
	query := `CREATE TABLE IF NOT EXISTS vms (
		id TEXT PRIMARY KEY NOT NULL,
		pid INTEGER DEFAULT 0,
		process_start_time TEXT DEFAULT '',
		exit_code INTEGER DEFAULT -1,
		status TEXT NOT NULL,
		image TEXT NOT NULL,
		cpus INTEGER DEFAULT 1,
		memory_mb INTEGER DEFAULT 512,
		name TEXT DEFAULT '',
		ports TEXT DEFAULT '',
		volumes TEXT DEFAULT '',
		log_path TEXT,
		drive_path TEXT,
		console_path TEXT,
		vsock_path TEXT DEFAULT '',
		network_mode TEXT DEFAULT '',
		tap_name TEXT,
		guest_ip TEXT,
		mac_address TEXT,
		agent_token TEXT DEFAULT '',
		auto_remove INTEGER DEFAULT 0,
		created_at DATETIME DEFAULT CURRENT_TIMESTAMP
	);`
	if _, err := db.Exec(query); err != nil {
		return err
	}
	_, _ = db.Exec(`ALTER TABLE vms ADD COLUMN log_path TEXT`)
	_, _ = db.Exec(`ALTER TABLE vms ADD COLUMN process_start_time TEXT DEFAULT ''`)
	_, _ = db.Exec(`ALTER TABLE vms ADD COLUMN exit_code INTEGER DEFAULT -1`)
	_, _ = db.Exec(`ALTER TABLE vms ADD COLUMN drive_path TEXT`)
	_, _ = db.Exec(`ALTER TABLE vms ADD COLUMN console_path TEXT`)
	_, _ = db.Exec(`ALTER TABLE vms ADD COLUMN vsock_path TEXT DEFAULT ''`)
	_, _ = db.Exec(`ALTER TABLE vms ADD COLUMN network_mode TEXT DEFAULT ''`)
	_, _ = db.Exec(`ALTER TABLE vms ADD COLUMN volumes TEXT DEFAULT ''`)
	_, _ = db.Exec(`ALTER TABLE vms ADD COLUMN tap_name TEXT`)
	_, _ = db.Exec(`ALTER TABLE vms ADD COLUMN guest_ip TEXT`)
	_, _ = db.Exec(`ALTER TABLE vms ADD COLUMN mac_address TEXT`)
	_, _ = db.Exec(`ALTER TABLE vms ADD COLUMN agent_token TEXT DEFAULT ''`)
	_, _ = db.Exec(`ALTER TABLE vms ADD COLUMN auto_remove INTEGER DEFAULT 0`)
	_, _ = db.Exec(`ALTER TABLE vms ADD COLUMN name TEXT DEFAULT ''`)
	_, _ = db.Exec(`ALTER TABLE vms ADD COLUMN ports TEXT DEFAULT ''`)
	_, _ = db.Exec(`UPDATE vms SET tap_name = '', guest_ip = '', mac_address = '' WHERE status <> ?`, internal.VmRunning)
	_, _ = db.Exec(`UPDATE vms SET name = '' WHERE name <> '' AND id NOT IN (SELECT MIN(id) FROM vms WHERE name <> '' GROUP BY name)`)
	if _, err := db.Exec(`CREATE UNIQUE INDEX IF NOT EXISTS idx_vms_name_unique ON vms(name) WHERE name <> ''`); err != nil {
		return err
	}
	return nil
}

// Reconcile marks running VM records as stopped when their recorded process is
// no longer alive or no longer matches the expected start time.
func Reconcile(db *sql.DB, processMatches ProcessMatchesFunc, onStale StaleVMFunc) error {
	if processMatches == nil {
		processMatches = func(pid int, expectedStartTime string) bool { return pid > 0 }
	}
	rows, err := db.Query(`SELECT id, pid, COALESCE(process_start_time, ''), COALESCE(tap_name, ''), COALESCE(guest_ip, ''), COALESCE(mac_address, ''), COALESCE(ports, '') FROM vms WHERE status = ? AND pid > 0`, internal.VmRunning)
	if err != nil {
		return err
	}

	var stale []StaleVM
	for rows.Next() {
		var vm StaleVM
		var pid int
		var processStartTime string
		if err := rows.Scan(&vm.ID, &pid, &processStartTime, &vm.TapName, &vm.GuestIP, &vm.MAC, &vm.PortsValue); err != nil {
			_ = rows.Close()
			return err
		}
		if !processMatches(pid, processStartTime) {
			stale = append(stale, vm)
		}
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		return err
	}
	if err := rows.Close(); err != nil {
		return err
	}
	for _, vm := range stale {
		if onStale != nil {
			onStale(vm)
		}
		_, _ = db.Exec(`UPDATE vms SET status = ?, pid = 0, process_start_time = '', tap_name = '', guest_ip = '', mac_address = '' WHERE id = ?`, internal.VmStopped, vm.ID)
	}
	return nil
}
