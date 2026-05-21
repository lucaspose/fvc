package fcapi

import (
	"encoding/json"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestWaitForSocket(t *testing.T) {
	socketPath := filepath.Join(t.TempDir(), "fc.sock")
	listener, err := net.Listen("unix", socketPath)
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()

	if err := WaitForSocket(socketPath, time.Second); err != nil {
		t.Fatalf("WaitForSocket failed: %v", err)
	}
}

func TestSendConfig(t *testing.T) {
	socketPath := filepath.Join(t.TempDir(), "fc.sock")
	listener, err := net.Listen("unix", socketPath)
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()

	var gotPath string
	var gotBody map[string]string
	server := http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		if err := json.NewDecoder(r.Body).Decode(&gotBody); err != nil {
			t.Fatal(err)
		}
		w.WriteHeader(http.StatusNoContent)
	})}
	done := make(chan struct{})
	go func() {
		_ = server.Serve(listener)
		close(done)
	}()
	defer func() {
		_ = server.Close()
		<-done
	}()

	if err := SendConfig(socketPath, http.MethodPut, "/boot-source", `{"boot_args":"console=ttyS0"}`); err != nil {
		t.Fatalf("SendConfig failed: %v", err)
	}
	if gotPath != "/boot-source" || gotBody["boot_args"] != "console=ttyS0" {
		t.Fatalf("unexpected request path/body: %s %#v", gotPath, gotBody)
	}
}

func TestConfigureDriveBuildsExpectedRequest(t *testing.T) {
	socketPath := filepath.Join(t.TempDir(), "fc.sock")
	requests, closeServer := captureUnixHTTP(t, socketPath)
	defer closeServer()

	if err := ConfigureDrive(socketPath, "rootfs", "/tmp/root.ext4", true, false); err != nil {
		t.Fatalf("ConfigureDrive failed: %v", err)
	}
	req := <-requests
	if req.path != "/drives/rootfs" {
		t.Fatalf("unexpected path %s", req.path)
	}
	if req.body["drive_id"] != "rootfs" || req.body["path_on_host"] != "/tmp/root.ext4" || req.body["is_root_device"] != true {
		t.Fatalf("unexpected body %#v", req.body)
	}
}

type capturedRequest struct {
	path string
	body map[string]any
}

func captureUnixHTTP(t *testing.T, socketPath string) (<-chan capturedRequest, func()) {
	t.Helper()
	_ = os.Remove(socketPath)
	listener, err := net.Listen("unix", socketPath)
	if err != nil {
		t.Fatal(err)
	}
	requests := make(chan capturedRequest, 1)
	server := http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatal(err)
		}
		requests <- capturedRequest{path: r.URL.Path, body: body}
		w.WriteHeader(http.StatusNoContent)
	})}
	done := make(chan struct{})
	go func() {
		_ = server.Serve(listener)
		close(done)
	}()
	return requests, func() {
		_ = server.Close()
		<-done
	}
}
