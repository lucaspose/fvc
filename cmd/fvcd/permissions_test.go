package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestRuntimeGIDDisabledWhenGroupEmpty(t *testing.T) {
	gid, ok, err := runtimeGID("")
	if err != nil {
		t.Fatalf("runtimeGID failed: %v", err)
	}
	if ok || gid != -1 {
		t.Fatalf("expected disabled runtime gid, got gid=%d ok=%v", gid, ok)
	}
}

func TestRuntimeGIDUsesLookup(t *testing.T) {
	oldLookup := lookupGroupID
	defer func() { lookupGroupID = oldLookup }()
	lookupGroupID = func(name string) (int, error) {
		if name != "fvc" {
			t.Fatalf("unexpected group lookup: %s", name)
		}
		return 1234, nil
	}

	gid, ok, err := runtimeGID("fvc")
	if err != nil {
		t.Fatalf("runtimeGID failed: %v", err)
	}
	if !ok || gid != 1234 {
		t.Fatalf("unexpected gid result gid=%d ok=%v", gid, ok)
	}
}

func TestApplyRuntimePermissionsSetsModeWithoutGroup(t *testing.T) {
	path := filepath.Join(t.TempDir(), "runtime")
	if err := os.WriteFile(path, []byte("x"), 0600); err != nil {
		t.Fatalf("failed to write file: %v", err)
	}
	if err := applyRuntimePermissions(path, "", 0660); err != nil {
		t.Fatalf("applyRuntimePermissions failed: %v", err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat failed: %v", err)
	}
	if info.Mode().Perm() != 0660 {
		t.Fatalf("unexpected mode: %v", info.Mode().Perm())
	}
}
