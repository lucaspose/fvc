package main

import (
	"errors"
	"fmt"
	"io"
	"os"

	"github.com/lucaspose/fvc/internal/cliui"
	"github.com/lucaspose/fvc/proto"
)

type commandFunc func(client proto.FvcServiceClient, args []string) error

type cliExitError struct {
	code int
}

func (e cliExitError) Error() string {
	return fmt.Sprintf("command exited with status %d", e.code)
}

type clientConnector func() (proto.FvcServiceClient, func() error, error)

func usage(out io.Writer) {
	fmt.Fprintln(out, "Usage: fvc <command> [arguments]")
	fmt.Fprintln(out)
	fmt.Fprintln(out, "Commands:")
	fmt.Fprintln(out, "  run [image|path] [--from docker] [--pull missing|always|never] [--network nat|none] [--name name] [--cpu n] [--ram mb] [-p host:guest] [-v name:/path[:ro]] [--publish-all] [--rm]")
	fmt.Fprintln(out, "  build [-t image] [path]")
	fmt.Fprintln(out, "  doctor")
	fmt.Fprintln(out, "  pull [--from docker|docker://] [-t local-tag] <image>")
	fmt.Fprintln(out, "  images")
	fmt.Fprintln(out, "  image inspect|rm|tag|import|export|history|prune")
	fmt.Fprintln(out, "  volume create|ls|inspect|rm|prune")
	fmt.Fprintln(out, "  prune [--dry-run] [--force]")
	fmt.Fprintln(out, "  stats [--watch] [--interval duration] [id]")
	fmt.Fprintln(out, "  ps [--all]")
	fmt.Fprintln(out, "  stop [--timeout seconds] <id>")
	fmt.Fprintln(out, "  start <id>")
	fmt.Fprintln(out, "  restart [--timeout seconds] <id>")
	fmt.Fprintln(out, "  rename <id> <name>")
	fmt.Fprintln(out, "  update [--cpu n] [--ram mb] <id>")
	fmt.Fprintln(out, "  top <id>")
	fmt.Fprintln(out, "  cp <src> <dest>")
	fmt.Fprintln(out, "  wait [--timeout seconds] <id>")
	fmt.Fprintln(out, "  kill <id>")
	fmt.Fprintln(out, "  snapshot create <id> <name>")
	fmt.Fprintln(out, "  snapshot ls <id>")
	fmt.Fprintln(out, "  snapshot restore <id> <name>")
	fmt.Fprintln(out, "  snapshot rm <id> <name>")
	fmt.Fprintln(out, "  rm <id>")
	fmt.Fprintln(out, "  inspect <id>")
	fmt.Fprintln(out, "  console <id>")
	fmt.Fprintln(out, "  exec [-w dir] [-e KEY=VALUE] <id> <command> [args...]")
	fmt.Fprintln(out, "  logs [--tail n] [--follow] <id>")
}

func defaultCommands() map[string]commandFunc {
	return map[string]commandFunc{
		"run":      executeRun,
		"build":    executeBuild,
		"doctor":   executeDoctor,
		"pull":     executePull,
		"images":   executeImages,
		"image":    executeImage,
		"volume":   executeVolume,
		"prune":    executePrune,
		"stats":    executeStats,
		"ps":       executePs,
		"stop":     executeStop,
		"start":    executeStart,
		"restart":  executeRestart,
		"rename":   executeRename,
		"update":   executeUpdate,
		"top":      executeTop,
		"cp":       executeCp,
		"wait":     executeWait,
		"kill":     executeKill,
		"snapshot": executeSnapshot,
		"rm":       executeRm,
		"inspect":  executeInspect,
		"console":  executeConsole,
		"exec":     executeExec,
		"logs":     executeLogs,
	}
}

func defaultClientConnector() (proto.FvcServiceClient, func() error, error) {
	client, conn, err := newClient()
	if err != nil {
		return nil, nil, err
	}
	return client, conn.Close, nil
}

func runCLI(args []string, out, errOut io.Writer, commands map[string]commandFunc, connect clientConnector) int {
	if len(args) < 1 {
		usage(out)
		return 1
	}
	cmdName := args[0]
	cmdArgs := args[1:]

	function, exists := commands[cmdName]
	if !exists {
		fmt.Fprintf(errOut, "%s unknown command %q\n\n", cliui.ColorLabel("ERR", cliui.ColorRed), cmdName)
		usage(out)
		return 1
	}

	client, closeClient, err := connect()
	if err != nil {
		fmt.Fprintf(errOut, "%s %v\n", cliui.ColorLabel("ERR", cliui.ColorRed), err)
		return 1
	}
	if closeClient != nil {
		defer closeClient()
	}

	if err := function(client, cmdArgs); err != nil {
		var exitErr cliExitError
		if errors.As(err, &exitErr) {
			return exitErr.code
		}
		fmt.Fprintf(errOut, "%s %v\n", cliui.ColorLabel("ERR", cliui.ColorRed), err)
		return 1
	}
	return 0
}

func main() {
	os.Exit(runCLI(os.Args[1:], os.Stdout, os.Stderr, defaultCommands(), defaultClientConnector))
}
