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

	fmt.Printf("%s%-16s %-38s %-12s %-8s %-16s %-7s %-8s %-15s %-18s%s\n", cliui.Color(cliui.ColorBold+cliui.ColorCyan), "NAME", "VM ID", "STATUS", "EXIT", "IMAGE", "CPU", "RAM", "IP", "PORTS", cliui.Color(cliui.ColorReset))
	fmt.Println(cliui.Color(cliui.ColorGray) + "--------------------------------------------------------------------------------------------------------------------------------------------" + cliui.Color(cliui.ColorReset))

	for _, vm := range res.Vms {
		cpus := int32(0)
		memory := int32(0)
		if vm.Config != nil {
			cpus = vm.Config.Cpus
			memory = vm.Config.MemoryMb
		}
		ports := ""
		if vm.Config != nil {
			ports = strings.Join(vm.Config.Ports, ",")
		}
		fmt.Printf("%-16s %-38s %-12s %-8s %-16s %-7d %-8d %-15s %-18s\n", vm.Name, vm.VmId, vm.Status, cliui.FormatExitCode(vm.ExitCode), vm.Image, cpus, memory, vm.GuestIp, ports)
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
		cliui.PrintKV("ports", strings.Join(vm.GetConfig().GetPorts(), ", "))
	}
	cliui.PrintKV("ip", vm.GetGuestIp())
	cliui.PrintKV("tap", vm.GetTapName())
	cliui.PrintKV("mac", vm.GetMacAddress())
	cliui.PrintKV("log", vm.GetLogPath())
	cliui.PrintKV("drive", vm.GetDrivePath())
	cliui.PrintKV("console", vm.GetConsolePath())
}
