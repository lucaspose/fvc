package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"

	"github.com/lucaspose/fvc/internal/cliui"
	"github.com/lucaspose/fvc/proto"
)

func executeStop(client proto.FvcServiceClient, args []string) error {
	fs := flag.NewFlagSet("stop", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	timeout := fs.Int("timeout", 10, "Seconds to wait before killing the VM")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if len(fs.Args()) < 1 {
		return errors.New("stop requires a microVM ID. Example: fvc stop --timeout 10 <id>")
	}

	res, err := client.Stop(context.Background(), &proto.StopRequest{
		VmId:           fs.Args()[0],
		TimeoutSeconds: int32(*timeout),
	})
	if err != nil {
		return fmt.Errorf("daemon request failed: %w", err)
	}
	if !res.Success {
		return fmt.Errorf("microVM stop failed: %s", res.Message)
	}
	cliui.PrintSuccess(res.Message)
	return nil
}

func executeStart(client proto.FvcServiceClient, args []string) error {
	fs := flag.NewFlagSet("start", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	if err := fs.Parse(args); err != nil {
		return err
	}
	if len(fs.Args()) < 1 {
		return errors.New("start requires a microVM ID. Example: fvc start <id>")
	}

	res, err := client.Start(context.Background(), &proto.StartRequest{VmId: fs.Args()[0]})
	if err != nil {
		return fmt.Errorf("daemon request failed: %w", err)
	}
	if !res.Success {
		return fmt.Errorf("microVM start failed: %s", res.Message)
	}
	cliui.PrintSuccess(res.Message)
	return nil
}

func executeRestart(client proto.FvcServiceClient, args []string) error {
	fs := flag.NewFlagSet("restart", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	timeout := fs.Int("timeout", 10, "Seconds to wait before killing the VM")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if len(fs.Args()) < 1 {
		return errors.New("restart requires a microVM ID. Example: fvc restart <id>")
	}
	vmID := fs.Args()[0]
	stop, err := client.Stop(context.Background(), &proto.StopRequest{VmId: vmID, TimeoutSeconds: int32(*timeout)})
	if err != nil {
		return fmt.Errorf("daemon request failed: %w", err)
	}
	if !stop.Success {
		return fmt.Errorf("microVM restart failed during stop: %s", stop.Message)
	}
	cliui.PrintSuccess(stop.Message)
	start, err := client.Start(context.Background(), &proto.StartRequest{VmId: vmID})
	if err != nil {
		return fmt.Errorf("daemon request failed: %w", err)
	}
	if !start.Success {
		return fmt.Errorf("microVM restart failed during start: %s", start.Message)
	}
	cliui.PrintSuccess(start.Message)
	return nil
}

func executeRm(client proto.FvcServiceClient, args []string) error {
	fs := flag.NewFlagSet("rm", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	if err := fs.Parse(args); err != nil {
		return err
	}
	if len(fs.Args()) < 1 {
		return errors.New("rm requires a microVM ID. Example: fvc rm <id>")
	}

	res, err := client.Rm(context.Background(), &proto.RmRequest{VmId: fs.Args()[0]})
	if err != nil {
		return fmt.Errorf("daemon request failed: %w", err)
	}
	if !res.Success {
		return fmt.Errorf("microVM remove failed: %s", res.Message)
	}
	cliui.PrintSuccess(res.Message)
	return nil
}

func executeKill(client proto.FvcServiceClient, args []string) error {
	fs := flag.NewFlagSet("kill", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	if err := fs.Parse(args); err != nil {
		return err
	}
	if len(fs.Args()) < 1 {
		return errors.New("kill requires a microVM ID. Example: fvc kill <id>")
	}

	res, err := client.Kill(context.Background(), &proto.KillRequest{VmId: fs.Args()[0]})
	if err != nil {
		return fmt.Errorf("daemon request failed: %w", err)
	}
	if !res.Success {
		return fmt.Errorf("microVM kill failed: %s", res.Message)
	}
	cliui.PrintSuccess(res.Message)
	return nil
}

func executeWait(client proto.FvcServiceClient, args []string) error {
	fs := flag.NewFlagSet("wait", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	timeout := fs.Int("timeout", 0, "Maximum seconds to wait; zero waits forever")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if len(fs.Args()) < 1 {
		return errors.New("wait requires a microVM ID. Example: fvc wait --timeout 30 <id>")
	}
	if *timeout < 0 {
		return errors.New("timeout must be zero or greater")
	}

	cliui.PrintStep("WAIT", fmt.Sprintf("Waiting for %s", fs.Args()[0]))
	res, err := client.Wait(context.Background(), &proto.WaitRequest{VmId: fs.Args()[0], TimeoutSeconds: int32(*timeout)})
	if err != nil {
		return fmt.Errorf("daemon request failed: %w", err)
	}
	if !res.Success {
		return fmt.Errorf("wait failed: %s", res.Message)
	}
	cliui.PrintSuccess(res.Message)
	cliui.PrintKV("status", res.Status)
	if res.ExitCode >= 0 {
		cliui.PrintKV("exit", fmt.Sprintf("%d", res.ExitCode))
	}
	return nil
}

func executeRename(client proto.FvcServiceClient, args []string) error {
	fs := flag.NewFlagSet("rename", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	if err := fs.Parse(args); err != nil {
		return err
	}
	if len(fs.Args()) != 2 {
		return errors.New("rename requires a microVM ID and a new name. Example: fvc rename <id> <name>")
	}
	res, err := client.Rename(context.Background(), &proto.RenameRequest{VmId: fs.Args()[0], Name: fs.Args()[1]})
	if err != nil {
		return fmt.Errorf("daemon request failed: %w", err)
	}
	if !res.Success {
		return fmt.Errorf("microVM rename failed: %s", res.Message)
	}
	cliui.PrintSuccess(res.Message)
	return nil
}

func executeUpdate(client proto.FvcServiceClient, args []string) error {
	fs := flag.NewFlagSet("update", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	cpus := fs.Int("cpu", 0, "New CPU count")
	memory := fs.Int("ram", 0, "New memory in MB")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if len(fs.Args()) != 1 {
		return errors.New("update requires a microVM ID. Example: fvc update --cpu 2 --ram 1024 <id>")
	}
	res, err := client.Update(context.Background(), &proto.UpdateRequest{VmId: fs.Args()[0], Cpus: int32(*cpus), MemoryMb: int32(*memory)})
	if err != nil {
		return fmt.Errorf("daemon request failed: %w", err)
	}
	if !res.Success {
		return fmt.Errorf("microVM update failed: %s", res.Message)
	}
	cliui.PrintSuccess(res.Message)
	return nil
}
