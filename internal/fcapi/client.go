package fcapi

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"time"
)

// SendConfig sends one JSON configuration request to a Firecracker API socket.
func SendConfig(socketPath, method, path, jsonBody string) error {
	client := http.Client{
		Transport: &http.Transport{
			DialContext: func(_ context.Context, _, _ string) (net.Conn, error) {
				return net.Dial("unix", socketPath)
			},
		},
	}

	req, err := http.NewRequest(method, "http://localhost"+path, bytes.NewBufferString(jsonBody))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusNoContent && resp.StatusCode != http.StatusOK {
		return fmt.Errorf("bad status code from firecracker API: %s", resp.Status)
	}
	return nil
}

// WaitForSocket waits until the Firecracker API Unix socket is accepting
// connections.
func WaitForSocket(socketPath string, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		conn, err := net.Dial("unix", socketPath)
		if err == nil {
			_ = conn.Close()
			return nil
		}
		time.Sleep(25 * time.Millisecond)
	}
	return fmt.Errorf("firecracker API socket not ready after %s", timeout)
}

func ConfigureBootSource(socketPath, kernelPath, bootArgs string) error {
	body, err := json.Marshal(map[string]string{
		"kernel_image_path": kernelPath,
		"boot_args":         bootArgs,
	})
	if err != nil {
		return fmt.Errorf("boot source config build failed: %w", err)
	}
	return SendConfig(socketPath, http.MethodPut, "/boot-source", string(body))
}

func ConfigureDrive(socketPath, driveID, pathOnHost string, rootDevice, readOnly bool) error {
	body, err := json.Marshal(map[string]any{
		"drive_id":       driveID,
		"path_on_host":   pathOnHost,
		"is_root_device": rootDevice,
		"is_read_only":   readOnly,
	})
	if err != nil {
		return fmt.Errorf("drive config build failed: %w", err)
	}
	return SendConfig(socketPath, http.MethodPut, "/drives/"+driveID, string(body))
}

func ConfigureNetwork(socketPath, ifaceID, guestMAC, hostDevice string) error {
	body, err := json.Marshal(map[string]any{
		"iface_id":      ifaceID,
		"guest_mac":     guestMAC,
		"host_dev_name": hostDevice,
	})
	if err != nil {
		return fmt.Errorf("network config build failed: %w", err)
	}
	return SendConfig(socketPath, http.MethodPut, "/network-interfaces/"+ifaceID, string(body))
}

func ConfigureMachine(socketPath string, cpus, memoryMb int32) error {
	body, err := json.Marshal(map[string]int32{
		"vcpu_count":   cpus,
		"mem_size_mib": memoryMb,
	})
	if err != nil {
		return fmt.Errorf("machine config build failed: %w", err)
	}
	return SendConfig(socketPath, http.MethodPut, "/machine-config", string(body))
}

func StartInstance(socketPath string) error {
	return SendConfig(socketPath, http.MethodPut, "/actions", `{"action_type":"InstanceStart"}`)
}
