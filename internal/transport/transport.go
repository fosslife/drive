// Package transport opens the listener the server answers on.
//
// There are two modes and no third. With a public hostname the drive gets a
// real certificate from an ACME CA and serves HTTPS. Without one it serves
// plaintext, because the machine has no name a CA can vouch for and the honest
// answers at that point are a reverse proxy, a Tailscale address, or a laptop
// trying the thing out — none of which are improved by a certificate the drive
// signed for itself. Plaintext on an address other than loopback says so in the
// log, every start.
package transport

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"log/slog"
	"net"
	"path/filepath"

	"github.com/caddyserver/certmagic"

	"github.com/fosslife/drive/internal/config"
)

// Test seams. An ACME exchange is only worth testing against a real CA, and a
// CA that runs inside the test speaks HTTPS with its own root and validates on
// a port the test picked. Both are zero in production.
var (
	acmeTrustedRoots *x509.CertPool
	acmeHTTPPort     int
)

// Listen opens cfg.Addr, wrapped in TLS when a hostname was configured. The
// bool reports whether the transport is encrypted; the secure cookie flag and
// the printed setup URL both follow it.
func Listen(ctx context.Context, cfg config.Config) (net.Listener, bool, error) {
	var tlsCfg *tls.Config
	if cfg.Hostname != "" {
		var err error
		// Arranged before the port opens: a listener that accepts before it can
		// answer is a connection refused at best.
		if tlsCfg, err = acmeTLS(ctx, cfg); err != nil {
			return nil, false, err
		}
	}

	ln, err := net.Listen("tcp", cfg.Addr)
	if err != nil {
		return nil, false, fmt.Errorf("listening on %s: %w", cfg.Addr, err)
	}
	if tlsCfg == nil {
		warnIfExposed(ln.Addr())
		return ln, false, nil
	}
	return tls.NewListener(ln, tlsCfg), true, nil
}

// warnIfExposed says something when plaintext leaves the machine. Loopback is
// somebody trying the drive out and needs no lecture; anything else is a
// network that can read passwords, files, and session cookies off the wire.
func warnIfExposed(addr net.Addr) {
	tcp, ok := addr.(*net.TCPAddr)
	if ok && tcp.IP.IsLoopback() {
		return
	}
	slog.Warn("serving plaintext HTTP on an address other than localhost: anyone on this network can read passwords, files, and session cookies",
		"address", addr.String(),
		"fix", "put it behind a proxy that terminates TLS, or set "+config.EnvHostname+" to a public name for automatic certificates")
}

// acmeTLS obtains and keeps a certificate for the configured hostname.
// Obtaining it is synchronous: an instance that cannot get a certificate for
// the name it was told to serve should say so at startup rather than answer
// every handshake with an error nobody can read.
func acmeTLS(ctx context.Context, cfg config.Config) (*tls.Config, error) {
	var magic *certmagic.Config
	cache := certmagic.NewCache(certmagic.CacheOptions{
		GetConfigForCert: func(certmagic.Certificate) (*certmagic.Config, error) { return magic, nil },
	})
	magic = certmagic.New(cache, certmagic.Config{
		Storage: &certmagic.FileStorage{Path: filepath.Join(cfg.CertsDir(), "acme")},
	})
	magic.Issuers = []certmagic.Issuer{certmagic.NewACMEIssuer(magic, certmagic.ACMEIssuer{
		CA:           cfg.ACMEDirectory,
		Email:        cfg.ACMEEmail,
		Agreed:       true,
		AltHTTPPort:  acmeHTTPPort,
		TrustedRoots: acmeTrustedRoots,
	})}
	if err := magic.ManageSync(ctx, []string{cfg.Hostname}); err != nil {
		return nil, fmt.Errorf("certificate for %s: %w (unset %s to serve plaintext behind a proxy instead)",
			cfg.Hostname, err, config.EnvHostname)
	}
	slog.Info("serving HTTPS with an automatically renewed certificate",
		"hostname", cfg.Hostname, "ca", cfg.ACMEDirectory)

	tlsCfg := magic.TLSConfig()
	// http/2 is not in certmagic's defaults, and the ALPN entry it does add is
	// the TLS-ALPN challenge, which must stay first for renewals to work.
	tlsCfg.NextProtos = append(tlsCfg.NextProtos, "h2", "http/1.1")
	tlsCfg.MinVersion = tls.VersionTLS12
	return tlsCfg, nil
}
