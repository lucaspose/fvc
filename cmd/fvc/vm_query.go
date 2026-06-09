package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/lucaspose/fvc/internal/cliui"
	"github.com/lucaspose/fvc/proto"
)

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

	fmt.Printf("%s%-16s %-38s %-12s %-8s %-16s %-7s %-8s %-8s %-15s %-18s%s\n", cliui.Color(cliui.ColorBold+cliui.ColorCyan), "NAME", "VM ID", "STATUS", "EXIT", "IMAGE", "CPU", "RAM", "NETWORK", "IP", "PORTS", cliui.Color(cliui.ColorReset))
	fmt.Println(cliui.Color(cliui.ColorGray) + "------------------------------------------------------------------------------------------------------------------------------------------------------" + cliui.Color(cliui.ColorReset))

	for _, vm := range res.Vms {
		cpus := int32(0)
		memory := int32(0)
		if vm.Config != nil {
			cpus = vm.Config.Cpus
			memory = vm.Config.MemoryMb
		}
		ports := ""
		network := ""
		if vm.Config != nil {
			ports = strings.Join(vm.Config.Ports, ",")
			network = vm.Config.NetworkMode
		}
		fmt.Printf("%-16s %-38s %-12s %-8s %-16s %-7d %-8d %-8s %-15s %-18s\n", vm.Name, vm.VmId, vm.Status, cliui.FormatExitCode(vm.ExitCode), vm.Image, cpus, memory, network, vm.GuestIp, ports)
	}
	return nil
}

func executeInspect(client proto.FvcServiceClient, args []string) error {
	fs := flag.NewFlagSet("inspect", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	if err := fs.Parse(args); err != nil {
		return err
	}
	if len(fs.Args()) < 1 {
		return errors.New("inspect requires a microVM ID. Example: fvc inspect <id>")
	}

	res, err := client.Inspect(context.Background(), &proto.InspectRequest{VmId: fs.Args()[0]})
	if err != nil {
		return fmt.Errorf("daemon request failed: %w", err)
	}
	if !res.Success {
		return fmt.Errorf("inspect failed: %s", res.Message)
	}
	printVMInspect(res.Vm)
	return nil
}

func executeStats(client proto.FvcServiceClient, args []string) error {
	fs := flag.NewFlagSet("stats", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	watch := fs.Bool("watch", false, "Refresh stats continuously")
	interval := fs.Duration("interval", 2*time.Second, "Refresh interval when --watch is enabled")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *interval <= 0 {
		return errors.New("interval must be greater than zero")
	}
	vmID := ""
	if len(fs.Args()) > 0 {
		vmID = fs.Args()[0]
	}

	for {
		res, err := client.Stats(context.Background(), &proto.StatsRequest{VmId: vmID})
		if err != nil {
			return fmt.Errorf("daemon request failed: %w", err)
		}
		if *watch {
			fmt.Print("\033[H\033[2J")
		}
		printStatsTable(res.Stats)
		if !*watch {
			return nil
		}
		time.Sleep(*interval)
	}
}

func printStatsTable(stats []*proto.VmStats) {
	fmt.Printf("%s%-38s %-8s %-12s %-8s %-22s %-10s%s\n", cliui.Color(cliui.ColorBold+cliui.ColorCyan), "VM ID", "PID", "STATUS", "CPU %", "MEM USAGE / LIMIT", "UPTIME", cliui.Color(cliui.ColorReset))
	fmt.Println(cliui.Color(cliui.ColorGray) + "--------------------------------------------------------------------------------------------------------------" + cliui.Color(cliui.ColorReset))
	for _, stat := range stats {
		fmt.Printf("%-38s %-8d %-12s %-8.1f %-22s %-10s\n",
			stat.VmId,
			stat.Pid,
			stat.Status,
			stat.CpuPercent,
			cliui.FormatMemoryUsage(stat.RssBytes, stat.MemoryMb),
			cliui.FormatDuration(stat.UptimeSeconds),
		)
	}
}

func printVMInspect(vm *proto.VmDetails) {
	cliui.PrintStep("INSPECT", vm.GetVmId())
	cliui.PrintKV("name", vm.GetName())
	cliui.PrintKV("status", vm.GetStatus())
	if vm.GetExitCode() >= 0 {
		cliui.PrintKV("exit", fmt.Sprintf("%d", vm.GetExitCode()))
	}
	cliui.PrintKV("pid", fmt.Sprintf("%d", vm.GetPid()))
	cliui.PrintKV("image", vm.GetImage())
	if vm.GetConfig() != nil {
		cliui.PrintKV("cpu", fmt.Sprintf("%d", vm.GetConfig().GetCpus()))
		cliui.PrintKV("ram", fmt.Sprintf("%d MB", vm.GetConfig().GetMemoryMb()))
		cliui.PrintKV("network", vm.GetConfig().GetNetworkMode())
		cliui.PrintKV("ports", strings.Join(vm.GetConfig().GetPorts(), ", "))
		cliui.PrintKV("volumes", strings.Join(vm.GetConfig().GetVolumes(), ", "))
	}
	cliui.PrintKV("ip", vm.GetGuestIp())
	cliui.PrintKV("tap", vm.GetTapName())
	cliui.PrintKV("mac", vm.GetMacAddress())
	cliui.PrintKV("log", vm.GetLogPath())
	cliui.PrintKV("drive", vm.GetDrivePath())
	cliui.PrintKV("console", vm.GetConsolePath())
}
