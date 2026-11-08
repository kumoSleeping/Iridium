//go:build !darwin

package discover

// defaultProcs is the system's processes: /proc.
func defaultProcs() Procs { return ProcFS{} }
