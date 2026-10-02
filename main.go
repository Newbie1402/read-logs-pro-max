// Log Viewer — đọc logs MinIO nhẹ nhàng, chạy local trên macOS/Windows.
package main

import (
	"context"
	"embed"
	"errors"
	"flag"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"runtime"
	"syscall"
	"time"

	"readlogs/internal/config"
	"readlogs/internal/server"
	"readlogs/internal/store"
)

//go:embed web
var webFS embed.FS

func main() {
	log.SetFlags(log.Ltime)
	portFlag := flag.String("port", "", "port HTTP (mặc định: PORT trong .env hoặc 8765)")
	noOpen := flag.Bool("no-open", false, "không tự mở trình duyệt")
	flag.Parse()

	cfg := config.Load()
	if *portFlag != "" {
		cfg.Port = *portFlag
	}
	st, err := store.New(cfg.CacheDir)
	if err != nil {
		log.Fatalf("không tạo được thư mục cache %s: %v", cfg.CacheDir, err)
	}
	st.RemoveTmpParts()
	if n := st.CleanOld(cfg.AutoCleanDays); n > 0 {
		log.Printf("[cache] tự dọn %d thư mục cũ hơn %d ngày", n, cfg.AutoCleanDays)
	}

	app, err := server.New(cfg, st, webFS)
	if err != nil {
		log.Fatalf("khởi tạo app lỗi: %v", err)
	}

	addr := net.JoinHostPort("127.0.0.1", cfg.Port)
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		log.Fatalf("không mở được cổng %s: %v", cfg.Port, err)
	}
	srv := &http.Server{Handler: app.Routes(), ReadHeaderTimeout: 10 * time.Second}

	url := fmt.Sprintf("http://localhost:%s", cfg.Port)
	fmt.Printf("╔══════════════════════════════════════════╗\n")
	fmt.Printf("║  Log Viewer %s\n", server.Version)
	fmt.Printf("║  Đang chạy tại: %s\n", url)
	fmt.Printf("║  Cache:        %s\n", cfg.CacheDir)
	fmt.Printf("║  Dừng app:     Ctrl+C  (cache giữ nguyên để lần sau mở nhanh)\n")
	fmt.Printf("╚══════════════════════════════════════════╝\n")

	if !*noOpen {
		go func() {
			time.Sleep(400 * time.Millisecond)
			openBrowser(url)
		}()
	}

	go func() {
		if err := srv.Serve(ln); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Fatalf("server lỗi: %v", err)
		}
	}()

	sig := make(chan os.Signal, 1)
	signal.Notify(sig, os.Interrupt, syscall.SIGTERM)
	<-sig
	fmt.Println("\nĐang tắt...")
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	srv.Shutdown(ctx)
	fmt.Println("Tạm biệt! Cache log vẫn nằm ở:", cfg.CacheDir)
}

func openBrowser(url string) {
	switch runtime.GOOS {
	case "darwin":
		_ = run("open", url)
	case "windows":
		_ = run("cmd", "/c", "start", "", url)
	default:
		_ = run("xdg-open", url)
	}
}

func run(name string, args ...string) error {
	return exec.Command(name, args...).Start()
}
