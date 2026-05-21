package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"

	"github.com/lucaspose/fvc/proto"
)

func executeLogs(client proto.FvcServiceClient, args []string) error {
	fs := flag.NewFlagSet("logs", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	follow := fs.Bool("follow", false, "Follow log output")
	tail := fs.Int("tail", 100, "Number of lines to show")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if len(fs.Args()) < 1 {
		return errors.New("logs requires a microVM ID. Example: fvc logs --tail 100 <id>")
	}
	if *tail < 0 {
		return errors.New("tail must be zero or greater")
	}

	stream, err := client.StreamLogs(context.Background(), &proto.LogsRequest{
		VmId:   fs.Args()[0],
		Follow: *follow,
		Tail:   int32(*tail),
	})
	if err != nil {
		return fmt.Errorf("daemon request failed: %w", err)
	}
	for {
		line, err := stream.Recv()
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return fmt.Errorf("log stream failed: %w", err)
		}
		fmt.Println(line.Line)
	}
}
