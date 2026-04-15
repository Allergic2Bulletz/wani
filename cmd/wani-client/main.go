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
		fmt.Fprintln(os.Stderr, "usage: wani-client <command> [options]")
		fmt.Fprintln(os.Stderr, "commands:")
		fmt.Fprintln(os.Stderr, "  stun                    — discover public address")
		fmt.Fprintln(os.Stderr, "  send [options] <path>   — send file or directory")
		fmt.Fprintln(os.Stderr, "  receive [options] <code>— receive transfer")
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

	args := fs.Args()
	if len(args) < 1 {
		return fmt.Errorf("send: missing path\nusage: wani-client send [options] <path>")
	}
	path := args[0]

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

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
		fmt.Fprintln(os.Stderr, "Pairing failed.")
		return fmt.Errorf("send: SPAKE2: %w", err)
	}
	if err := sc.PingPong(id); err != nil {
		fmt.Fprintln(os.Stderr, "Pairing failed.")
		return fmt.Errorf("send: ping-pong: %w", err)
	}

	fmt.Println("Paired successfully!")

	// Build manifest before ICE (CPU-bound; keeps transfer latency low).
	fmt.Println("Scanning files...")
	manifest, err := client.BuildManifest(path)
	if err != nil {
		return fmt.Errorf("send: build manifest: %w", err)
	}
	totalFiles := len(manifest.Files)
	var totalBytes int64
	for _, f := range manifest.Files {
		totalBytes += f.Size
	}
	fmt.Printf("Sending %d file(s), %d bytes total\n", totalFiles, totalBytes)

	// ICE: gather and connect (controlling = sender).
	fmt.Println("Establishing P2P connection...")
	agent, err := client.NewICEAgent(nil)
	if err != nil {
		return fmt.Errorf("send: ICE agent: %w", err)
	}
	iceConn, err := client.GatherAndConnect(ctx, agent, sc, true)
	if err != nil {
		return fmt.Errorf("send: ICE connect: %w", err)
	}

	// QUIC over ICE.
	quicConn, err := client.DialQUIC(ctx, iceConn, id.SharedSecret())
	if err != nil {
		return fmt.Errorf("send: QUIC dial: %w", err)
	}
	defer quicConn.CloseWithError(0, "done")

	// Manifest exchange.
	resp, err := client.SendManifest(ctx, quicConn, manifest)
	if err != nil {
		return fmt.Errorf("send: manifest: %w", err)
	}
	if resp.Status != "ready" {
		return fmt.Errorf("send: receiver not ready: %s", resp.Status)
	}

	// File transfer.
	progress := func(filePath string, written, total int64) {
		if total > 0 {
			pct := written * 100 / total
			fmt.Printf("\r  %s  %d/%d bytes (%d%%)   ", filePath, written, total, pct)
		}
	}
	if err := client.SendFiles(ctx, quicConn, manifest, path, progress); err != nil {
		fmt.Println()
		return fmt.Errorf("send: transfer: %w", err)
	}
	fmt.Printf("\nTransfer complete: %d file(s)\n", totalFiles)
	return nil
}

func runReceive() error {
	fs := flag.NewFlagSet("receive", flag.ContinueOnError)
	serverURL := fs.String("server", "ws://localhost:8080/ws", "signaling server WebSocket URL")
	outDir := fs.String("out", ".", "destination directory")
	if err := fs.Parse(os.Args[2:]); err != nil {
		return err
	}

	args := fs.Args()
	if len(args) < 1 {
		return fmt.Errorf("receive: missing pairing code\nusage: wani-client receive [options] <code>")
	}
	code := args[0]

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

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
		fmt.Fprintln(os.Stderr, "Pairing failed.")
		return fmt.Errorf("receive: SPAKE2: %w", err)
	}
	if err := sc.WaitAndPong(id); err != nil {
		fmt.Fprintln(os.Stderr, "Pairing failed.")
		return fmt.Errorf("receive: ping-pong: %w", err)
	}

	fmt.Println("Paired successfully!")

	// ICE: gather and connect (controlled = receiver).
	fmt.Println("Establishing P2P connection...")
	agent, err := client.NewICEAgent(nil)
	if err != nil {
		return fmt.Errorf("receive: ICE agent: %w", err)
	}
	iceConn, err := client.GatherAndConnect(ctx, agent, sc, false)
	if err != nil {
		return fmt.Errorf("receive: ICE connect: %w", err)
	}

	// QUIC over ICE.
	quicConn, err := client.ListenQUIC(ctx, iceConn, id.SharedSecret())
	if err != nil {
		return fmt.Errorf("receive: QUIC listen: %w", err)
	}
	defer quicConn.CloseWithError(0, "done")

	// Manifest exchange + file receive.
	manifest, err := client.ReceiveManifest(ctx, quicConn, *outDir)
	if err != nil {
		return fmt.Errorf("receive: manifest: %w", err)
	}
	fmt.Printf("Incoming: %d file(s)\n", len(manifest.Files))

	progress := func(filePath string, written, total int64) {
		if total > 0 {
			pct := written * 100 / total
			fmt.Printf("\r  %s  %d/%d bytes (%d%%)   ", filePath, written, total, pct)
		}
	}
	if err := client.ReceiveFiles(ctx, quicConn, manifest, *outDir, progress); err != nil {
		fmt.Println()
		return fmt.Errorf("receive: transfer: %w", err)
	}
	fmt.Printf("\nReceived %d file(s) → %s\n", len(manifest.Files), *outDir)
	return nil
}
