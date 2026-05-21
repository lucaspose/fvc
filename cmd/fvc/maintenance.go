package main

import (
	"context"
	"flag"
	"fmt"
	"os"

	"github.com/lucaspose/fvc/internal/cliui"
	"github.com/lucaspose/fvc/proto"
)

func executeImages(client proto.FvcServiceClient, args []string) error {
	fs := flag.NewFlagSet("images", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	if err := fs.Parse(args); err != nil {
		return err
	}

	res, err := client.ListImages(context.Background(), &proto.ListImagesRequest{})
	if err != nil {
		return fmt.Errorf("daemon request failed: %w", err)
	}

	fmt.Printf("%s%-24s %-10s %s%s\n", cliui.Color(cliui.ColorBold+cliui.ColorCyan), "IMAGE", "SIZE", "PATH", cliui.Color(cliui.ColorReset))
	fmt.Println(cliui.Color(cliui.ColorGray) + "--------------------------------------------------------------------------------" + cliui.Color(cliui.ColorReset))
	for _, image := range res.Images {
		fmt.Printf("%-24s %-10s %s\n", image.Image, cliui.FormatBytes(image.SizeBytes), image.Path)
	}
	return nil
}

func executePrune(client proto.FvcServiceClient, args []string) error {
	fs := flag.NewFlagSet("prune", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	dryRun := fs.Bool("dry-run", false, "Show what would be removed without deleting files")
	force := fs.Bool("force", false, "Do not ask for confirmation")
	if err := fs.Parse(args); err != nil {
		return err
	}

	if !*dryRun && !*force {
		ok, err := confirm("This will remove unused local fvc files. Continue? [y/N] ")
		if err != nil {
			return err
		}
		if !ok {
			cliui.PrintStep("PRUNE", "Cancelled")
			return nil
		}
	}

	message := "Cleaning unused local resources"
	if *dryRun {
		message = "Checking unused local resources"
	}
	cliui.PrintStep("PRUNE", message)
	res, err := client.Prune(context.Background(), &proto.PruneRequest{DryRun: *dryRun})
	if err != nil {
		return fmt.Errorf("daemon request failed: %w", err)
	}
	if !res.Success {
		return fmt.Errorf("prune failed: %s", res.Message)
	}
	cliui.PrintSuccess(res.Message)
	cliui.PrintKV("removed", fmt.Sprintf("%d files", res.RemovedFiles))
	cliui.PrintKV("freed", cliui.FormatBytes(res.FreedBytes))
	for _, item := range res.Items {
		fmt.Printf("  %s%-8s%s %-12s %s\n", cliui.Color(cliui.ColorDim), item.Kind+":", cliui.Color(cliui.ColorReset), cliui.FormatBytes(item.SizeBytes), item.Path)
	}
	return nil
}
