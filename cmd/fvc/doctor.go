package main

import (
	"context"
	"flag"
	"fmt"
	"os"

	"github.com/lucaspose/fvc/internal/cliui"
	"github.com/lucaspose/fvc/proto"
)

func executeDoctor(client proto.FvcServiceClient, args []string) error {
	fs := flag.NewFlagSet("doctor", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	if err := fs.Parse(args); err != nil {
		return err
	}
	target := grpcTarget()
	cliui.PrintStep("DOCTOR", "Checking fvc host runtime")
	cliui.PrintKV("target", target)
	if daemonReachable(target) {
		cliui.PrintKV("daemon", "reachable")
	} else {
		cliui.PrintKV("daemon", "not reachable yet")
	}
	res, err := client.Diagnostics(context.Background(), &proto.DiagnosticsRequest{})
	if err != nil {
		return fmt.Errorf("daemon diagnostics failed: %w", err)
	}
	cliui.PrintKV("grpc", fmt.Sprintf("%s %s", res.GrpcNetwork, res.GrpcAddress))
	cliui.PrintKV("data", res.DataDir)
	cliui.PrintKV("runtime", res.RuntimeDir)
	cliui.PrintKV("network", fmt.Sprintf("%t", res.NetworkEnabled))
	failed := 0
	for _, check := range res.Checks {
		label := "OK"
		code := cliui.ColorGreen
		if !check.Ok {
			label = "ERR"
			code = cliui.ColorRed
			failed++
		}
		fmt.Printf("%s %-18s %s\n", cliui.ColorLabel(label, code), check.Name, check.Message)
	}
	if failed > 0 {
		return fmt.Errorf("doctor found %d runtime problem(s)", failed)
	}
	cliui.PrintSuccess("runtime looks ready")
	return nil
}
