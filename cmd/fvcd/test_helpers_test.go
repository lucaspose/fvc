package main

import (
	"fmt"
	"strings"
)

type fakeRunner struct {
	calls []string
	fail  map[string]bool
}

func (f *fakeRunner) Run(name string, args ...string) error {
	call := name + " " + strings.Join(args, " ")
	f.calls = append(f.calls, call)
	if f.fail != nil && f.fail[call] {
		return fmt.Errorf("forced failure")
	}
	return nil
}
