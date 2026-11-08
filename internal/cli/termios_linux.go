package cli

import "golang.org/x/sys/unix"

// The ioctls that get and set a terminal's attributes.
const (
	ioctlGetTermios = unix.TCGETS
	ioctlSetTermios = unix.TCSETS
)
