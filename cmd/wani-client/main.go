package main

import (
	"context"
	"fmt"
	"os"
	"time"

	"github.com/Allergic2Bulletz/wani/internal/client"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintf(os.Stderr, "wani-client: %v\n", err)
		os.Exit(1)
	}
}

func run() error {
	if len(os.Args) < 2 {
		fmt.Println("usage: wani-client <command>")
		fmt.Println("commands: stun")
		return nil
	}

	switch os.Args[1] {
	case "stun":
		return runSTUN()
	default:
		return fmt.Errorf("unknown command: %s", os.Args[1])
	}
}

func runSTUN() error {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	addr, err := client.DiscoverPublicAddr(ctx)
	if err != nil {
		return err
	}
	fmt.Printf("Public address: %s\n", addr.String())
	return nil
}
