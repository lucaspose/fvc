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
