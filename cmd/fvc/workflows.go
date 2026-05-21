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

	"github.com/lucaspose/fvc/internal"
	"github.com/lucaspose/fvc/internal/cliui"
	"github.com/lucaspose/fvc/proto"
)

func executeRun(client proto.FvcServiceClient, args []string) error {
	fs := flag.NewFlagSet("run", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)

	flagImage := fs.String("image", "", "Name of the disk image to use")
	flagCPU := fs.Int("cpu", 0, "Number of CPU cores")
	flagRAM := fs.Int("ram", 0, "Amount of RAM in MB")
	flagName := fs.String("name", "", "Stable microVM name")
	publishAll := fs.Bool("publish-all", false, "Publish all ports exposed by image metadata")
	var flagPorts stringListFlag
	fs.Var(&flagPorts, "p", "Publish a TCP port as HOST:GUEST or PORT")
	fs.Var(&flagPorts, "publish", "Publish a TCP port as HOST:GUEST or PORT")

	if err := fs.Parse(args); err != nil {
		return err
	}

	finalImage := strings.TrimSpace(*flagImage)
	finalCPU := *flagCPU
	finalRAM := *flagRAM
	finalName := strings.TrimSpace(*flagName)
	finalPorts := []string(flagPorts)

	sourcePath := "."
	if len(fs.Args()) > 0 {
		sourcePath = fs.Args()[0]
	}

	if path, ok, err := findVmfile(sourcePath); err != nil {
		return fmt.Errorf("cannot read Vmfile source %q: %w", sourcePath, err)
	} else if ok {
		config, err := parseVmfile(path)
		if err != nil {
			return fmt.Errorf("invalid Vmfile %s: %w", path, err)
		}
		if finalImage == "" {
			finalImage = config.Image.Source
		}
		if finalCPU == 0 {
			finalCPU = config.VM.CPU
		}
		if finalRAM == 0 {
			finalRAM = config.VM.RAM
		}
		if finalName == "" {
			finalName = config.VM.Name
		}
		if len(finalPorts) == 0 {
			finalPorts = append(finalPorts, config.VM.Ports...)
		}
	}

	if finalCPU == 0 {
		finalCPU = 1
	}
	if finalRAM == 0 {
		finalRAM = 512
	}

	if finalImage == "" {
		return errors.New("missing image. Specify --image <name> or set [image].source in a Vmfile")
	}
	if err := internal.ValidateImageRef(finalImage); err != nil {
		return fmt.Errorf("invalid image name %q: %w", finalImage, err)
	}
	if err := internal.ValidateResources(int32(finalCPU), int32(finalRAM)); err != nil {
		return fmt.Errorf("invalid resources: %w", err)
	}
	if err := internal.ValidateVMName(finalName); err != nil {
		return fmt.Errorf("invalid vm name %q: %w", finalName, err)
	}
	normalizedPorts := make([]string, 0, len(finalPorts))
	for _, port := range finalPorts {
		normalized, err := internal.NormalizePortSpec(port)
		if err != nil {
			return fmt.Errorf("invalid port mapping %q: %w", port, err)
		}
		normalizedPorts = append(normalizedPorts, normalized)
	}

	cliui.PrintStep("CONFIG", "Resolving configuration")
	if finalName != "" {
		cliui.PrintKV("name", finalName)
	}
	cliui.PrintKV("image", finalImage)
	cliui.PrintKV("cpu", fmt.Sprintf("%d", finalCPU))
	cliui.PrintKV("ram", fmt.Sprintf("%d MB", finalRAM))
	if len(normalizedPorts) > 0 {
		cliui.PrintKV("ports", strings.Join(normalizedPorts, ", "))
	} else if *publishAll {
		cliui.PrintKV("ports", "publish all exposed ports")
	}

	stream, err := client.RunStream(context.Background(), &proto.RunRequest{
		Source: finalImage,
		Name:   finalName,
		Config: &proto.VmConfig{
			Cpus:       int32(finalCPU),
			MemoryMb:   int32(finalRAM),
			Ports:      normalizedPorts,
			PublishAll: *publishAll,
		},
	})
	if err != nil {
		return fmt.Errorf("daemon request failed: %w", err)
	}

	var vmID string
	progress := &cliui.ProgressLine{}
	defer progress.Finish()
	for {
		event, err := stream.Recv()
		if err == io.EOF {
			break
		}
		if err != nil {
			return fmt.Errorf("run stream failed: %w", err)
		}
		if event.GetVmId() != "" {
			vmID = event.GetVmId()
		}
		cliui.PrintRunEvent(progress, event)
		if event.GetStatus() == "error" {
			return fmt.Errorf("microVM start failed: %s", event.GetErrorMessage())
		}
	}
	if vmID == "" {
		return errors.New("microVM start failed: daemon did not return a VM ID")
	}
	cliui.PrintSuccess(fmt.Sprintf("microVM started: %s", vmID))
	cliui.PrintKV("status", "running")
	return nil
}

func executeBuild(client proto.FvcServiceClient, args []string) error {
	fs := flag.NewFlagSet("build", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	tag := fs.String("t", "", "Image tag to build")
	if err := fs.Parse(args); err != nil {
		return err
	}
	contextPath := "."
	if len(fs.Args()) > 0 {
		contextPath = fs.Args()[0]
	}
	absContext, err := filepath.Abs(contextPath)
	if err != nil {
		return fmt.Errorf("build context resolve failed: %w", err)
	}

	cliui.PrintStep("BUILD", fmt.Sprintf("Building image from %s", absContext))
	if strings.TrimSpace(*tag) != "" {
		cliui.PrintKV("tag", strings.TrimSpace(*tag))
	}
	stream, err := client.BuildImageStream(context.Background(), &proto.BuildImageRequest{
		ContextPath: absContext,
		Tag:         strings.TrimSpace(*tag),
	})
	if err != nil {
		return fmt.Errorf("daemon request failed: %w", err)
	}
	progress := &cliui.ProgressLine{}
	defer progress.Finish()
	var image, path string
	for {
		event, err := stream.Recv()
		if err == io.EOF {
			break
		}
		if err != nil {
			return fmt.Errorf("build stream failed: %w", err)
		}
		if event.GetImage() != "" {
			image = event.GetImage()
		}
		if event.GetPath() != "" {
			path = event.GetPath()
		}
		cliui.PrintOperationEvent(progress, event)
		if event.GetStatus() == "error" {
			return fmt.Errorf("build failed: %s", event.GetErrorMessage())
		}
	}
	if image != "" {
		cliui.PrintKV("image", image)
	}
	if path != "" {
		cliui.PrintKV("path", path)
	}
	return nil
}

func executePull(client proto.FvcServiceClient, args []string) error {
	options, err := parsePullOptions(args)
	if err != nil {
		return err
	}

	if options.source == "docker" {
		cliui.PrintStep("PULL", fmt.Sprintf("Converting Docker image %s", options.image))
		cliui.PrintKV("source", "docker")
		cliui.PrintKV("target", options.target)
	} else {
		cliui.PrintStep("PULL", fmt.Sprintf("Resolving image %s", options.image))
	}
	stream, err := client.PullImageStream(context.Background(), &proto.PullImageRequest{Image: options.image, Source: options.source, Target: options.target})
	if err != nil {
		return fmt.Errorf("daemon request failed: %w", err)
	}
	progress := &cliui.ProgressLine{}
	defer progress.Finish()
	var path string
	for {
		event, err := stream.Recv()
		if err == io.EOF {
			break
		}
		if err != nil {
			return fmt.Errorf("pull stream failed: %w", err)
		}
		if event.GetPath() != "" {
			path = event.GetPath()
		}
		cliui.PrintOperationEvent(progress, event)
		if event.GetStatus() == "error" {
			return fmt.Errorf("pull failed: %s", event.GetErrorMessage())
		}
	}
	if path != "" {
		cliui.PrintKV("path", path)
	}
	return nil
}

type pullOptions struct {
	image  string
	source string
	target string
}

func parsePullOptions(args []string) (pullOptions, error) {
	var options pullOptions
	for i := 0; i < len(args); i++ {
		arg := args[i]
		switch {
		case arg == "--from":
			i++
			if i >= len(args) {
				return pullOptions{}, errors.New("pull --from requires a source")
			}
			options.source = strings.TrimSpace(args[i])
		case strings.HasPrefix(arg, "--from="):
			options.source = strings.TrimSpace(strings.TrimPrefix(arg, "--from="))
		case arg == "-t" || arg == "--tag":
			i++
			if i >= len(args) {
				return pullOptions{}, errors.New("pull -t/--tag requires a local image tag")
			}
			options.target = strings.TrimSpace(args[i])
		case strings.HasPrefix(arg, "-t="):
			options.target = strings.TrimSpace(strings.TrimPrefix(arg, "-t="))
		case strings.HasPrefix(arg, "--tag="):
			options.target = strings.TrimSpace(strings.TrimPrefix(arg, "--tag="))
		case strings.HasPrefix(arg, "-"):
			return pullOptions{}, fmt.Errorf("unknown pull flag %q", arg)
		default:
			if options.image != "" {
				return pullOptions{}, fmt.Errorf("pull accepts one image, got %q and %q", options.image, arg)
			}
			options.image = strings.TrimSpace(arg)
		}
	}
	if options.image == "" {
		return pullOptions{}, errors.New("pull requires an image name. Example: fvc pull ubuntu or fvc pull --from docker ubuntu:24.04 -t ubuntu-fvc:24.04")
	}
	if options.source != "" && options.source != "docker" {
		return pullOptions{}, fmt.Errorf("unsupported pull source %q", options.source)
	}
	if options.source == "docker" {
		if options.target == "" {
			options.target = options.image
		}
		if err := internal.ValidateImageRef(options.target); err != nil {
			return pullOptions{}, fmt.Errorf("invalid local image tag %q: %w", options.target, err)
		}
	}
	return options, nil
}
