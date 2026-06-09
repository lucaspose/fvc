package main

import (
	"context"
	"errors"
	"fmt"
	"io"

	"github.com/lucaspose/fvc/proto"
)

func streamGuestCommand(client proto.FvcServiceClient, vmID string, command []string, stdout, stderr io.Writer) error {
	stream, err := client.Exec(context.Background(), &proto.ExecRequest{VmId: vmID, Command: command})
	if err != nil {
		return fmt.Errorf("daemon request failed: %w", err)
	}
	exitCode := int32(0)
	for {
		event, err := stream.Recv()
		if err == io.EOF {
			break
		}
		if err != nil {
			return fmt.Errorf("exec stream failed: %w", err)
		}
		if event.GetErrorMessage() != "" {
			return errors.New(event.GetErrorMessage())
		}
		switch event.GetStream() {
		case "stdout":
			_, _ = stdout.Write(event.GetData())
		case "stderr":
			_, _ = stderr.Write(event.GetData())
		case "exit":
			exitCode = event.GetExitCode()
		}
	}
	if exitCode != 0 {
		return cliExitError{code: int(exitCode)}
	}
	return nil
}
