package transport

import (
	"crypto/tls"
	"crypto/x509"
	"encoding/pem"
	"io"
	"log"
	"net"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"
	"time"

	"github.com/letsencrypt/pebble/v2/ca"
	"github.com/letsencrypt/pebble/v2/db"
	"github.com/letsencrypt/pebble/v2/va"
	"github.com/letsencrypt/pebble/v2/wfe"

	"github.com/fosslife/drive/internal/config"
)

// 14.1: a configured hostname gets a real certificate from a real ACME
// exchange, and serves it. Staging Let's Encrypt cannot validate a machine
// with no public name, so the staging CA runs inside the test: Pebble is the
// same code path as a live CA — account, order, HTTP-01 challenge, CSR,
// issuance — with a root nobody trusts.
func TestACMEObtainsAndServesACertificate(t *testing.T) {
	directory, roots, challengePort := startPebble(t)

	acmeTrustedRoots, acmeHTTPPort = roots, challengePort
	t.Cleanup(func() { acmeTrustedRoots, acmeHTTPPort = nil, 0 })

	cfg := config.Config{
		DataDir:       t.TempDir(),
		Addr:          "127.0.0.1:0",
		Hostname:      "localhost", // the one name this machine really answers to
		ACMEDirectory: directory,
		ACMEEmail:     "ops@example.com",
	}
	ln, encrypted, err := Listen(t.Context(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	if !encrypted {
		t.Fatal("a configured hostname must serve HTTPS")
	}
	serve(t, ln)

	// Verified against the CA's root rather than skipped: the point of ACME is
	// that a client can check the chain, so the test checks it too.
	conn, err := tls.Dial("tcp", ln.Addr().String(), &tls.Config{ServerName: cfg.Hostname, RootCAs: roots})
	if err != nil {
		t.Fatalf("serving the certificate it obtained: %v", err)
	}
	defer conn.Close()

	served := conn.ConnectionState().PeerCertificates[0]
	if err := served.VerifyHostname(cfg.Hostname); err != nil {
		t.Errorf("certificate does not cover %s: %v", cfg.Hostname, err)
	}
	if time.Now().After(served.NotAfter) {
		t.Errorf("certificate already expired at %s", served.NotAfter)
	}
	if served.Issuer.String() == served.Subject.String() {
		t.Errorf("self-signed certificate served despite a hostname being configured: %s", served.Subject)
	}
}

// startPebble runs the ACME CA in this process and returns its directory URL,
// the pool that trusts what it issues, and the port its validator will look on
// for HTTP-01 challenges.
func startPebble(t *testing.T) (directory string, roots *x509.CertPool, challengePort int) {
	t.Helper()
	// Pebble sleeps a random few seconds before validating, to catch clients
	// that assume it is instant. We are not testing patience.
	t.Setenv("PEBBLE_VA_NOSLEEP", "1")

	logger := log.New(testWriter{t}, "pebble ", 0)
	store := db.NewMemoryStore()
	authority := ca.New(logger, store, "", "ecdsa", 0, 1, map[string]ca.Profile{"default": {Description: "default"}})
	challengePort = freePort(t)
	validator := va.New(logger, challengePort, freePort(t), false, "", store)
	front := wfe.New(logger, store, validator, authority, []string{"pebble.test"}, false, false, 0, 0)

	// The CA speaks HTTPS, so it needs a certificate of its own. httptest's
	// covers 127.0.0.1, which is the only address it will be reached on.
	srv := httptest.NewTLSServer(front.Handler())
	t.Cleanup(srv.Close)

	roots = x509.NewCertPool()
	roots.AddCert(srv.Certificate()) // to trust the CA's own HTTPS endpoint
	management := httptest.NewServer(front.ManagementHandler())
	t.Cleanup(management.Close)
	resp, err := http.Get(management.URL + wfe.RootCertPath + "0")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	rootPEM, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	if !roots.AppendCertsFromPEM(rootPEM) {
		t.Fatalf("pebble root is not a certificate: %q", rootPEM)
	}
	if block, _ := pem.Decode(rootPEM); block == nil {
		t.Fatal("pebble served no root certificate")
	}
	return srv.URL + wfe.DirectoryPath, roots, challengePort
}

// freePort picks a port nobody is using, for a listener something else opens.
func freePort(t *testing.T) int {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	_, port, _ := net.SplitHostPort(ln.Addr().String())
	n, _ := strconv.Atoi(port)
	return n
}

// testWriter sends the CA's chatter to the test log, where it is only printed
// if something fails.
type testWriter struct{ t *testing.T }

func (w testWriter) Write(p []byte) (int, error) {
	w.t.Logf("%s", p)
	return len(p), nil
}
