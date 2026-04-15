#!/usr/bin/env bash
set -euo pipefail

mkdir -p build

# Inject default server URL from config.json if jq is available and IP is configured.
CLIENT_LDFLAGS=""
if command -v jq &>/dev/null; then
  SERVER_IP=$(jq -r '."wani-server-ip"' config.json 2>/dev/null || true)
  if [[ -n "$SERVER_IP" && "$SERVER_IP" != "IP_HERE" && "$SERVER_IP" != "null" ]]; then
    CLIENT_LDFLAGS="-X 'main.defaultServerURL=ws://${SERVER_IP}:8080/ws'"
    echo "Default server: ws://${SERVER_IP}:8080/ws"
  else
    echo "Warning: config.json wani-server-ip not set — client will default to localhost"
  fi
else
  echo "Warning: jq not found — skipping server URL injection, client will default to localhost"
fi

GOOS=linux GOARCH=amd64 go build -o build/wani-server ./cmd/wani-server
GOOS=linux GOARCH=amd64 go build -ldflags "$CLIENT_LDFLAGS" -o build/wani-client ./cmd/wani-client
GOOS=windows GOARCH=amd64 go build -ldflags "$CLIENT_LDFLAGS" -o build/wani-client.exe ./cmd/wani-client
echo "Build complete: build/wani-server, build/wani-client, build/wani-client.exe"
