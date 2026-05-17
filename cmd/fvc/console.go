package main

import (
	"bufio"
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"syscall"
	"time"
)

func attachConsole(logPath, inputPath string) error {
	if err := printConsoleTail(logPath, os.Stdout, 200); err != nil {
		return err
	}
	fmt.Fprintln(os.Stdout)
	printStep("CONSOLE", "Press Enter if the login prompt is not visible")

	input, err := openConsoleWriter(inputPath)
	if err != nil {
		if errors.Is(err, os.ErrPermission) {
			return fmt.Errorf("console input open failed: %w; restart fvcd with FVC_RUNTIME_GROUP=fvc, recreate or restart this VM, and make sure your shell session belongs to that group", err)
		}
		return fmt.Errorf("console input open failed: %w", err)
	}
	defer input.Close()

	restore, err := configureConsoleTerminal(os.Stdin)
	if err != nil {
		return err
	}
	defer restore()

	errCh := make(chan error, 2)
	go func() {
		errCh <- followConsoleLog(logPath, os.Stdout)
	}()
	go func() {
		_, err := io.Copy(input, os.Stdin)
		errCh <- err
	}()

	err = <-errCh
	if err == io.EOF {
		return nil
	}
	return err
}

func openConsoleWriter(path string) (*os.File, error) {
	fd, err := syscall.Open(path, syscall.O_WRONLY|syscall.O_NONBLOCK, 0600)
	if err != nil {
		if errors.Is(err, syscall.ENXIO) {
			return nil, fmt.Errorf("no console reader is attached; restart this VM with the current fvcd, or stop/start it so Firecracker is launched with console support")
		}
		return nil, err
	}
	return os.NewFile(uintptr(fd), path), nil
}

func printConsoleTail(path string, out io.Writer, tail int) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("console log read failed: %w", err)
	}
	lines := splitConsoleLines(data)
	if tail > 0 && len(lines) > tail {
		lines = lines[len(lines)-tail:]
	}
	for _, line := range lines {
		if _, err := out.Write(append(line, '\n')); err != nil {
			return err
		}
	}
	return nil
}

func splitConsoleLines(data []byte) [][]byte {
	scanner := bufio.NewScanner(bytes.NewReader(data))
	lines := make([][]byte, 0)
	for scanner.Scan() {
		line := append([]byte(nil), scanner.Bytes()...)
		lines = append(lines, line)
	}
	return lines
}

func followConsoleLog(path string, out io.Writer) error {
	file, err := os.Open(path)
	if err != nil {
		return fmt.Errorf("console log open failed: %w", err)
	}
	defer file.Close()

	if _, err := file.Seek(0, io.SeekEnd); err != nil {
		return fmt.Errorf("console log seek failed: %w", err)
	}

	reader := bufio.NewReader(file)
	for {
		line, err := reader.ReadString('\n')
		if line != "" {
			if _, writeErr := io.WriteString(out, line); writeErr != nil {
				return writeErr
			}
		}
		if err == io.EOF {
			time.Sleep(100 * time.Millisecond)
			continue
		}
		if err != nil {
			return fmt.Errorf("console log read failed: %w", err)
		}
	}
}
