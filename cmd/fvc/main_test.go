package main

import "testing"

func TestValidateVmfileAcceptsMinimalConfig(t *testing.T) {
	config := VmfileConfig{
		VM: VMSection{
			Name: "api",
			CPU:  2,
			RAM:  512,
		},
		Image: ImageSection{
			Source: "debian:bookworm",
		},
	}

	if err := validateVmfile(config); err != nil {
		t.Fatalf("expected config to be valid, got %v", err)
	}
}

func TestValidateVmfileRejectsMissingImage(t *testing.T) {
	config := VmfileConfig{
		VM: VMSection{
			CPU: 1,
			RAM: 512,
		},
	}

	if err := validateVmfile(config); err == nil {
		t.Fatal("expected missing image to be rejected")
	}
}

func TestValidateVmfileRejectsInvalidResources(t *testing.T) {
	config := VmfileConfig{
		VM: VMSection{
			CPU: 0,
			RAM: 64,
		},
		Image: ImageSection{
			Source: "debian",
		},
	}

	if err := validateVmfile(config); err == nil {
		t.Fatal("expected invalid resources to be rejected")
	}
}

func TestValidateVmfileRejectsUnsafeImage(t *testing.T) {
	config := VmfileConfig{
		VM: VMSection{
			CPU: 1,
			RAM: 512,
		},
		Image: ImageSection{
			Source: "../debian",
		},
	}

	if err := validateVmfile(config); err == nil {
		t.Fatal("expected unsafe image to be rejected")
	}
}
