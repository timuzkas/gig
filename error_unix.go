//go:build !windows
// +build !windows

package main

import (
	"fmt"
	"os"
)

func showErrorBox(title, message string) {
	fmt.Fprintf(os.Stderr, "%s: %s\n", title, message)
}
