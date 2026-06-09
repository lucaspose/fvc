package main

import (
	"errors"
	"flag"
	"os"

	"github.com/lucaspose/fvc/proto"
)

func executeTop(client proto.FvcServiceClient, args []string) error {
	fs := flag.NewFlagSet("top", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	if err := fs.Parse(args); err != nil {
		return err
	}
	if len(fs.Args()) != 1 {
		return errors.New("top requires a microVM ID. Example: fvc top <id>")
	}
	command := "if command -v ps >/dev/null 2>&1; then ps w || ps aux; elif command -v busybox >/dev/null 2>&1; then busybox ps; else echo 'ps command not found' >&2; exit 127; fi"
	return streamGuestCommand(client, fs.Args()[0], []string{"/bin/sh", "-lc", command}, os.Stdout, os.Stderr)
}
