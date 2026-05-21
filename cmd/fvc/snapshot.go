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

func executeSnapshot(client proto.FvcServiceClient, args []string) error {
	if len(args) < 1 {
		return errors.New("snapshot requires a subcommand: create, ls, restore, rm")
	}
	switch args[0] {
	case "create":
		return executeSnapshotCreate(client, args[1:])
	case "ls", "list":
		return executeSnapshotList(client, args[1:])
	case "restore":
		return executeSnapshotRestore(client, args[1:])
	case "rm", "remove":
		return executeSnapshotRemove(client, args[1:])
	default:
		return fmt.Errorf("unknown snapshot subcommand %q", args[0])
	}
}

func executeSnapshotCreate(client proto.FvcServiceClient, args []string) error {
	fs := flag.NewFlagSet("snapshot create", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	if err := fs.Parse(args); err != nil {
		return err
	}
	if len(fs.Args()) < 2 {
		return errors.New("snapshot create requires a VM ID and name. Example: fvc snapshot create <id> before-upgrade")
	}
	vmID, name := fs.Args()[0], fs.Args()[1]
	cliui.PrintStep("SNAPSHOT", fmt.Sprintf("Creating %s for %s", name, vmID))
	res, err := client.SnapshotCreate(context.Background(), &proto.SnapshotCreateRequest{VmId: vmID, Name: name})
	if err != nil {
		return fmt.Errorf("daemon request failed: %w", err)
	}
	if !res.Success {
		return fmt.Errorf("snapshot create failed: %s", res.Message)
	}
	cliui.PrintSuccess(res.Message)
	printSnapshot(res.Snapshot)
	return nil
}

func executeSnapshotList(client proto.FvcServiceClient, args []string) error {
	fs := flag.NewFlagSet("snapshot ls", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	if err := fs.Parse(args); err != nil {
		return err
	}
	if len(fs.Args()) < 1 {
		return errors.New("snapshot ls requires a VM ID. Example: fvc snapshot ls <id>")
	}
	res, err := client.SnapshotList(context.Background(), &proto.SnapshotListRequest{VmId: fs.Args()[0]})
	if err != nil {
		return fmt.Errorf("daemon request failed: %w", err)
	}
	if !res.Success {
		return fmt.Errorf("snapshot list failed: %s", res.Message)
	}
	fmt.Printf("%s%-24s %-10s %s%s\n", cliui.Color(cliui.ColorBold+cliui.ColorCyan), "SNAPSHOT", "SIZE", "PATH", cliui.Color(cliui.ColorReset))
	fmt.Println(cliui.Color(cliui.ColorGray) + "--------------------------------------------------------------------------------" + cliui.Color(cliui.ColorReset))
	for _, snapshot := range res.Snapshots {
		fmt.Printf("%-24s %-10s %s\n", snapshot.Name, cliui.FormatBytes(snapshot.SizeBytes), snapshot.Path)
	}
	return nil
}

func executeSnapshotRestore(client proto.FvcServiceClient, args []string) error {
	fs := flag.NewFlagSet("snapshot restore", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	if err := fs.Parse(args); err != nil {
		return err
	}
	if len(fs.Args()) < 2 {
		return errors.New("snapshot restore requires a VM ID and name. Example: fvc snapshot restore <id> before-upgrade")
	}
	vmID, name := fs.Args()[0], fs.Args()[1]
	cliui.PrintStep("SNAPSHOT", fmt.Sprintf("Restoring %s on %s", name, vmID))
	res, err := client.SnapshotRestore(context.Background(), &proto.SnapshotRestoreRequest{VmId: vmID, Name: name})
	if err != nil {
		return fmt.Errorf("daemon request failed: %w", err)
	}
	if !res.Success {
		return fmt.Errorf("snapshot restore failed: %s", res.Message)
	}
	cliui.PrintSuccess(res.Message)
	printSnapshot(res.Snapshot)
	return nil
}

func executeSnapshotRemove(client proto.FvcServiceClient, args []string) error {
	fs := flag.NewFlagSet("snapshot rm", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	if err := fs.Parse(args); err != nil {
		return err
	}
	if len(fs.Args()) < 2 {
		return errors.New("snapshot rm requires a VM ID and name. Example: fvc snapshot rm <id> before-upgrade")
	}
	res, err := client.SnapshotRemove(context.Background(), &proto.SnapshotRemoveRequest{VmId: fs.Args()[0], Name: fs.Args()[1]})
	if err != nil {
		return fmt.Errorf("daemon request failed: %w", err)
	}
	if !res.Success {
		return fmt.Errorf("snapshot remove failed: %s", res.Message)
	}
	cliui.PrintSuccess(res.Message)
	return nil
}

func printSnapshot(snapshot *proto.SnapshotDetails) {
	if snapshot == nil {
		return
	}
	cliui.PrintKV("name", snapshot.Name)
	cliui.PrintKV("size", cliui.FormatBytes(snapshot.SizeBytes))
	cliui.PrintKV("path", snapshot.Path)
}
