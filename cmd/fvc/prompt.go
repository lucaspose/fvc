package main

import (
	"bufio"
	"fmt"
	"os"
	"strings"
)

func confirm(prompt string) (bool, error) {
	fmt.Fprint(os.Stderr, prompt)
	line, err := bufio.NewReader(os.Stdin).ReadString('\n')
	if err != nil {
		return false, err
	}
	answer := strings.ToLower(strings.TrimSpace(line))
	return answer == "y" || answer == "yes" || answer == "o" || answer == "oui", nil
}
