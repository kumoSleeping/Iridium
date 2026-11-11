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

// SplitSender takes a Sender apart.
func SplitSender(s string) (terminal, login string) {
	terminal, login, _ = strings.Cut(s, "/")
	return terminal, login
}
