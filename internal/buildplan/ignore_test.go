package buildplan

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadIgnoreMatchesPatterns(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, ".fvcignore"), []byte(".git\n*.db\nbuild\n"), 0644); err != nil {
		t.Fatal(err)
	}

	ignore, err := LoadIgnore(dir)
	if err != nil {
		t.Fatalf("LoadIgnore failed: %v", err)
	}
	for _, path := range []string{".git/config", "state.db", "build/output"} {
		if !ignore.Matches(path) {
			t.Fatalf("expected %s to be ignored", path)
		}
	}
	if ignore.Matches("app/main.go") {
		t.Fatal("did not expect app/main.go to be ignored")
	}
}
