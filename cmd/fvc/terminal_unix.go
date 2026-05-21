//go:build linux

package main

import (
	"fmt"
	"os"
	"syscall"
	"unsafe"
)

const (
	ioctlTCGETS = 0x5401
	ioctlTCSETS = 0x5402
)

func configureConsoleTerminal(file *os.File) (func(), error) {
	if err := requireInteractiveTerminal(file); err != nil {
		return func() {}, err
	}
	fd := file.Fd()
	var original syscall.Termios
	if err := ioctlTermios(fd, ioctlTCGETS, &original); err != nil {
		return func() {}, fmt.Errorf("terminal read failed: %w", err)
	}

	raw := original
	raw.Lflag &^= syscall.ECHO | syscall.ICANON
	raw.Iflag &^= syscall.ICRNL | syscall.IXON
	raw.Cc[syscall.VMIN] = 1
	raw.Cc[syscall.VTIME] = 0

	if err := ioctlTermios(fd, ioctlTCSETS, &raw); err != nil {
		return func() {}, fmt.Errorf("terminal configure failed: %w", err)
	}

	return func() {
		_ = ioctlTermios(fd, ioctlTCSETS, &original)
	}, nil
}

func requireInteractiveTerminal(file *os.File) error {
	info, err := file.Stat()
	if err != nil {
		return fmt.Errorf("terminal stat failed: %w", err)
	}
	if info.Mode()&os.ModeCharDevice == 0 {
		return fmt.Errorf("console requires an interactive TTY; when using Docker, run: docker exec -it fvcd /usr/bin/fvc console <vm>")
	}
	fd := file.Fd()
	var termios syscall.Termios
	if err := ioctlTermios(fd, ioctlTCGETS, &termios); err != nil {
		return fmt.Errorf("console requires an interactive TTY; when using Docker, run: docker exec -it fvcd /usr/bin/fvc console <vm>: %w", err)
	}
	return nil
}

func ioctlTermios(fd uintptr, request uintptr, termios *syscall.Termios) error {
	_, _, errno := syscall.Syscall(syscall.SYS_IOCTL, fd, request, uintptr(unsafe.Pointer(termios)))
	if errno != 0 {
		return errno
	}
	return nil
}
