package internal

import "testing"

func TestValidateImageRefAcceptsSafeRefs(t *testing.T) {
	for _, ref := range []string{
		"debian",
		"ubuntu:22.04",
		"library/debian:bookworm",
		"registry.local/team/api_1.2:prod",
	} {
		t.Run(ref, func(t *testing.T) {
			if err := ValidateImageRef(ref); err != nil {
				t.Fatalf("expected %q to be valid, got %v", ref, err)
			}
		})
	}
}

func TestValidateImageRefRejectsUnsafeRefs(t *testing.T) {
	for _, ref := range []string{
		"",
		"../debian",
		"library/../debian",
		"/debian",
		"debian/",
		"debian?tag=latest",
		"debian:latest:extra",
	} {
		t.Run(ref, func(t *testing.T) {
			if err := ValidateImageRef(ref); err == nil {
				t.Fatalf("expected %q to be rejected", ref)
			}
		})
	}
}

func TestValidateResources(t *testing.T) {
	if err := ValidateResources(2, 512); err != nil {
		t.Fatalf("expected resources to be valid, got %v", err)
	}
	if err := ValidateResources(0, 64); err == nil {
		t.Fatal("expected invalid resources to be rejected")
	}
}

func TestValidateNetworkMode(t *testing.T) {
	for _, mode := range []string{"", NetworkModeNAT, NetworkModeNone, " nat "} {
		if err := ValidateNetworkMode(mode); err != nil {
			t.Fatalf("expected %q to be valid, got %v", mode, err)
		}
	}
	if err := ValidateNetworkMode("bridge"); err == nil {
		t.Fatal("expected unsupported network mode to be rejected")
	}
}

func TestNormalizeVolumeSpec(t *testing.T) {
	cases := map[string]string{
		"data:/var/lib/app":    "data:/var/lib/app",
		"cache:/cache:ro":      "cache:/cache:ro",
		"logs_1:/var/logs:rw":  "logs_1:/var/logs",
		" tmp-data:/tmp/data ": "tmp-data:/tmp/data",
	}
	for input, want := range cases {
		got, err := NormalizeVolumeSpec(input)
		if err != nil {
			t.Fatalf("NormalizeVolumeSpec(%q) failed: %v", input, err)
		}
		if got != want {
			t.Fatalf("NormalizeVolumeSpec(%q) = %q, want %q", input, got, want)
		}
	}
}

func TestNormalizeVolumeSpecRejectsInvalid(t *testing.T) {
	for _, input := range []string{"", "/host:/guest", "data:relative", "data:/", "data:/a/../b", "data:/guest:bad"} {
		if _, err := NormalizeVolumeSpec(input); err == nil {
			t.Fatalf("expected %q to be rejected", input)
		}
	}
}

func TestNormalizePortSpec(t *testing.T) {
	cases := map[string]string{
		"80":       "80:80",
		"8080:80":  "8080:80",
		" 443:443": "443:443",
	}
	for input, want := range cases {
		got, err := NormalizePortSpec(input)
		if err != nil {
			t.Fatalf("NormalizePortSpec(%q) failed: %v", input, err)
		}
		if got != want {
			t.Fatalf("NormalizePortSpec(%q) = %q, want %q", input, got, want)
		}
	}
}

func TestValidatePortSpecRejectsInvalid(t *testing.T) {
	for _, input := range []string{"", "0:80", "80:70000", "1:2:3", "abc"} {
		if err := ValidatePortSpec(input); err == nil {
			t.Fatalf("expected %q to be rejected", input)
		}
	}
}
