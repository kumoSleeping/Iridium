package platform

import "golang.org/x/sys/unix"

// allPIDs lists the processes, from sysctl kern.proc.all.
func allPIDs() []int {
	procs, _ := unix.SysctlKinfoProcSlice("kern.proc.all")
	out := make([]int, 0, len(procs))
	for _, p := range procs {
		if p.Proc.P_pid > 0 {
			out = append(out, int(p.Proc.P_pid))
		}
	}
	return out
}
