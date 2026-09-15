// Package config loads the server configuration. Every value has a working
// default, so the process starts with an empty environment and no config file.
package config

import (
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strconv"
)

// Environment variables. An unset or empty value means "use the default".
const (
	EnvDataDir = "DRIVE_DATA_DIR"
	EnvAddr    = "DRIVE_ADDR"
	EnvMinFree = "DRIVE_MIN_FREE_BYTES"
)

// DefaultMinFree is the headroom kept on the data volume. A full disk is not
// graceful degradation: it fails writes partway and can leave the index needing
// manual recovery. The reserve turns that into an error message.
const DefaultMinFree = 1 << 30 // 1 GiB

type Config struct {
	DataDir string
	Addr    string
	MinFree int64
}

// IndexPath is the SQLite index, which is disposable: deleting it loses no
// user data, it is rebuilt by scanning the storage roots.
func (c Config) IndexPath() string { return filepath.Join(c.DataDir, "index.db") }

// UsersDir holds one storage root per user.
func (c Config) UsersDir() string { return filepath.Join(c.DataDir, "users") }

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
		DataDir: defaultDataDir(),
		Addr:    ":8080",
		MinFree: DefaultMinFree,
	}
	if v := os.Getenv(EnvDataDir); v != "" {
		c.DataDir = v
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
	return nil
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
