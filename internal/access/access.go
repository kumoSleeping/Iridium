// Package access is what the interactive terminals may do (plan "交互终端"): everything, every
// terminal alike (the user, 2026-11-08), Claude as any other agent; what an app or login may use
// is its Perms (agents and folders, internal/server/perms.go).
package access

import "errors"

// Denied is what a check refuses; the server answers 403.
type Denied struct{ Reason string }

func (d *Denied) Error() string { return d.Reason }

// IsDenied reports whether err is (or wraps) a Denied.
func IsDenied(err error) bool {
	var d *Denied
	return errors.As(err, &d)
}

// ErrManage refuses managing to one that may not.
var ErrManage = &Denied{"only ir_web's logins and the host manage this"}
