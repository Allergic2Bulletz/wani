package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"time"

	"github.com/Allergic2Bulletz/wani/internal/client"
	"github.com/Allergic2Bulletz/wani/internal/identity"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintf(os.Stderr, "wani-client: %v\n", err)
		os.Exit(1)
	}
}

func run() error {
	if len(os.Args) < 2 {
		fmt.Println("usage: wani-client <command> [options]")
		fmt.Println("commands: stun, send, receive <code>")
		return nil
	}

	switch os.Args[1] {
	case "stun":
		return runSTUN()
	case "send":
		return runSend()
	case "receive":
		return runReceive()
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

func runSend() error {
	fs := flag.NewFlagSet("send", flag.ContinueOnError)
	serverURL := fs.String("server", "ws://localhost:8080/ws", "signaling server WebSocket URL")
	if err := fs.Parse(os.Args[2:]); err != nil {
		return err
	}

	ctx := context.Background()
	sc, err := client.Connect(ctx, *serverURL)
	if err != nil {
		return fmt.Errorf("send: connect: %w", err)
	}
	defer sc.Close()

	code, err := sc.CreateSession()
	if err != nil {
		return fmt.Errorf("send: create session: %w", err)
	}
	fmt.Printf("Pairing code: %s\n", code)
	fmt.Println("Waiting for receiver...")

	if err := sc.WaitForPeer(); err != nil {
		return fmt.Errorf("send: wait for peer: %w", err)
	}

	id, err := identity.NewEphemeralIdentity(identity.RoleSender, []byte(code))
	if err != nil {
		return fmt.Errorf("send: identity: %w", err)
	}
	if err := sc.ExchangeSPAKE2(id); err != nil {
		fmt.Println("Pairing failed.")
		return fmt.Errorf("send: SPAKE2: %w", err)
	}
	if err := sc.PingPong(id); err != nil {
		fmt.Println("Pairing failed.")
		return fmt.Errorf("send: ping-pong: %w", err)
	}

	fmt.Println("Paired successfully!")
	return nil
}

func runReceive() error {
	fs := flag.NewFlagSet("receive", flag.ContinueOnError)
	serverURL := fs.String("server", "ws://localhost:8080/ws", "signaling server WebSocket URL")
	if err := fs.Parse(os.Args[2:]); err != nil {
		return err
	}

	args := fs.Args()
	if len(args) < 1 {
		return fmt.Errorf("receive: missing pairing code\nusage: wani-client receive <code>")
	}
	code := args[0]

	ctx := context.Background()
	sc, err := client.Connect(ctx, *serverURL)
	if err != nil {
		return fmt.Errorf("receive: connect: %w", err)
	}
	defer sc.Close()

	if err := sc.JoinSession(code); err != nil {
		return fmt.Errorf("receive: join session: %w", err)
	}

	id, err := identity.NewEphemeralIdentity(identity.RoleReceiver, []byte(code))
	if err != nil {
		return fmt.Errorf("receive: identity: %w", err)
	}
	if err := sc.ExchangeSPAKE2(id); err != nil {
		fmt.Println("Pairing failed.")
		return fmt.Errorf("receive: SPAKE2: %w", err)
	}
	if err := sc.WaitAndPong(id); err != nil {
		fmt.Println("Pairing failed.")
		return fmt.Errorf("receive: ping-pong: %w", err)
	}

	fmt.Println("Paired successfully!")
	return nil
}
