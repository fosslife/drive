package config

import (
	"errors"
	"path/filepath"
	"testing"
	"time"
)

// emptyEnv simulates starting with no environment at all.
func emptyEnv(t *testing.T) {
	t.Helper()
	for _, k := range []string{EnvDataDir, EnvAddr, EnvMinFree, EnvScanInterval, EnvTrashTTL,
		EnvHostname, EnvACMEEmail, EnvACMEDirectory, "XDG_DATA_HOME", "HOME"} {
		t.Setenv(k, "")
	}
}

func TestLoadWithEmptyEnvironment(t *testing.T) {
	emptyEnv(t)

	c, err := Load()
	if err != nil {
		t.Fatalf("Load with empty environment: %v", err)
	}
	if !filepath.IsAbs(c.DataDir) {
		t.Errorf("DataDir = %q, want an absolute path", c.DataDir)
	}
	if c.Addr != ":8080" {
		t.Errorf("Addr = %q, want :8080", c.Addr)
	}
	if got, want := c.IndexPath(), filepath.Join(c.DataDir, "index.db"); got != want {
		t.Errorf("IndexPath = %q, want %q", got, want)
	}
	if c.MinFree != DefaultMinFree || c.ScanInterval != DefaultScanInterval {
		t.Errorf("reserve %d and scan interval %s, want %d and %s",
			c.MinFree, c.ScanInterval, DefaultMinFree, DefaultScanInterval)
	}
	if c.TrashTTL != DefaultTrashTTL {
		t.Errorf("TrashTTL = %s, want %s", c.TrashTTL, DefaultTrashTTL)
	}
}

// 9.4: the retention period is configurable, and zero is the never-expire
// setting rather than "delete immediately" or a rejected value.
func TestTrashRetentionAcceptsZeroAsNever(t *testing.T) {
	emptyEnv(t)
	t.Setenv(EnvTrashTTL, "0")

	c, err := Load()
	if err != nil {
		t.Fatalf("Load with %s=0: %v", EnvTrashTTL, err)
	}
	if c.TrashTTL != 0 {
		t.Errorf("TrashTTL = %s, want 0 meaning never expire", c.TrashTTL)
	}

	t.Setenv(EnvTrashTTL, "168h")
	if c, err := Load(); err != nil || c.TrashTTL != 168*time.Hour {
		t.Errorf("TrashTTL = %s, %v, want 168h", c.TrashTTL, err)
	}

	t.Setenv(EnvTrashTTL, "-1h")
	var invalid *InvalidError
	if _, err := Load(); !errors.As(err, &invalid) || invalid.Name != EnvTrashTTL {
		t.Errorf("a negative retention loaded as %v, want an *InvalidError naming %s", err, EnvTrashTTL)
	}
}

// 14.3: a hostname is the only switch for HTTPS, and setting one moves the
// port with it, because a certificate for a public name is only useful on the
// port the public connects to.
func TestTransportDefaults(t *testing.T) {
	emptyEnv(t)
	c, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if c.Hostname != "" || c.ACMEDirectory != DefaultACMEDirectory {
		t.Errorf("hostname %q, directory %q, want no hostname and Let's Encrypt", c.Hostname, c.ACMEDirectory)
	}

	t.Setenv(EnvHostname, "drive.example.com")
	if c, err := Load(); err != nil || c.Addr != ":443" {
		t.Errorf("with a hostname the address is %q (%v), want :443", c.Addr, err)
	}
	t.Setenv(EnvAddr, "127.0.0.1:8443")
	if c, err := Load(); err != nil || c.Addr != "127.0.0.1:8443" {
		t.Errorf("explicit address became %q (%v)", c.Addr, err)
	}
}

func TestEnvironmentOverrides(t *testing.T) {
	emptyEnv(t)
	t.Setenv(EnvDataDir, "/srv/drive")
	t.Setenv(EnvAddr, "127.0.0.1:9000")
	t.Setenv(EnvScanInterval, "90s")

	c, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if c.DataDir != "/srv/drive" || c.Addr != "127.0.0.1:9000" {
		t.Errorf("got %+v, want data dir /srv/drive and addr 127.0.0.1:9000", c)
	}
	if c.ScanInterval != 90*time.Second {
		t.Errorf("ScanInterval = %s, want 90s", c.ScanInterval)
	}
}

func TestXDGDataHome(t *testing.T) {
	emptyEnv(t)
	t.Setenv("XDG_DATA_HOME", "/xdg")

	c, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if c.DataDir != "/xdg/drive" {
		t.Errorf("DataDir = %q, want /xdg/drive", c.DataDir)
	}
}

func TestInvalidConfigurationIsRejected(t *testing.T) {
	cases := []struct {
		name string
		env  map[string]string
		want string // the value the error must name
	}{
		{"relative data dir", map[string]string{EnvDataDir: "relative/path"}, EnvDataDir},
		{"data dir with no leading slash", map[string]string{EnvDataDir: "srv"}, EnvDataDir},
		{"address with no port", map[string]string{EnvAddr: "localhost"}, EnvAddr},
		{"port out of range", map[string]string{EnvAddr: ":70000"}, EnvAddr},
		{"non-numeric port", map[string]string{EnvAddr: ":http"}, EnvAddr},
		{"unparseable scan interval", map[string]string{EnvScanInterval: "soon"}, EnvScanInterval},
		{"scan interval with no unit", map[string]string{EnvScanInterval: "60"}, EnvScanInterval},
		{"zero scan interval", map[string]string{EnvScanInterval: "0s"}, EnvScanInterval},
		{"negative scan interval", map[string]string{EnvScanInterval: "-5m"}, EnvScanInterval},
		{"hostname as a URL", map[string]string{EnvHostname: "https://drive.example.com"}, EnvHostname},
		{"hostname with a port", map[string]string{EnvHostname: "drive.example.com:443"}, EnvHostname},
		{"hostname with a path", map[string]string{EnvHostname: "drive.example.com/files"}, EnvHostname},
		{"acme directory without a scheme", map[string]string{EnvACMEDirectory: "acme.example.com/dir"}, EnvACMEDirectory},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			emptyEnv(t)
			for k, v := range tc.env {
				t.Setenv(k, v)
			}

			_, err := Load()
			var invalid *InvalidError
			if !errors.As(err, &invalid) {
				t.Fatalf("Load returned %v, want an *InvalidError", err)
			}
			if invalid.Name != tc.want {
				t.Errorf("error names %q, want %q", invalid.Name, tc.want)
			}
			if invalid.Expected == "" {
				t.Error("error does not say what was expected")
			}
		})
	}
}
