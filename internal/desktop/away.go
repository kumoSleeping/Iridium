package desktop

// openIn shows address, at link ("": its page as it is, its first page if it has none yet): in
// its window of its own if it has one, else in the main window (mainwin.go).
func (u *ui) openIn(address, link string) {
	go u.showServer(address, link)
}

// gone: address left the list, or was logged out of: its window of its own closes, and its page
// leaves the main window.
func (u *ui) gone(address string) {
	if w := u.awayWindow(address); w != nil {
		w.Close()
	}
	u.leave(address)
}
