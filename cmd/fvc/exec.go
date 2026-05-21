package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/lucaspose/fvc/proto"
)

func executeExec(client proto.FvcServiceClient, args []string) error {
	fs := flag.NewFlagSet("exec", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	workdir := fs.String("w", "", "Working directory inside the microVM")
	fs.StringVar(workdir, "workdir", "", "Working directory inside the microVM")
	var env stringListFlag
	fs.Var(&env, "e", "Environment variable KEY=VALUE")
	fs.Var(&env, "env", "Environment variable KEY=VALUE")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if len(fs.Args()) < 2 {
		return errors.New("exec requires a microVM ID and command. Example: fvc exec <id> -- uname -a")
	}
	vmID := fs.Args()[0]
	command, err := normalizeExecCommand(fs.Args()[1:])
	if err != nil {
		return err
	}
	for _, entry := range env {
		if !strings.Contains(entry, "=") || strings.HasPrefix(entry, "=") || strings.ContainsAny(entry, "\x00\r\n") {
			return fmt.Errorf("invalid env entry %q", entry)
		}
	}
	if strings.TrimSpace(*workdir) != "" && !filepath.IsAbs(*workdir) {
		return errors.New("workdir must be an absolute path")
	}

	stream, err := client.Exec(context.Background(), &proto.ExecRequest{
		VmId:    vmID,
		Command: command,
		Env:     []string(env),
		Workdir: strings.TrimSpace(*workdir),
	})
	if err != nil {
		return fmt.Errorf("daemon request failed: %w", err)
	}
	exitCode := int32(127)
	sawExit := false
	for {
		event, err := stream.Recv()
		if err == io.EOF {
			break
		}
		if err != nil {
			return fmt.Errorf("exec stream failed: %w", err)
		}
		switch event.GetStream() {
		case "stdout":
			if _, err := os.Stdout.Write(event.GetData()); err != nil {
				return err
			}
		case "stderr":
			if _, err := os.Stderr.Write(event.GetData()); err != nil {
				return err
			}
		case "exit":
			exitCode = event.GetExitCode()
			sawExit = true
			if event.GetErrorMessage() != "" {
				fmt.Fprintln(os.Stderr, event.GetErrorMessage())
			}
		}
	}
	if !sawExit {
		return errors.New("exec stream ended without an exit event")
	}
	if exitCode != 0 {
		return cliExitError{code: int(exitCode)}
	}
	return nil
}

func normalizeExecCommand(args []string) ([]string, error) {
	command := append([]string(nil), args...)
	if len(command) > 0 && command[0] == "--" {
		command = command[1:]
	}
	if len(command) == 0 {
		return nil, errors.New("exec requires a command after --")
	}
	return command, nil
}
