package dockerimport

import (
	"archive/tar"
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"syscall"
	"testing"
)

func TestParseDockerImageRefDefaultsLibraryAndLatest(t *testing.T) {
	ref, err := parseDockerImageRef("ubuntu")
	if err != nil {
		t.Fatalf("parseDockerImageRef failed: %v", err)
	}
	if ref.Repository != "library/ubuntu" || ref.Reference != "latest" || ref.Display != "ubuntu:latest" {
		t.Fatalf("unexpected ref: %#v", ref)
	}
}

func TestParseDockerImageRefKeepsNamespaceAndTag(t *testing.T) {
	ref, err := parseDockerImageRef("lucas/web:1.2.3")
	if err != nil {
		t.Fatalf("parseDockerImageRef failed: %v", err)
	}
	if ref.Repository != "lucas/web" || ref.Reference != "1.2.3" || ref.Display != "lucas/web:1.2.3" {
		t.Fatalf("unexpected ref: %#v", ref)
	}
}

func TestApplyTarLayerHandlesWhiteouts(t *testing.T) {
	rootfs := t.TempDir()
	if err := os.MkdirAll(filepath.Join(rootfs, "etc"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(rootfs, "etc", "old.conf"), []byte("old"), 0644); err != nil {
		t.Fatal(err)
	}
	var layer bytes.Buffer
	tw := tar.NewWriter(&layer)
	if err := tw.WriteHeader(&tar.Header{Name: "etc/.wh.old.conf", Typeflag: tar.TypeReg, Mode: 0644}); err != nil {
		t.Fatal(err)
	}
	if err := tw.WriteHeader(&tar.Header{Name: "etc/new.conf", Typeflag: tar.TypeReg, Mode: 0644, Size: int64(len("new"))}); err != nil {
		t.Fatal(err)
	}
	if _, err := tw.Write([]byte("new")); err != nil {
		t.Fatal(err)
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := applyTarLayer(rootfs, bytes.NewReader(layer.Bytes())); err != nil {
		t.Fatalf("applyTarLayer failed: %v", err)
	}
	if _, err := os.Stat(filepath.Join(rootfs, "etc", "old.conf")); !os.IsNotExist(err) {
		t.Fatalf("expected old.conf to be whiteouted, stat err=%v", err)
	}
	data, err := os.ReadFile(filepath.Join(rootfs, "etc", "new.conf"))
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "new" {
		t.Fatalf("unexpected new.conf content %q", string(data))
	}
}

func TestApplyTarLayerRestoresFileModeAfterUmask(t *testing.T) {
	oldUmask := syscall.Umask(0077)
	defer syscall.Umask(oldUmask)
	rootfs := t.TempDir()
	var layer bytes.Buffer
	tw := tar.NewWriter(&layer)
	if err := tw.WriteHeader(&tar.Header{Name: "index.html", Typeflag: tar.TypeReg, Mode: 0644, Size: int64(len("ok"))}); err != nil {
		t.Fatal(err)
	}
	if _, err := tw.Write([]byte("ok")); err != nil {
		t.Fatal(err)
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := applyTarLayer(rootfs, bytes.NewReader(layer.Bytes())); err != nil {
		t.Fatalf("applyTarLayer failed: %v", err)
	}
	info, err := os.Stat(filepath.Join(rootfs, "index.html"))
	if err != nil {
		t.Fatal(err)
	}
	if got := info.Mode().Perm(); got != 0644 {
		t.Fatalf("expected mode 0644, got %o", got)
	}
}

func TestDockerMetadataFromConfig(t *testing.T) {
	config := map[string]any{
		"created": "2026-05-21T10:00:00Z",
		"config": map[string]any{
			"Env":        []string{"A=B"},
			"Entrypoint": []string{"/entry"},
			"Cmd":        []string{"serve"},
			"WorkingDir": "/srv",
			"ExposedPorts": map[string]any{
				"80/tcp":  map[string]any{},
				"53/udp":  map[string]any{},
				"bad/tcp": map[string]any{},
			},
			"Labels": map[string]string{"app": "web"},
		},
	}
	data, err := json.Marshal(config)
	if err != nil {
		t.Fatal(err)
	}
	metadata, err := dockerMetadataFromConfig(data, "web:latest", "nginx:latest")
	if err != nil {
		t.Fatalf("dockerMetadataFromConfig failed: %v", err)
	}
	if metadata.Name != "web:latest" || metadata.Source != "docker:nginx:latest" {
		t.Fatalf("unexpected metadata identity: %#v", metadata)
	}
	if !reflect.DeepEqual(metadata.Cmd, []string{"/entry", "serve"}) {
		t.Fatalf("unexpected cmd %#v", metadata.Cmd)
	}
	if metadata.Workdir != "/srv" {
		t.Fatalf("unexpected workdir %q", metadata.Workdir)
	}
	if !reflect.DeepEqual(metadata.ExposedPorts, []int32{80}) {
		t.Fatalf("unexpected exposed ports %#v", metadata.ExposedPorts)
	}
}
