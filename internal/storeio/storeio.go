package storeio

import (
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"time"
)

const maxTransferBytes = 10 << 30 // 10 GiB

// ProgressFunc receives byte-level transfer progress.
type ProgressFunc func(current, total int64)

// DownloadAtomic downloads a remote file and publishes it only after a complete
// successful transfer.
func DownloadAtomic(remoteURL, destPath string) error {
	return DownloadAtomicProgress(remoteURL, destPath, nil)
}

// DownloadAtomicProgress is DownloadAtomic with transfer progress callbacks.
func DownloadAtomicProgress(remoteURL, destPath string, progress ProgressFunc) error {
	client := http.Client{Timeout: 10 * time.Minute}
	resp, err := client.Get(remoteURL)
	if err != nil {
		return fmt.Errorf("http request failed: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("remote file unavailable: %s", resp.Status)
	}

	if err := os.MkdirAll(filepath.Dir(destPath), 0755); err != nil {
		return fmt.Errorf("destination directory setup failed: %v", err)
	}

	tmp, err := os.CreateTemp(filepath.Dir(destPath), "."+filepath.Base(destPath)+".tmp-*")
	if err != nil {
		return fmt.Errorf("temporary file create failed: %v", err)
	}
	tmpPath := tmp.Name()
	defer func() {
		_ = os.Remove(tmpPath)
	}()

	total := resp.ContentLength
	if progress != nil {
		progress(0, total)
	}
	reader := &progressReader{
		reader: io.LimitReader(resp.Body, maxTransferBytes+1),
		total:  total,
		emit:   progress,
	}
	written, err := io.Copy(tmp, reader)
	if closeErr := tmp.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		return fmt.Errorf("download copy failed: %v", err)
	}
	if written == 0 {
		return fmt.Errorf("remote file is empty")
	}
	if written > maxTransferBytes {
		return fmt.Errorf("remote file exceeds maximum size")
	}

	if err := os.Rename(tmpPath, destPath); err != nil {
		return fmt.Errorf("download publish failed: %v", err)
	}
	return nil
}

// CopyFileAtomic copies a file and publishes the destination with an atomic
// rename in the destination directory.
func CopyFileAtomic(sourcePath, destPath string) (int64, error) {
	return CopyFileAtomicProgress(sourcePath, destPath, nil)
}

// CopyFileAtomicProgress is CopyFileAtomic with transfer progress callbacks.
func CopyFileAtomicProgress(sourcePath, destPath string, progress ProgressFunc) (int64, error) {
	src, err := os.Open(sourcePath)
	if err != nil {
		if os.IsNotExist(err) {
			return 0, fmt.Errorf("source file not found: %s", sourcePath)
		}
		return 0, fmt.Errorf("source file open failed: %v", err)
	}
	defer src.Close()
	total := int64(-1)
	if info, err := src.Stat(); err == nil {
		total = info.Size()
	}
	if progress != nil {
		progress(0, total)
	}

	if err := os.MkdirAll(filepath.Dir(destPath), 0755); err != nil {
		return 0, fmt.Errorf("destination directory setup failed: %v", err)
	}

	tmp, err := os.CreateTemp(filepath.Dir(destPath), "."+filepath.Base(destPath)+".tmp-*")
	if err != nil {
		return 0, fmt.Errorf("temporary file create failed: %v", err)
	}
	tmpPath := tmp.Name()
	defer func() {
		_ = os.Remove(tmpPath)
	}()

	reader := &progressReader{reader: src, total: total, emit: progress}
	size, copyErr := io.Copy(tmp, reader)
	closeErr := tmp.Close()
	if copyErr != nil {
		return 0, fmt.Errorf("file copy failed: %v", copyErr)
	}
	if closeErr != nil {
		return 0, fmt.Errorf("temporary file close failed: %v", closeErr)
	}
	if err := os.Rename(tmpPath, destPath); err != nil {
		return 0, fmt.Errorf("file publish failed: %v", err)
	}
	return size, nil
}

type progressReader struct {
	reader      io.Reader
	current     int64
	lastCurrent int64
	total       int64
	lastEmit    time.Time
	emit        ProgressFunc
}

func (r *progressReader) Read(p []byte) (int, error) {
	n, err := r.reader.Read(p)
	if n > 0 {
		r.current += int64(n)
		if r.shouldEmit() {
			r.emit(r.current, r.total)
			r.lastCurrent = r.current
			r.lastEmit = time.Now()
		}
	}
	return n, err
}

func (r *progressReader) shouldEmit() bool {
	if r.emit == nil {
		return false
	}
	if r.total > 0 && r.current >= r.total {
		return true
	}
	if r.current-r.lastCurrent >= 1<<20 {
		return true
	}
	return time.Since(r.lastEmit) >= 120*time.Millisecond
}
