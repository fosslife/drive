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
	"net/url"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/fosslife/drive/internal/auth"
	"github.com/fosslife/drive/internal/config"
	"github.com/fosslife/drive/internal/files"
	"github.com/fosslife/drive/internal/index"
	"github.com/fosslife/drive/internal/scan"
	"github.com/fosslife/drive/internal/server"
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

	db, movedTo, err := index.OpenOrReset(cfg.IndexPath())
	if err != nil {
		return err
	}
	defer db.Close()
	if movedTo != "" {
		slog.Warn("index was unreadable and has been replaced by an empty one; file metadata is being rebuilt by scanning, but accounts, API tokens, and share links were in it and are gone",
			"moved_to", movedTo)
	}

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
		"scan_interval", cfg.ScanInterval,
	)

	scanner := scan.NewScanner(db, cfg.DataDir, cfg.ScanInterval)
	users := auth.NewStore(db, cfg.DataDir, cfg.MinFree)

	// No default credentials, ever. With no account the only way in is the
	// one-time token printed here, and it stops working once setup completes.
	setupToken, err := users.OpenSetup()
	if err != nil {
		return err
	}
	if setupToken != "" {
		slog.Warn("no account exists yet: open this URL to create the first administrator",
			"url", setupURL(listener.Addr(), encrypted, setupToken))
	}

	// Secure cookies follow the listener: set unconditionally they would not be
	// sent at all over the plaintext listener, which is every login failing.
	sessions := auth.NewSessions(db, encrypted)
	s := server.New(db, users, sessions, scanner.Status)
	s.SetReady(true)
	httpSrv := &http.Server{
		Handler:           s.Handler(),
		ReadHeaderTimeout: 10 * time.Second,
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	// Reconciliation runs alongside serving, never before it. A first scan of a
	// large root takes minutes and must not delay the port opening.
	go scanner.Run(ctx)
	go reclaimUploads(ctx, db, cfg.DataDir, cfg.UploadTTL)
	go expireTrash(ctx, db, cfg.DataDir, cfg.TrashTTL)

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

// reclaimUploads discards upload data nobody came back for. It runs on its own
// timer rather than inside the reconciler, which has no delete path at all and
// must keep it that way.
func reclaimUploads(ctx context.Context, db *index.DB, dataDir string, ttl time.Duration) {
	// A tenth of the retention period: often enough that abandoned data does
	// not sit around for a whole extra day, rare enough to cost nothing.
	ticker := time.NewTicker(max(ttl/10, time.Minute))
	defer ticker.Stop()
	for {
		if n, err := files.Reclaim(db, dataDir, ttl); err != nil {
			slog.Error("reclaiming abandoned uploads", "error", err)
		} else if n > 0 {
			slog.Info("reclaimed abandoned uploads", "uploads", n, "older_than", ttl)
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

// expireTrash permanently deletes what has sat in the trash past the retention
// period. Like the upload sweep it runs on its own timer, well away from the
// reconciler. A retention of zero switches it off: deleted files then stay
// until someone asks for them to go.
func expireTrash(ctx context.Context, db *index.DB, dataDir string, ttl time.Duration) {
	if ttl <= 0 {
		slog.Info("trash never expires", "reason", config.EnvTrashTTL+"=0")
		return
	}
	// Hourly at most: the retention period is measured in days, and the cost of
	// noticing an expiry an hour late is nothing.
	ticker := time.NewTicker(min(max(ttl/10, time.Minute), time.Hour))
	defer ticker.Stop()
	for {
		if n, err := files.ExpireTrash(db, dataDir, ttl); err != nil {
			slog.Error("expiring trash", "error", err)
		} else if n > 0 {
			slog.Info("permanently deleted expired trash", "items", n, "older_than", ttl)
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

// setupURL is the address an operator can actually paste into a browser. A
// wildcard listener answers on every address and so names none of them;
// localhost is the one that always reaches it from the machine reading this log.
func setupURL(addr net.Addr, encrypted bool, token string) string {
	scheme := "http"
	if encrypted {
		scheme = "https"
	}
	host, port, err := net.SplitHostPort(addr.String())
	if err != nil {
		host, port = addr.String(), ""
	}
	if ip := net.ParseIP(host); host == "" || (ip != nil && ip.IsUnspecified()) {
		host = "localhost"
	}
	if port != "" {
		host = net.JoinHostPort(host, port)
	}
	u := url.URL{Scheme: scheme, Host: host, Path: "/setup"}
	u.RawQuery = url.Values{"token": {token}}.Encode()
	return u.String()
}
