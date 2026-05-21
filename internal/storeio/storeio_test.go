package storeio

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

func TestCopyFileAtomicProgressCopiesContentAndReportsProgress(t *testing.T) {
	dir := t.TempDir()
	source := filepath.Join(dir, "source.txt")
	dest := filepath.Join(dir, "nested", "dest.txt")
	if err := os.WriteFile(source, []byte("hello"), 0644); err != nil {
		t.Fatal(err)
	}

	var calls int
	size, err := CopyFileAtomicProgress(source, dest, func(current, total int64) {
		calls++
		if total != 5 {
			t.Fatalf("expected total 5, got %d", total)
		}
		if current < 0 || current > total {
			t.Fatalf("unexpected current value %d", current)
		}
	})
	if err != nil {
		t.Fatalf("CopyFileAtomicProgress failed: %v", err)
	}
	if size != 5 {
		t.Fatalf("expected size 5, got %d", size)
	}
	data, err := os.ReadFile(dest)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "hello" {
		t.Fatalf("unexpected copied content %q", string(data))
	}
	if calls == 0 {
		t.Fatalf("expected progress callback")
	}
}

func TestDownloadAtomicProgressPublishesSuccessfulResponse(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("downloaded"))
	}))
	defer server.Close()

	dest := filepath.Join(t.TempDir(), "out.bin")
	if err := DownloadAtomicProgress(server.URL, dest, nil); err != nil {
		t.Fatalf("DownloadAtomicProgress failed: %v", err)
	}
	data, err := os.ReadFile(dest)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "downloaded" {
		t.Fatalf("unexpected downloaded content %q", string(data))
	}
}
