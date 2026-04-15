#!/usr/bin/env bash
set -euo pipefail

mkdir -p build
GOOS=linux GOARCH=amd64 go build -o build/wani-server ./cmd/wani-server
GOOS=linux GOARCH=amd64 go build -o build/wani-client ./cmd/wani-client
GOOS=windows GOARCH=amd64 go build -o build/wani-client.exe ./cmd/wani-client
echo "Build complete: build/wani-server, build/wani-client, build/wani-client.exe"
