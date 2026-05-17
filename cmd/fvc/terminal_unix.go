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
	info, err := file.Stat()
	if err != nil {
		return func() {}, fmt.Errorf("terminal stat failed: %w", err)
	}
	if info.Mode()&os.ModeCharDevice == 0 {
		return func() {}, nil
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

func ioctlTermios(fd uintptr, request uintptr, termios *syscall.Termios) error {
	_, _, errno := syscall.Syscall(syscall.SYS_IOCTL, fd, request, uintptr(unsafe.Pointer(termios)))
	if errno != 0 {
		return errno
	}
	return nil
}
