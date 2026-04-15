package main

import (
	"flag"
	"fmt"
	"os"

	"github.com/Allergic2Bulletz/wani/internal/server"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintf(os.Stderr, "wani-server: %v\n", err)
		os.Exit(1)
	}
}

func run() error {
	var addr string
	flag.StringVar(&addr, "addr", ":8080", "address to listen on")
	flag.Parse()

	s := server.New(addr)
	return s.ListenAndServe()
}
