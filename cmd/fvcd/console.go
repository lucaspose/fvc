package main

import (
	"fmt"
	"os"
	"syscall"
)

func openConsoleInput(path string, runtimeGroup string) (*os.File, error) {
	if err := syscall.Mkfifo(path, 0600); err != nil && !os.IsExist(err) {
		return nil, fmt.Errorf("fifo create failed: %v", err)
	}
	if err := applyRuntimePermissions(path, runtimeGroup, 0660); err != nil {
		return nil, err
	}
	file, err := os.OpenFile(path, os.O_RDWR, 0600)
	if err != nil {
		return nil, fmt.Errorf("fifo open failed: %v", err)
	}
	return file, nil
}
