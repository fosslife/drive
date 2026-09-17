package transport

import (
	"bytes"
	"io"
	"log/slog"
	"net"
	"net/http"
	"os"
	"strings"
	"sync"
	"testing"

	"github.com/fosslife/drive/internal/config"
)

// 14.2: with nothing configured the drive serves plaintext and works. No
// certificate is invented, because a certificate this process signed for
// itself is a browser warning rather than security, and the first thing a new
// operator sees should not be one.
func TestUnconfiguredServesPlaintextQuietlyOnLoopback(t *testing.T) {
	logs := captureLogs(t)
	cfg := config.Config{DataDir: t.TempDir(), Addr: "127.0.0.1:0"}

	ln, encrypted, err := Listen(t.Context(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	if encrypted {
		t.Fatal("no hostname is configured, so there is no certificate to serve")
	}

	serve(t, ln)
	resp, err := http.Get("http://" + ln.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if body, _ := io.ReadAll(resp.Body); string(body) != "ok" {
		t.Errorf("answered %q, want ok", body)
	}

	if _, err := os.Stat(cfg.CertsDir()); err == nil {
		t.Error("something generated a certificate nobody asked for")
	}
	if strings.Contains(logs.String(), "level=WARN") {
		t.Errorf("localhost got a warning it does not need: %s", logs)
	}
}

// 14.3: the same thing on an address the network can reach says so. It still
// serves — the operator may be mid-setup, or behind a proxy that terminates
// TLS — but nobody gets to be surprised later.
func TestPlaintextOffLoopbackWarns(t *testing.T) {
	logs := captureLogs(t)

	ln, _, err := Listen(t.Context(), config.Config{DataDir: t.TempDir(), Addr: ":0"})
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()

	if !strings.Contains(logs.String(), "level=WARN") {
		t.Errorf("a network-reachable plaintext listener said nothing: %s", logs)
	}
	for _, want := range []string{"passwords", config.EnvHostname} {
		if !strings.Contains(logs.String(), want) {
			t.Errorf("the warning does not mention %q: %s", want, logs)
		}
	}
}

// serve answers anything with "ok" until the test ends.
func serve(t *testing.T, ln net.Listener) {
	t.Helper()
	srv := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, "ok")
	})}
	go srv.Serve(ln)
	t.Cleanup(func() { srv.Close() })
}

// captureLogs points the default logger at a buffer for the length of the test.
func captureLogs(t *testing.T) *syncBuffer {
	t.Helper()
	buf := &syncBuffer{}
	previous := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(buf, nil)))
	t.Cleanup(func() { slog.SetDefault(previous) })
	return buf
}

type syncBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *syncBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *syncBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}
