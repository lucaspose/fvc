package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/BurntSushi/toml"
	"github.com/lucaspose/fvc/internal"
	"github.com/lucaspose/fvc/proto"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

type commandFunc func(client proto.FvcServiceClient, args []string) error

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

	fmt.Printf("Config: image=%s cpu=%d ram=%dMB\n", finalImage, finalCPU, finalRAM)
	res, err := client.Run(context.Background(), &proto.RunRequest{
		Source: finalImage,
		Config: &proto.VmConfig{
			Cpus:     int32(finalCPU),
			MemoryMb: int32(finalRAM),
		},
	})
	if err != nil {
		return fmt.Errorf("failed to run microVM via daemon: %w", err)
	}
	if res.Status == "failed" {
		return fmt.Errorf("daemon failed to start the microVM: %s", res.ErrorMessage)
	}
	fmt.Printf("MicroVM started successfully. ID: %s (status: %s)\n", res.VmId, res.Status)
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
		return errors.New("stop requires a microVM ID. Example: fvc stop <id>")
	}

	res, err := client.Stop(context.Background(), &proto.StopRequest{
		VmId:           fs.Args()[0],
		TimeoutSeconds: int32(*timeout),
	})
	if err != nil {
		return fmt.Errorf("failed to communicate with daemon: %w", err)
	}
	if !res.Success {
		return fmt.Errorf("cannot stop VM: %s", res.Message)
	}
	fmt.Printf("Success: %s\n", res.Message)
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
		return fmt.Errorf("cannot retrieve microVM list: %w", err)
	}
	if len(res.Vms) == 0 {
		fmt.Println("No microVMs found.")
		return nil
	}

	fmt.Printf("%-38s %-8s %-12s %-20s %-8s %-8s\n", "VM ID", "PID", "STATUS", "IMAGE", "CPU", "RAM")
	fmt.Println("------------------------------------------------------------------------------------------------")

	for _, vm := range res.Vms {
		cpus := int32(0)
		memory := int32(0)
		if vm.Config != nil {
			cpus = vm.Config.Cpus
			memory = vm.Config.MemoryMb
		}
		fmt.Printf("%-38s %-8d %-12s %-20s %-8d %-8d\n", vm.VmId, vm.Pid, vm.Status, vm.Image, cpus, memory)
	}
	return nil
}

func usage() {
	fmt.Println("Usage: fvc <command> [arguments]")
	fmt.Println()
	fmt.Println("Commands:")
	fmt.Println("  run [path] [--image name] [--cpu n] [--ram mb]")
	fmt.Println("  ps [--all]")
	fmt.Println("  stop <id> [--timeout seconds]")
}

func main() {
	commands := map[string]commandFunc{
		"run":  executeRun,
		"ps":   executePs,
		"stop": executeStop,
	}

	if len(os.Args) < 2 {
		usage()
		os.Exit(1)
	}
	cmdName := os.Args[1]
	cmdArgs := os.Args[2:]

	function, exists := commands[cmdName]
	if !exists {
		fmt.Fprintf(os.Stderr, "Unknown command: %s\n\n", cmdName)
		usage()
		os.Exit(1)
	}

	client, conn, err := newClient()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
	defer conn.Close()

	if err := function(client, cmdArgs); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
}

func envOrDefault(key, fallback string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return fallback
}
