package buildplan

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// Ignore contains .fvcignore patterns for a build context.
type Ignore struct {
	patterns []string
}

// LoadIgnore reads .fvcignore from contextPath. Missing files are treated as an
// empty ignore set.
func LoadIgnore(contextPath string) (Ignore, error) {
	path := filepath.Join(contextPath, ".fvcignore")
	file, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			return Ignore{}, nil
		}
		return Ignore{}, fmt.Errorf(".fvcignore read failed: %w", err)
	}
	defer file.Close()

	var patterns []string
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		line = filepath.ToSlash(filepath.Clean(strings.TrimPrefix(line, "/")))
		if line == "." || strings.HasPrefix(line, "../") || line == ".." {
			return Ignore{}, fmt.Errorf(".fvcignore pattern escapes context: %s", scanner.Text())
		}
		patterns = append(patterns, line)
	}
	if err := scanner.Err(); err != nil {
		return Ignore{}, fmt.Errorf(".fvcignore scan failed: %w", err)
	}
	return Ignore{patterns: patterns}, nil
}

// Matches reports whether rel is ignored by this ignore set.
func (i Ignore) Matches(rel string) bool {
	rel = filepath.ToSlash(filepath.Clean(strings.TrimPrefix(rel, "/")))
	if rel == "." {
		return false
	}
	for _, pattern := range i.patterns {
		if ignorePatternMatches(pattern, rel) {
			return true
		}
	}
	return false
}

func ignorePatternMatches(pattern, rel string) bool {
	if pattern == rel || strings.HasPrefix(rel, pattern+"/") {
		return true
	}
	if ok, _ := filepath.Match(pattern, rel); ok {
		return true
	}
	base := pathBase(rel)
	if ok, _ := filepath.Match(pattern, base); ok {
		return true
	}
	return false
}

func pathBase(rel string) string {
	idx := strings.LastIndex(rel, "/")
	if idx == -1 {
		return rel
	}
	return rel[idx+1:]
}
