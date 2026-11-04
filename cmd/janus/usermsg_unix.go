//go:build !windows

package main

import (
	"bufio"
	"fmt"
	"os"
)

func userAlert(title, message string) {
	fmt.Fprintf(os.Stderr, "%s: %s\n", title, message)
}

func userAlertAndWait(title, message string) {
	userAlert(title, message)
	fmt.Fprintln(os.Stderr, "Press Enter to close...")
	_, _ = bufio.NewReader(os.Stdin).ReadBytes('\n')
}
