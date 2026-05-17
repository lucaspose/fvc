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
	"time"

	"github.com/BurntSushi/toml"
	"github.com/lucaspose/fvc/internal"
	"github.com/lucaspose/fvc/proto"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

type commandFunc func(client proto.FvcServiceClient, args []string) error

const (
	colorReset = "\033[0m"
	colorBold  = "\033[1m"
	colorDim   = "\033[2m"
	colorRed   = "\033[31m"
	colorGreen = "\033[32m"
	colorCyan  = "\033[36m"
	colorGray  = "\033[90m"
)

type progressLine struct {
	active bool
	label  string
	done   chan struct{}
}

type VMSection struct {
	Name string `toml:"name"`
	CPU  int    `toml:"cpu"`
	RAM  int    `toml:"ram"`
	Disk int    `toml:"disk"`
}

type ImageSection struct {
	Source string `toml:"source"`
}

type VmfileConfig struct {
	VM    VMSection    `toml:"vm"`
	Image ImageSection `toml:"image"`
}

func newClient() (proto.FvcServiceClient, *grpc.ClientConn, error) {
	addr := envOrDefault("FVC_GRPC_ADDR", "127.0.0.1:50051")
	conn, err := grpc.NewClient(addr, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		return nil, nil, fmt.Errorf("daemon is not reachable at %s: %w", addr, err)
	}
	client := proto.NewFvcServiceClient(conn)
	return client, conn, nil
}

func parseVmfile(path string) (*VmfileConfig, error) {
	var config VmfileConfig
	if _, err := toml.DecodeFile(path, &config); err != nil {
		return nil, err
	}
	if err := validateVmfile(config); err != nil {
		return nil, err
	}
	return &config, nil
}

func validateVmfile(config VmfileConfig) error {
	var problems []string

	if strings.TrimSpace(config.Image.Source) == "" {
		problems = append(problems, "image.source is required")
	} else if err := internal.ValidateImageRef(config.Image.Source); err != nil {
		problems = append(problems, "image.source: "+err.Error())
	}
	if err := internal.ValidateVMName(config.VM.Name); err != nil {
		problems = append(problems, "vm.name: "+err.Error())
	}
	if config.VM.CPU < 1 || config.VM.CPU > internal.MaxCPUs {
		problems = append(problems, fmt.Sprintf("vm.cpu must be between 1 and %d", internal.MaxCPUs))
	}
	if config.VM.RAM < internal.MinMemoryMB {
		problems = append(problems, fmt.Sprintf("vm.ram must be at least %d MB", internal.MinMemoryMB))
	}
	if len(problems) > 0 {
		return errors.New(strings.Join(problems, "; "))
	}
	return nil
}

func findVmfile(sourcePath string) (string, bool, error) {
	info, err := os.Stat(sourcePath)
	if err != nil {
		return "", false, err
	}
	if info.IsDir() {
		path := filepath.Join(sourcePath, "Vmfile")
		if _, err := os.Stat(path); err == nil {
			return path, true, nil
		}
		return "", false, nil
	}
	if info.Name() == "Vmfile" {
		return sourcePath, true, nil
	}
	return "", false, nil
}

func executeRun(client proto.FvcServiceClient, args []string) error {
	fs := flag.NewFlagSet("run", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)

	flagImage := fs.String("image", "", "Name of the disk image to use")
	flagCPU := fs.Int("cpu", 0, "Number of CPU cores")
	flagRAM := fs.Int("ram", 0, "Amount of RAM in MB")

	if err := fs.Parse(args); err != nil {
		return err
	}

	finalImage := strings.TrimSpace(*flagImage)
	finalCPU := *flagCPU
	finalRAM := *flagRAM

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

	printStep("CONFIG", "Resolving configuration")
	printKV("image", finalImage)
	printKV("cpu", fmt.Sprintf("%d", finalCPU))
	printKV("ram", fmt.Sprintf("%d MB", finalRAM))

	stream, err := client.RunStream(context.Background(), &proto.RunRequest{
		Source: finalImage,
		Config: &proto.VmConfig{
			Cpus:     int32(finalCPU),
			MemoryMb: int32(finalRAM),
		},
	})
	if err != nil {
		return fmt.Errorf("daemon request failed: %w", err)
	}

	var vmID string
	progress := &progressLine{}
	defer progress.finish()
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
		printRunEvent(progress, event)
		if event.GetStatus() == "error" {
			return fmt.Errorf("microVM start failed: %s", event.GetErrorMessage())
		}
	}
	if vmID == "" {
		return errors.New("microVM start failed: daemon did not return a VM ID")
	}
	printSuccess(fmt.Sprintf("microVM started: %s", vmID))
	printKV("status", "running")
	return nil
}

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
	printSuccess(res.Message)
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
	printSuccess(res.Message)
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
	printSuccess(res.Message)
	return nil
}

func executePs(client proto.FvcServiceClient, args []string) error {
	fs := flag.NewFlagSet("ps", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	all := fs.Bool("all", false, "Show stopped VMs too")
	if err := fs.Parse(args); err != nil {
		return err
	}

	res, err := client.Ps(context.Background(), &proto.PsRequest{All: *all})
	if err != nil {
		return fmt.Errorf("daemon request failed: %w", err)
	}

	fmt.Printf("%s%-38s %-8s %-12s %-18s %-8s %-8s %-15s%s\n", color(colorBold+colorCyan), "VM ID", "PID", "STATUS", "IMAGE", "CPU", "RAM", "IP", color(colorReset))
	fmt.Println(color(colorGray) + "----------------------------------------------------------------------------------------------------------" + color(colorReset))

	for _, vm := range res.Vms {
		cpus := int32(0)
		memory := int32(0)
		if vm.Config != nil {
			cpus = vm.Config.Cpus
			memory = vm.Config.MemoryMb
		}
		fmt.Printf("%-38s %-8d %-12s %-18s %-8d %-8d %-15s\n", vm.VmId, vm.Pid, vm.Status, vm.Image, cpus, memory, vm.GuestIp)
	}
	return nil
}

func executeConsole(client proto.FvcServiceClient, args []string) error {
	fs := flag.NewFlagSet("console", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	if err := fs.Parse(args); err != nil {
		return err
	}
	if len(fs.Args()) < 1 {
		return errors.New("console requires a microVM ID. Example: fvc console <id>")
	}

	vmID := fs.Args()[0]
	info, err := client.ConsoleInfo(context.Background(), &proto.ConsoleInfoRequest{VmId: vmID})
	if err != nil {
		return fmt.Errorf("daemon request failed: %w", err)
	}
	if !info.Success {
		return fmt.Errorf("console unavailable: %s", info.Message)
	}

	printStep("CONSOLE", fmt.Sprintf("Attaching to %s", vmID))
	printKV("exit", "Ctrl-C")
	return attachConsole(info.LogPath, info.InputPath)
}

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

func printStep(label, message string) {
	fmt.Printf("%s %s\n", colorLabel(label, colorCyan), message)
}

func printSuccess(message string) {
	fmt.Printf("%s %s\n", colorLabel("OK", colorGreen), message)
}

func printFailure(message string) {
	fmt.Printf("%s %s\n", colorLabel("ERR", colorRed), message)
}

func printKV(key, value string) {
	fmt.Printf("  %s%-8s%s %s\n", color(colorDim), key+":", color(colorReset), value)
}

func printRunEvent(progress *progressLine, event *proto.RunEvent) {
	switch event.GetStatus() {
	case "complete":
		if event.GetStage() == "done" {
			progress.finish()
			return
		}
		progress.finish()
		printSuccess(event.GetMessage())
	case "error":
		progress.finish()
		printFailure(event.GetMessage())
	default:
		progress.start(strings.ToUpper(event.GetStage()), event.GetMessage())
	}
}

func (p *progressLine) start(label, message string) {
	p.finish()
	p.active = true
	p.label = label
	p.done = make(chan struct{})
	go func(done <-chan struct{}) {
		frames := []string{"-", "\\", "|", "/"}
		ticker := time.NewTicker(120 * time.Millisecond)
		defer ticker.Stop()
		i := 0
		for {
			select {
			case <-done:
				return
			case <-ticker.C:
				fmt.Printf("\r%s %s %s", colorLabel(label, colorCyan), message, color(colorGray)+frames[i%len(frames)]+color(colorReset))
				i++
			}
		}
	}(p.done)
	fmt.Printf("%s %s %s", colorLabel(label, colorCyan), message, color(colorGray)+"-"+color(colorReset))
}

func (p *progressLine) finish() {
	if !p.active {
		return
	}
	close(p.done)
	fmt.Print("\r\033[2K")
	p.active = false
}

func colorLabel(label, code string) string {
	return fmt.Sprintf("%s[%s]%s", color(code), label, color(colorReset))
}

func color(code string) string {
	if os.Getenv("NO_COLOR") != "" {
		return ""
	}
	return code
}

func usage() {
	fmt.Println("Usage: fvc <command> [arguments]")
	fmt.Println()
	fmt.Println("Commands:")
	fmt.Println("  run [path] [--image name] [--cpu n] [--ram mb]")
	fmt.Println("  ps [--all]")
	fmt.Println("  stop [--timeout seconds] <id>")
	fmt.Println("  start <id>")
	fmt.Println("  rm <id>")
	fmt.Println("  console <id>")
	fmt.Println("  logs [--tail n] [--follow] <id>")
}

func main() {
	commands := map[string]commandFunc{
		"run":     executeRun,
		"ps":      executePs,
		"stop":    executeStop,
		"start":   executeStart,
		"rm":      executeRm,
		"console": executeConsole,
		"logs":    executeLogs,
	}

	if len(os.Args) < 2 {
		usage()
		os.Exit(1)
	}
	cmdName := os.Args[1]
	cmdArgs := os.Args[2:]

	function, exists := commands[cmdName]
	if !exists {
		fmt.Fprintf(os.Stderr, "%s unknown command %q\n\n", colorLabel("ERR", colorRed), cmdName)
		usage()
		os.Exit(1)
	}

	client, conn, err := newClient()
	if err != nil {
		fmt.Fprintf(os.Stderr, "%s %v\n", colorLabel("ERR", colorRed), err)
		os.Exit(1)
	}
	defer conn.Close()

	if err := function(client, cmdArgs); err != nil {
		fmt.Fprintf(os.Stderr, "%s %v\n", colorLabel("ERR", colorRed), err)
		os.Exit(1)
	}
}

func envOrDefault(key, fallback string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return fallback
}
