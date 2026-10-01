package main

import (
	"fmt"
	"io"
)

// version is injected at build time via -ldflags "-X main.version=...".
var version = "dev"

// runVersion prints the build version.
func runVersion(stdout io.Writer) int {
	_, _ = fmt.Fprintf(stdout, "yz %s\n", version)
	return 0
}
