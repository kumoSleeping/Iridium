//go:build !linux && !darwin && !windows

package machineid

func read() string { return "" }
