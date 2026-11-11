package dsh

import (
	"fmt"
	"os"
)

// ownFile: path is a regular file (Windows: the user's profile keeps others out).
func ownFile(path string) error {
	st, err := os.Lstat(path)
	if err != nil {
		return fmt.Errorf("its patch: %w", err)
	}
	if !st.Mode().IsRegular() {
		return fmt.Errorf("its patch %s is not a file", path)
	}
	return nil
}
