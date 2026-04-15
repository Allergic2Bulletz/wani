package main

import (
	"fmt"
	"os"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintf(os.Stderr, "wani-client: %v\n", err)
		os.Exit(1)
	}
}

func run() error {
	fmt.Println("wani-client starting...")
	return nil
}
