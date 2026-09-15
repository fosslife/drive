// Command drive is the whole product: one binary, one process, no external
// services.
package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/Sparkenstein/drive/internal/config"
	"github.com/Sparkenstein/drive/internal/index"
	"github.com/Sparkenstein/drive/internal/server"
)

// Version is overridden at build time with -ldflags "-X main.Version=v1.2.3".
var Version = "dev"

func main() {
	if len(os.Args) > 1 && os.Args[1] == "version" {
		fmt.Println(Version)
		return
	}
	if err := run(); err != nil {
		// Nothing is running at this point: a configuration or index problem
		// must stop the process rather than half-start it.
		fmt.Fprintln(os.Stderr, "drive: "+err.Error())
		os.Exit(1)
	}
}

func run() error {
	cfg, err := config.Load()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(cfg.DataDir, 0o700); err != nil {
		return fmt.Errorf("data directory: %w", err)
	}

	db, err := index.Open(cfg.IndexPath())
	if err != nil {
		return err
	}
	defer db.Close()

	listener, err := net.Listen("tcp", cfg.Addr)
	if err != nil {
		return fmt.Errorf("listening on %s: %w", cfg.Addr, err)
	}

	// TODO(14.x): HTTPS by default via CertMagic, self-signed fallback.
	encrypted := false
	slog.Info("drive started",
		"version", Version,
		"address", listener.Addr().String(),
		"encrypted", encrypted,
		"data_dir", cfg.DataDir,
		"schema_version", index.Version(),
	)

	s := server.New()
	s.SetReady(true)
	httpSrv := &http.Server{
		Handler:           s.Handler(),
		ReadHeaderTimeout: 10 * time.Second,
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	go func() {
		<-ctx.Done()
		s.SetReady(false)
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		httpSrv.Shutdown(shutdownCtx)
	}()

	if err := httpSrv.Serve(listener); err != nil && !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	slog.Info("drive stopped")
	return nil
}
