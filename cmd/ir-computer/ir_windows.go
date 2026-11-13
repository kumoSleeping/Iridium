package main

import (
	"fmt"
	"os"
)

// irMain: the CLI is not built for Windows.
func irMain([]string) int {
	fmt.Fprintln(os.Stderr, "ir: not on Windows")
	return 2
}
