//go:build !linux && !darwin

package platform

// Supervise runs Detached units' supervisors, which only Linux and macOS have.
func Supervise(args []string) (code int, ok bool) { return 0, false }
