package main

import "testing"

func TestDockerLocalTargetAddsLatestAndDropsRegistry(t *testing.T) {
	target, err := dockerLocalTarget("registry-1.docker.io/library/nginx", "")
	if err != nil {
		t.Fatalf("dockerLocalTarget failed: %v", err)
	}
	if target != "library/nginx:latest" {
		t.Fatalf("unexpected target: %s", target)
	}
}

func TestDockerLocalTargetKeepsExplicitTarget(t *testing.T) {
	target, err := dockerLocalTarget("nginx", "web:dev")
	if err != nil {
		t.Fatalf("dockerLocalTarget failed: %v", err)
	}
	if target != "web:dev" {
		t.Fatalf("unexpected target: %s", target)
	}
}
