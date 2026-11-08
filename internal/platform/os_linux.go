package platform

import (
	"os"
	"strconv"
)

// allPIDs lists the processes, from /proc.
func allPIDs() []int {
	ents, _ := os.ReadDir("/proc")
	out := make([]int, 0, len(ents))
	for _, e := range ents {
		if n, err := strconv.Atoi(e.Name()); err == nil && n > 0 {
			out = append(out, n)
		}
	}
	return out
}
