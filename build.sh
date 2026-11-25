#!/bin/sh
# Build Log Viewer cho macOS (arm64 + amd64) và Windows (amd64)
# Cần: go >= 1.22  (brew install go)
set -e
cd "$(dirname "$0")"

export PATH="/opt/homebrew/bin:$PATH:/usr/local/go/bin:$PATH"
mkdir -p dist

make_mac_app() {
	app="dist/LogViewer.app"
	echo "==> Tạo macOS app bundle để ghim vào Dock..."
	rm -rf "$app"
	mkdir -p "$app/Contents/MacOS"
	cp dist/LogViewer-macOS-arm64 "$app/Contents/MacOS/LogViewer.bin"
	cp macos/LogViewerLauncher.sh "$app/Contents/MacOS/LogViewer"
	cp macos/Info.plist "$app/Contents/Info.plist"
	chmod +x "$app/Contents/MacOS/LogViewer" "$app/Contents/MacOS/LogViewer.bin"
	echo "==> Đã tạo $app"
}

case "${1:-all}" in
	all)
		;;
	mac-app|--mac-app|--dock)
		echo "==> Build macOS (Apple Silicon)..."
		GOOS=darwin GOARCH=arm64 go build -trimpath -ldflags="-s -w" -o dist/LogViewer-macOS-arm64 .
		make_mac_app
		exit 0
		;;
	*)
		echo "Dùng: $0 [all|mac-app]"
		exit 2
		;;
esac

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

make_mac_app

# chỉ copy template .env nếu dist/.env chưa tồn tại (không ghi đè cấu hình thật)
[ -f dist/.env ] || cp .env.example dist/.env

echo ""
echo "==> Xong! File trong dist/:"
ls -lh dist/
