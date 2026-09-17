// Package config loads the server configuration. Every value has a working
// default, so the process starts with an empty environment and no config file.
package config

import (
	"fmt"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// Environment variables. An unset or empty value means "use the default".
const (
	EnvDataDir       = "DRIVE_DATA_DIR"
	EnvAddr          = "DRIVE_ADDR"
	EnvMinFree       = "DRIVE_MIN_FREE_BYTES"
	EnvScanInterval  = "DRIVE_SCAN_INTERVAL"
	EnvUploadTTL     = "DRIVE_UPLOAD_RETENTION"
	EnvTrashTTL      = "DRIVE_TRASH_RETENTION"
	EnvHostname      = "DRIVE_HOSTNAME"
	EnvACMEEmail     = "DRIVE_ACME_EMAIL"
	EnvACMEDirectory = "DRIVE_ACME_DIRECTORY"
)

// DefaultMinFree is the headroom kept on the data volume. A full disk is not
// graceful degradation: it fails writes partway and can leave the index needing
// manual recovery. The reserve turns that into an error message.
const DefaultMinFree = 1 << 30 // 1 GiB

// DefaultScanInterval is how often the reconciler rescans every storage root.
// It is the ceiling on how stale the index can be after an external change,
// and a scan of an unchanged root costs one stat per file.
const DefaultScanInterval = 15 * time.Minute

// DefaultUploadTTL is how long an interrupted upload waits to be resumed before
// its temporary data is reclaimed. Long enough to survive a laptop closing for
// the night, short enough that an abandoned 4 GB upload is not permanent.
const DefaultUploadTTL = 24 * time.Hour

// DefaultTrashTTL is how long a deleted file stays recoverable before it is
// permanently deleted. Zero means never: an instance that would rather buy
// disks than lose a file sets DRIVE_TRASH_RETENTION=0 and keeps everything.
const DefaultTrashTTL = 30 * 24 * time.Hour

// DefaultACMEDirectory is Let's Encrypt. DRIVE_ACME_DIRECTORY points at a
// staging or private CA instead; certificates from it are not publicly trusted.
const DefaultACMEDirectory = "https://acme-v02.api.letsencrypt.org/directory"

type Config struct {
	DataDir      string
	Addr         string
	MinFree      int64
	ScanInterval time.Duration
	UploadTTL    time.Duration
	TrashTTL     time.Duration

	// Hostname is the public name a certificate is obtained for, and the only
	// switch for HTTPS there is. Empty means plaintext: no name means no CA
	// will vouch for the drive, and a certificate it signs for itself is a
	// browser warning rather than security.
	Hostname      string
	ACMEEmail     string
	ACMEDirectory string
}

// IndexPath is the SQLite index, which is disposable: deleting it loses no
// user data, it is rebuilt by scanning the storage roots.
func (c Config) IndexPath() string { return filepath.Join(c.DataDir, "index.db") }

// UsersDir holds one storage root per user.
func (c Config) UsersDir() string { return filepath.Join(c.DataDir, "users") }

// CertsDir is where CertMagic keeps the account key and the certificates it
// obtains. Like the index it is rebuildable: deleting it costs one more trip
// to the CA. It does not exist at all unless a hostname is configured.
func (c Config) CertsDir() string { return filepath.Join(c.DataDir, "certs") }

// InvalidError names the offending value and what was expected.
type InvalidError struct {
	Name     string
	Value    string
	Expected string
}

func (e *InvalidError) Error() string {
	return fmt.Sprintf("invalid configuration: %s=%q, expected %s", e.Name, e.Value, e.Expected)
}

// Load reads the environment and validates it. A returned error means nothing
// was started: the caller must exit rather than run half-configured.
func Load() (Config, error) {
	c := Config{
		DataDir:       defaultDataDir(),
		MinFree:       DefaultMinFree,
		ScanInterval:  DefaultScanInterval,
		UploadTTL:     DefaultUploadTTL,
		TrashTTL:      DefaultTrashTTL,
		ACMEDirectory: DefaultACMEDirectory,
	}
	if v := os.Getenv(EnvDataDir); v != "" {
		c.DataDir = v
	}
	if v := os.Getenv(EnvHostname); v != "" {
		c.Hostname = v
	}
	c.ACMEEmail = os.Getenv(EnvACMEEmail)
	if v := os.Getenv(EnvACMEDirectory); v != "" {
		c.ACMEDirectory = v
	}
	// A hostname worth obtaining a certificate for is one the world reaches on
	// the port the world uses; :8080 would answer nothing that matters.
	if c.Hostname != "" {
		c.Addr = ":443"
	} else {
		c.Addr = ":8080"
	}
	if v := os.Getenv(EnvAddr); v != "" {
		c.Addr = v
	}
	if v := os.Getenv(EnvMinFree); v != "" {
		n, err := strconv.ParseInt(v, 10, 64)
		if err != nil || n < 0 {
			return Config{}, &InvalidError{EnvMinFree, v, "a non-negative whole number of bytes"}
		}
		c.MinFree = n
	}
	if v := os.Getenv(EnvScanInterval); v != "" {
		d, err := time.ParseDuration(v)
		if err != nil || d <= 0 {
			return Config{}, &InvalidError{EnvScanInterval, v, "a positive duration, for example 15m or 1h"}
		}
		c.ScanInterval = d
	}
	if v := os.Getenv(EnvUploadTTL); v != "" {
		d, err := time.ParseDuration(v)
		if err != nil || d <= 0 {
			return Config{}, &InvalidError{EnvUploadTTL, v, "a positive duration, for example 24h"}
		}
		c.UploadTTL = d
	}
	if v := os.Getenv(EnvTrashTTL); v != "" {
		// Zero is meaningful here and nowhere else: it is the never-expire
		// setting, so the bound is non-negative rather than positive.
		d, err := time.ParseDuration(v)
		if err != nil || d < 0 {
			return Config{}, &InvalidError{EnvTrashTTL, v, "a duration such as 720h, or 0 to keep deleted files forever"}
		}
		c.TrashTTL = d
	}
	return c, c.validate()
}

func (c Config) validate() error {
	if !filepath.IsAbs(c.DataDir) {
		return &InvalidError{EnvDataDir, c.DataDir, "an absolute path"}
	}
	_, port, err := net.SplitHostPort(c.Addr)
	if err != nil {
		return &InvalidError{EnvAddr, c.Addr, "a host:port address, for example :8080 or 127.0.0.1:8080"}
	}
	n, err := strconv.Atoi(port)
	if err != nil || n < 1 || n > 65535 {
		return &InvalidError{EnvAddr, c.Addr, "a port between 1 and 65535"}
	}
	if c.Hostname != "" && !validHostname(c.Hostname) {
		return &InvalidError{EnvHostname, c.Hostname, "a bare DNS name such as drive.example.com, with no scheme, port, or path"}
	}
	if u, err := url.Parse(c.ACMEDirectory); err != nil || u.Host == "" || (u.Scheme != "https" && u.Scheme != "http") {
		return &InvalidError{EnvACMEDirectory, c.ACMEDirectory, "the URL of an ACME directory, for example " + DefaultACMEDirectory}
	}
	return nil
}

// validHostname accepts what a certificate can name: DNS labels, nothing else.
// A scheme, a port, or a path here is a misunderstanding worth catching at
// startup rather than at the first failed ACME order.
func validHostname(h string) bool {
	if len(h) > 253 || strings.HasPrefix(h, "-") || strings.HasSuffix(h, "-") {
		return false
	}
	for _, label := range strings.Split(h, ".") {
		if label == "" || len(label) > 63 {
			return false
		}
		for _, r := range label {
			alnum := (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9')
			if !alnum && r != '-' {
				return false
			}
		}
	}
	return true
}

func defaultDataDir() string {
	if d := os.Getenv("XDG_DATA_HOME"); filepath.IsAbs(d) {
		return filepath.Join(d, "drive")
	}
	if home, err := os.UserHomeDir(); err == nil && filepath.IsAbs(home) {
		return filepath.Join(home, ".local", "share", "drive")
	}
	return "/var/lib/drive" // no home directory: containers, systemd units
}
