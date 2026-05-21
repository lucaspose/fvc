package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/lucaspose/fvc/internal/cliui"
	"github.com/lucaspose/fvc/proto"
)

func executeImage(client proto.FvcServiceClient, args []string) error {
	if len(args) < 1 {
		return errors.New("image requires a subcommand: inspect, rm, tag, import, export, history, prune")
	}
	switch args[0] {
	case "inspect":
		return executeImageInspect(client, args[1:])
	case "rm", "remove":
		return executeImageRemove(client, args[1:])
	case "tag":
		return executeImageTag(client, args[1:])
	case "import":
		return executeImageImport(client, args[1:])
	case "export":
		return executeImageExport(client, args[1:])
	case "history":
		return executeImageHistory(client, args[1:])
	case "prune":
		return executeImagePrune(client, args[1:])
	default:
		return fmt.Errorf("unknown image subcommand %q", args[0])
	}
}

func executeImageInspect(client proto.FvcServiceClient, args []string) error {
	fs := flag.NewFlagSet("image inspect", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	if err := fs.Parse(args); err != nil {
		return err
	}
	if len(fs.Args()) < 1 {
		return errors.New("image inspect requires an image name. Example: fvc image inspect ubuntu")
	}
	res, err := client.ImageInspect(context.Background(), &proto.ImageInspectRequest{Image: fs.Args()[0]})
	if err != nil {
		return fmt.Errorf("daemon request failed: %w", err)
	}
	if !res.Success {
		return fmt.Errorf("image inspect failed: %s", res.Message)
	}
	printImageDetails(res.Image)
	return nil
}

func executeImageRemove(client proto.FvcServiceClient, args []string) error {
	fs := flag.NewFlagSet("image rm", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	force := fs.Bool("force", false, "Remove even if the image is referenced by VMs")
	fs.BoolVar(force, "f", false, "Remove even if the image is referenced by VMs")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if len(fs.Args()) < 1 {
		return errors.New("image rm requires an image name. Example: fvc image rm ubuntu-web")
	}
	res, err := client.ImageRemove(context.Background(), &proto.ImageRemoveRequest{Image: fs.Args()[0], Force: *force})
	if err != nil {
		return fmt.Errorf("daemon request failed: %w", err)
	}
	if !res.Success {
		return fmt.Errorf("image remove failed: %s", res.Message)
	}
	cliui.PrintSuccess(res.Message)
	return nil
}

func executeImageTag(client proto.FvcServiceClient, args []string) error {
	fs := flag.NewFlagSet("image tag", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	if err := fs.Parse(args); err != nil {
		return err
	}
	if len(fs.Args()) < 2 {
		return errors.New("image tag requires source and target. Example: fvc image tag ubuntu ubuntu-copy")
	}
	res, err := client.ImageTag(context.Background(), &proto.ImageTagRequest{Source: fs.Args()[0], Target: fs.Args()[1]})
	if err != nil {
		return fmt.Errorf("daemon request failed: %w", err)
	}
	if !res.Success {
		return fmt.Errorf("image tag failed: %s", res.Message)
	}
	cliui.PrintSuccess(res.Message)
	printImageDetails(res.Image)
	return nil
}

func executeImageImport(client proto.FvcServiceClient, args []string) error {
	fs := flag.NewFlagSet("image import", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	if err := fs.Parse(args); err != nil {
		return err
	}
	if len(fs.Args()) < 2 {
		return errors.New("image import requires a rootfs and image name. Example: fvc image import ./rootfs.ext4 custom")
	}
	sourcePath, err := filepath.Abs(fs.Args()[0])
	if err != nil {
		return fmt.Errorf("source path resolve failed: %w", err)
	}
	res, err := client.ImageImport(context.Background(), &proto.ImageImportRequest{SourcePath: sourcePath, Image: fs.Args()[1]})
	if err != nil {
		return fmt.Errorf("daemon request failed: %w", err)
	}
	if !res.Success {
		return fmt.Errorf("image import failed: %s", res.Message)
	}
	cliui.PrintSuccess(res.Message)
	printImageDetails(res.Image)
	return nil
}

func executeImageExport(client proto.FvcServiceClient, args []string) error {
	fs := flag.NewFlagSet("image export", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	if err := fs.Parse(args); err != nil {
		return err
	}
	if len(fs.Args()) < 2 {
		return errors.New("image export requires image and destination. Example: fvc image export ubuntu ./ubuntu.ext4")
	}
	destPath, err := filepath.Abs(fs.Args()[1])
	if err != nil {
		return fmt.Errorf("destination path resolve failed: %w", err)
	}
	res, err := client.ImageExport(context.Background(), &proto.ImageExportRequest{Image: fs.Args()[0], DestPath: destPath})
	if err != nil {
		return fmt.Errorf("daemon request failed: %w", err)
	}
	if !res.Success {
		return fmt.Errorf("image export failed: %s", res.Message)
	}
	cliui.PrintSuccess(res.Message)
	cliui.PrintKV("path", res.Path)
	return nil
}

func executeImageHistory(client proto.FvcServiceClient, args []string) error {
	fs := flag.NewFlagSet("image history", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	if err := fs.Parse(args); err != nil {
		return err
	}
	if len(fs.Args()) < 1 {
		return errors.New("image history requires an image name. Example: fvc image history ubuntu")
	}
	res, err := client.ImageHistory(context.Background(), &proto.ImageHistoryRequest{Image: fs.Args()[0]})
	if err != nil {
		return fmt.Errorf("daemon request failed: %w", err)
	}
	if !res.Success {
		return fmt.Errorf("image history failed: %s", res.Message)
	}
	fmt.Printf("%s%-22s %-14s %s%s\n", cliui.Color(cliui.ColorBold+cliui.ColorCyan), "CREATED", "ACTION", "MESSAGE", cliui.Color(cliui.ColorReset))
	fmt.Println(cliui.Color(cliui.ColorGray) + "--------------------------------------------------------------------------------" + cliui.Color(cliui.ColorReset))
	for _, entry := range res.Entries {
		fmt.Printf("%-22s %-14s %s\n", formatTimestamp(entry.CreatedAt.AsTime()), entry.Action, entry.Message)
	}
	return nil
}

func executeImagePrune(client proto.FvcServiceClient, args []string) error {
	fs := flag.NewFlagSet("image prune", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	dryRun := fs.Bool("dry-run", false, "Show images that would be removed")
	force := fs.Bool("force", false, "Do not ask for confirmation")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if !*dryRun && !*force {
		ok, err := confirm("This will remove unused local images. Continue? [y/N] ")
		if err != nil {
			return err
		}
		if !ok {
			cliui.PrintStep("IMAGE", "Prune cancelled")
			return nil
		}
	}
	res, err := client.ImagePrune(context.Background(), &proto.ImagePruneRequest{DryRun: *dryRun})
	if err != nil {
		return fmt.Errorf("daemon request failed: %w", err)
	}
	if !res.Success {
		return fmt.Errorf("image prune failed: %s", res.Message)
	}
	cliui.PrintSuccess(res.Message)
	cliui.PrintKV("removed", fmt.Sprintf("%d images", res.RemovedImages))
	cliui.PrintKV("freed", cliui.FormatBytes(res.FreedBytes))
	for _, image := range res.Images {
		fmt.Printf("  %s%-8s%s %-10s %s\n", cliui.Color(cliui.ColorDim), image.Image+":", cliui.Color(cliui.ColorReset), cliui.FormatBytes(image.SizeBytes), image.Path)
	}
	return nil
}

func printImageDetails(image *proto.ImageDetails) {
	if image == nil {
		return
	}
	cliui.PrintStep("IMAGE", image.Image)
	cliui.PrintKV("path", image.Path)
	cliui.PrintKV("size", cliui.FormatBytes(image.SizeBytes))
	cliui.PrintKV("digest", image.Digest)
	cliui.PrintKV("source", image.Source)
	if image.Workdir != "" {
		cliui.PrintKV("workdir", image.Workdir)
	}
	if len(image.Cmd) > 0 {
		cliui.PrintKV("cmd", fmt.Sprintf("%q", image.Cmd))
	}
	for _, env := range image.Env {
		cliui.PrintKV("env", env)
	}
	if len(image.ExposedPorts) > 0 {
		cliui.PrintKV("expose", formatExposedPorts(image.ExposedPorts))
	}
	if image.CreatedAt != nil {
		cliui.PrintKV("created", formatTimestamp(image.CreatedAt.AsTime()))
	}
	for key, value := range image.Labels {
		cliui.PrintKV("label", fmt.Sprintf("%s=%s", key, value))
	}
}

func formatExposedPorts(ports []int32) string {
	values := make([]string, 0, len(ports))
	for _, port := range ports {
		values = append(values, fmt.Sprintf("%d", port))
	}
	return strings.Join(values, ", ")
}

func formatTimestamp(value time.Time) string {
	if value.IsZero() {
		return ""
	}
	return value.Local().Format("2006-01-02 15:04:05")
}
