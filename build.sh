#!/bin/sh
# Build Log Viewer cho macOS (arm64 + amd64) và Windows (amd64)
# Cần: go >= 1.22  (brew install go)
set -e
cd "$(dirname "$0")"

export PATH="/opt/homebrew/bin:$PATH:/usr/local/go/bin:$PATH"
mkdir -p dist

echo "==> Build macOS (Apple Silicon)..."
GOOS=darwin GOARCH=arm64 go build -trimpath -ldflags="-s -w" -o dist/LogViewer-macOS-arm64 .

echo "==> Build macOS (Intel)..."
GOOS=darwin GOARCH=amd64 go build -trimpath -ldflags="-s -w" -o dist/LogViewer-macOS-amd64 .

echo "==> Build Windows (amd64)..."
GOOS=windows GOARCH=amd64 go build -trimpath -ldflags="-s -w" -o dist/LogViewer-Windows-amd64.exe .

echo "==> Build Linux (amd64 - cho VPS)..."
GOOS=linux GOARCH=amd64 CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o dist/LogViewer-linux-amd64 .

echo "==> Build Linux (arm64)..."
GOOS=linux GOARCH=arm64 CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o dist/LogViewer-linux-arm64 .

# chỉ copy template .env nếu dist/.env chưa tồn tại (không ghi đè cấu hình thật)
[ -f dist/.env ] || cp .env.example dist/.env

echo ""
echo "==> Xong! File trong dist/:"
ls -lh dist/
