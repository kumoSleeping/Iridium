package api

import "strings"

// Sender joins an app's ID and the login of it a message came from ("app/login"), as a
// transcript's sources give it; SplitSender takes them apart.
func Sender(terminal, login string) string {
	if login == "" {
		return terminal
	}
	return terminal + "/" + login
}

// SplitSender takes a Sender apart (and leaves out a device, WithDevice's).
func SplitSender(s string) (terminal, login string) {
	s, _, _ = strings.Cut(s, deviceSep)
	terminal, login, _ = strings.Cut(s, "/")
	return terminal, login
}

const deviceSep = "\x1f"

// WithDevice is sender with the device it was sent from (a browser of ir_web's: Device), as a
// transcript's sources give it; SenderDevice tells it.
func WithDevice(sender, device string) string {
	if device == "" {
		return sender
	}
	return sender + deviceSep + device
}

// SenderDevice is the device a sender (WithDevice) was sent from, "" if it does not say.
func SenderDevice(s string) string {
	_, d, _ := strings.Cut(s, deviceSep)
	return d
}
