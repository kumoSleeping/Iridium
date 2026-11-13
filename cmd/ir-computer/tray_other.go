//go:build !windows && !darwin

package main

import (
	"context"
	"errors"

	"github.com/kumoSleeping/iridium/internal/join"
)

// tray: none on Linux (a systemd user unit, mostly on a machine no one sits at).
func tray(context.Context, join.Tray) error { return errors.New("not on this system") }
