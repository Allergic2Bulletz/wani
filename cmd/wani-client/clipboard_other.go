//go:build !windows && !darwin

package main

// tryWriteClipboard is a no-op on platforms without desktop clipboard support.
func tryWriteClipboard(_ string) {}
