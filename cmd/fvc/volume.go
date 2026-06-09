package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"time"

	"github.com/lucaspose/fvc/internal/cliui"
	"github.com/lucaspose/fvc/proto"
)

func executeVolume(client proto.FvcServiceClient, args []string) error {
	if len(args) < 1 {
		return errors.New("volume requires a subcommand: create, ls, inspect, rm, prune")
	}
	switch args[0] {
	case "create":
		return executeVolumeCreate(client, args[1:])
	case "ls", "list":
		return executeVolumeList(client, args[1:])
	case "inspect":
		return executeVolumeInspect(client, args[1:])
	case "rm", "remove":
		return executeVolumeRemove(client, args[1:])
	case "prune":
		return executeVolumePrune(client, args[1:])
	default:
		return fmt.Errorf("unknown volume subcommand %q", args[0])
	}
}

func executeVolumeCreate(client proto.FvcServiceClient, args []string) error {
	fs := flag.NewFlagSet("volume create", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	sizeMB := fs.Int64("size", 128, "Volume size in MB")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if len(fs.Args()) != 1 {
		return errors.New("volume create requires a name. Example: fvc volume create --size 1024 data")
	}
	res, err := client.VolumeCreate(context.Background(), &proto.VolumeCreateRequest{Name: fs.Args()[0], SizeMb: *sizeMB})
	if err != nil {
		return fmt.Errorf("daemon request failed: %w", err)
	}
	if !res.Success {
		return fmt.Errorf("volume create failed: %s", res.Message)
	}
	cliui.PrintSuccess(res.Message)
	printVolumeDetails(res.Volume)
	return nil
}

func executeVolumeList(client proto.FvcServiceClient, args []string) error {
	fs := flag.NewFlagSet("volume ls", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	if err := fs.Parse(args); err != nil {
		return err
	}
	res, err := client.VolumeList(context.Background(), &proto.VolumeListRequest{})
	if err != nil {
		return fmt.Errorf("daemon request failed: %w", err)
	}
	if !res.Success {
		return fmt.Errorf("volume list failed: %s", res.Message)
	}
	fmt.Printf("%s%-24s %-10s %-8s %-8s %s%s\n", cliui.Color(cliui.ColorBold+cliui.ColorCyan), "NAME", "SIZE", "USED", "RUNNING", "PATH", cliui.Color(cliui.ColorReset))
	fmt.Println(cliui.Color(cliui.ColorGray) + "--------------------------------------------------------------------------------" + cliui.Color(cliui.ColorReset))
	for _, volume := range res.Volumes {
		fmt.Printf("%-24s %-10s %-8d %-8t %s\n", volume.Name, cliui.FormatBytes(volume.SizeBytes), volume.UsedBy, volume.AttachedToRunning, volume.Path)
	}
	return nil
}

func executeVolumeInspect(client proto.FvcServiceClient, args []string) error {
	fs := flag.NewFlagSet("volume inspect", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	if err := fs.Parse(args); err != nil {
		return err
	}
	if len(fs.Args()) != 1 {
		return errors.New("volume inspect requires a name. Example: fvc volume inspect data")
	}
	res, err := client.VolumeInspect(context.Background(), &proto.VolumeInspectRequest{Name: fs.Args()[0]})
	if err != nil {
		return fmt.Errorf("daemon request failed: %w", err)
	}
	if !res.Success {
		return fmt.Errorf("volume inspect failed: %s", res.Message)
	}
	printVolumeDetails(res.Volume)
	return nil
}

func executeVolumeRemove(client proto.FvcServiceClient, args []string) error {
	fs := flag.NewFlagSet("volume rm", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	force := fs.Bool("force", false, "Remove even if the volume is referenced by stopped VMs")
	fs.BoolVar(force, "f", false, "Remove even if the volume is referenced by stopped VMs")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if len(fs.Args()) != 1 {
		return errors.New("volume rm requires a name. Example: fvc volume rm data")
	}
	res, err := client.VolumeRemove(context.Background(), &proto.VolumeRemoveRequest{Name: fs.Args()[0], Force: *force})
	if err != nil {
		return fmt.Errorf("daemon request failed: %w", err)
	}
	if !res.Success {
		return fmt.Errorf("volume remove failed: %s", res.Message)
	}
	cliui.PrintSuccess(res.Message)
	if res.Volume != nil {
		cliui.PrintKV("freed", cliui.FormatBytes(res.Volume.SizeBytes))
	}
	return nil
}

func executeVolumePrune(client proto.FvcServiceClient, args []string) error {
	fs := flag.NewFlagSet("volume prune", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	dryRun := fs.Bool("dry-run", false, "Show volumes that would be removed")
	force := fs.Bool("force", false, "Do not ask for confirmation")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if !*dryRun && !*force {
		ok, err := confirm("This will remove unused local volumes. Continue? [y/N] ")
		if err != nil {
			return err
		}
		if !ok {
			cliui.PrintStep("VOLUME", "Prune cancelled")
			return nil
		}
	}
	res, err := client.VolumePrune(context.Background(), &proto.VolumePruneRequest{DryRun: *dryRun})
	if err != nil {
		return fmt.Errorf("daemon request failed: %w", err)
	}
	if !res.Success {
		return fmt.Errorf("volume prune failed: %s", res.Message)
	}
	cliui.PrintSuccess(res.Message)
	cliui.PrintKV("removed", fmt.Sprintf("%d volumes", res.RemovedVolumes))
	cliui.PrintKV("freed", cliui.FormatBytes(res.FreedBytes))
	for _, volume := range res.Volumes {
		fmt.Printf("  %s%-8s%s %-10s %s\n", cliui.Color(cliui.ColorDim), volume.Name+":", cliui.Color(cliui.ColorReset), cliui.FormatBytes(volume.SizeBytes), volume.Path)
	}
	return nil
}

func printVolumeDetails(volume *proto.VolumeDetails) {
	if volume == nil {
		return
	}
	cliui.PrintStep("VOLUME", volume.Name)
	cliui.PrintKV("path", volume.Path)
	cliui.PrintKV("size", cliui.FormatBytes(volume.SizeBytes))
	cliui.PrintKV("used-by", fmt.Sprintf("%d VM(s)", volume.UsedBy))
	cliui.PrintKV("running", fmt.Sprintf("%t", volume.AttachedToRunning))
	if volume.CreatedAt != nil {
		cliui.PrintKV("created", formatVolumeTimestamp(volume.CreatedAt.AsTime()))
	}
}

func formatVolumeTimestamp(value time.Time) string {
	if value.IsZero() {
		return ""
	}
	return value.Local().Format("2006-01-02 15:04:05")
}
