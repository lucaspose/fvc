package main

import (
	"bytes"
	"encoding/base64"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/lucaspose/fvc/internal/cliui"
	"github.com/lucaspose/fvc/proto"
)

const maxInlineCopyBytes = 1024 * 1024

func executeCp(client proto.FvcServiceClient, args []string) error {
	fs := flag.NewFlagSet("cp", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	if err := fs.Parse(args); err != nil {
		return err
	}
	if len(fs.Args()) != 2 {
		return errors.New("cp requires source and destination. Examples: fvc cp file.txt vm:/tmp/file.txt or fvc cp vm:/tmp/file.txt ./file.txt")
	}
	srcVM, srcPath, srcRemote := splitVMPath(fs.Args()[0])
	dstVM, dstPath, dstRemote := splitVMPath(fs.Args()[1])
	if srcRemote == dstRemote {
		return errors.New("cp requires exactly one VM path in the form <vm>:<path>")
	}
	if srcRemote {
		return copyFromGuest(client, srcVM, srcPath, dstPath)
	}
	return copyToGuest(client, dstVM, fs.Args()[0], dstPath)
}

func splitVMPath(value string) (string, string, bool) {
	idx := strings.Index(value, ":")
	if idx <= 0 {
		return "", value, false
	}
	return value[:idx], value[idx+1:], true
}

func copyToGuest(client proto.FvcServiceClient, vmID, localPath, guestPath string) error {
	if strings.TrimSpace(guestPath) == "" || !strings.HasPrefix(guestPath, "/") {
		return fmt.Errorf("guest destination must be absolute: %s", guestPath)
	}
	info, err := os.Stat(localPath)
	if err != nil {
		return fmt.Errorf("source stat failed: %w", err)
	}
	if info.IsDir() {
		return errors.New("cp currently supports regular files only")
	}
	if info.Size() > maxInlineCopyBytes {
		return fmt.Errorf("file is too large for fvc cp host-to-guest path (%d bytes > %d bytes)", info.Size(), maxInlineCopyBytes)
	}
	data, err := os.ReadFile(localPath)
	if err != nil {
		return fmt.Errorf("source read failed: %w", err)
	}
	encoded := base64.StdEncoding.EncodeToString(data)
	dir := filepath.Dir(guestPath)
	command := fmt.Sprintf("mkdir -p %s && printf %%s %s | base64 -d > %s", shellQuote(dir), shellQuote(encoded), shellQuote(guestPath))
	if err := streamGuestCommand(client, vmID, []string{"/bin/sh", "-lc", command}, io.Discard, os.Stderr); err != nil {
		return err
	}
	cliui.PrintSuccess(fmt.Sprintf("copied %s to %s:%s", localPath, vmID, guestPath))
	return nil
}

func copyFromGuest(client proto.FvcServiceClient, vmID, guestPath, localPath string) error {
	if strings.TrimSpace(guestPath) == "" || !strings.HasPrefix(guestPath, "/") {
		return fmt.Errorf("guest source must be absolute: %s", guestPath)
	}
	var out bytes.Buffer
	if err := streamGuestCommand(client, vmID, []string{"/bin/sh", "-lc", "cat " + shellQuote(guestPath)}, &out, os.Stderr); err != nil {
		return err
	}
	if localPath == "-" {
		_, err := os.Stdout.Write(out.Bytes())
		return err
	}
	if err := os.MkdirAll(filepath.Dir(localPath), 0755); err != nil && filepath.Dir(localPath) != "." {
		return fmt.Errorf("destination parent create failed: %w", err)
	}
	if err := os.WriteFile(localPath, out.Bytes(), 0644); err != nil {
		return fmt.Errorf("destination write failed: %w", err)
	}
	cliui.PrintSuccess(fmt.Sprintf("copied %s:%s to %s", vmID, guestPath, localPath))
	return nil
}

func shellQuote(value string) string {
	return strconv.Quote(value)
}
