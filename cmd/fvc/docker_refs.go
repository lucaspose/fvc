package main

import (
	"fmt"
	"strings"

	"github.com/lucaspose/fvc/internal"
)

func parseDockerImageArg(value string) (string, bool) {
	value = strings.TrimSpace(value)
	if strings.HasPrefix(value, "docker://") {
		return strings.TrimPrefix(value, "docker://"), true
	}
	return value, false
}

func dockerLocalTarget(sourceRef, explicitTarget string) (string, error) {
	target := strings.TrimSpace(explicitTarget)
	if target == "" {
		target = localTargetFromDockerRef(sourceRef)
	}
	if err := internal.ValidateImageRef(target); err != nil {
		return "", fmt.Errorf("invalid local image tag %q: %w", target, err)
	}
	return target, nil
}

func localTargetFromDockerRef(sourceRef string) string {
	ref := strings.TrimSpace(sourceRef)
	ref = strings.TrimPrefix(ref, "docker://")
	if idx := strings.Index(ref, "@"); idx >= 0 {
		ref = ref[:idx]
	}
	parts := strings.Split(ref, "/")
	if len(parts) > 1 && isDockerRegistryPart(parts[0]) {
		parts = parts[1:]
	}
	ref = strings.Join(parts, "/")
	if ref == "" {
		return ref
	}
	name, tag, hasTag := splitDockerTag(ref)
	if !hasTag {
		return name + ":latest"
	}
	return name + ":" + tag
}

func isDockerRegistryPart(value string) bool {
	return strings.Contains(value, ".") || strings.Contains(value, ":") || value == "localhost"
}

func splitDockerTag(ref string) (string, string, bool) {
	lastSlash := strings.LastIndex(ref, "/")
	lastColon := strings.LastIndex(ref, ":")
	if lastColon > lastSlash {
		return ref[:lastColon], ref[lastColon+1:], true
	}
	return ref, "", false
}
