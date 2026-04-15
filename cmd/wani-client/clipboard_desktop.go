//go:build windows || darwin

package main

import (
	"fmt"

	"golang.design/x/clipboard"
)

// tryWriteClipboard copies text to the system clipboard and prints a status line.
func tryWriteClipboard(text string) {
	if err := clipboard.Init(); err == nil {
		clipboard.Write(clipboard.FmtText, []byte(text))
		fmt.Println("(copied to clipboard)")
	} else {
		fmt.Println("(clipboard unavailable)")
	}
}
