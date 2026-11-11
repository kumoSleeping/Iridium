//go:build !windows

package dsh

import (
	"fmt"
	"os"
	"syscall"
)

// ownFile: path is a file of this user's that no one else may write (a patch an executor on this
// computer gave: one at another machine cannot have made it).
func ownFile(path string) error {
	st, err := os.Lstat(path)
	if err != nil {
		return fmt.Errorf("its patch: %w", err)
	}
	sys, ok := st.Sys().(*syscall.Stat_t)
	if !st.Mode().IsRegular() || st.Mode().Perm()&0o022 != 0 || !ok || int(sys.Uid) != os.Getuid() {
		return fmt.Errorf("its patch %s is not this user's own file", path)
	}
	return nil
}
