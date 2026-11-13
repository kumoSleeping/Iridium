//go:build !windows

package main

import "github.com/kumoSleeping/iridium/internal/cli"

// irMain is ir for the agents it starts (executor.Options.IR): the CLI.
func irMain(args []string) int { return cli.Main(args) }
