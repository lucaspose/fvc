//go:build !linux

package main

import "os"

func configureConsoleTerminal(file *os.File) (func(), error) {
	return func() {}, nil
}

func requireInteractiveTerminal(file *os.File) error {
	return nil
}
