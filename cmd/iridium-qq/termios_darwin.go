package main

import "golang.org/x/sys/unix"

// The ioctl that gets a terminal's attributes.
const ioctlGetTermios = unix.TIOCGETA
