package main

import (
	"context"
	"embed"
	"errors"
	"io/fs"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"readergo/internal/server"
	"strings"
	"syscall"
	"time"
)

//go:embed all:static
var static embed.FS

func main() {
	if e := run(); e != nil {
		slog.Error("reader stopped", "error", e)
		os.Exit(1)
	}
}
func healthcheckURL() string {
	addr := strings.TrimSpace(os.Getenv("READER_HTTP_ADDR"))
	if addr == "" {
		if port := strings.TrimSpace(os.Getenv("READER_PORT")); port != "" {
			addr = ":" + port
		} else {
			addr = ":8080"
		}
	}
	_, port, err := net.SplitHostPort(addr)
	if err != nil || port == "" {
		port = "8080"
	}
	return "http://127.0.0.1:" + port + "/health/ready"
}
func run() error {
	command := "serve"
	if len(os.Args) > 1 {
		command = os.Args[1]
	}
	if command == "healthcheck" {
		client := http.Client{Timeout: 3 * time.Second}
		resp, e := client.Get(healthcheckURL())
		if e != nil {
			return e
		}
		defer resp.Body.Close()
		if resp.StatusCode != 200 {
			return errors.New("service is not ready")
		}
		return nil
	}
	if command != "serve" && command != "migrate" {
		return errors.New("usage: reader [serve|migrate|healthcheck]")
	}
	cfg, e := server.LoadConfig()
	if e != nil {
		return e
	}
	db, e := server.OpenDB()
	if e != nil {
		return e
	}
	defer db.Close()
	if command == "migrate" {
		ctx, c := context.WithTimeout(context.Background(), time.Minute)
		defer c()
		if e = server.Migrate(ctx, db); e != nil {
			return e
		}
		slog.Info("schema migration complete")
		return nil
	}
	var version int
	if e = db.QueryRow("SELECT MAX(version) FROM schema_migrations").Scan(&version); e != nil || version != 1 {
		return errors.New("database schema not ready; run reader migrate first")
	}
	assets, e := fs.Sub(static, "static")
	if e != nil {
		return e
	}
	app := server.New(db, cfg, assets)
	httpServer := &http.Server{Addr: cfg.Addr, Handler: app.Handler(), ReadHeaderTimeout: 10 * time.Second, ReadTimeout: 120 * time.Second, WriteTimeout: 120 * time.Second, IdleTimeout: 60 * time.Second, MaxHeaderBytes: 32 << 10}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM, syscall.SIGINT)
	defer stop()
	done := make(chan error, 1)
	go func() {
		slog.Info("reader listening", "addr", cfg.Addr, "version", server.Version)
		done <- httpServer.ListenAndServe()
	}()
	select {
	case e := <-done:
		slog.Info("httpServer.ListenAndServe finished", "error", e)
		if !errors.Is(e, http.ErrServerClosed) {
			return e
		}
		return nil
	case <-ctx.Done():
		slog.Info("signal context done received, shutting down")
		shutdown, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		return httpServer.Shutdown(shutdown)
	}
}
