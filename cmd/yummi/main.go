// Package main provides the yummi CLI entrypoint.
package main

import (
	"os"

	"github.com/n0connect/yummi/internal/cli"
)

func main() {
	os.Exit(cli.Run(os.Args[1:], os.Stdout, os.Stderr))
}
