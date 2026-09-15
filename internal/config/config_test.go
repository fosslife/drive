package config

import (
	"errors"
	"path/filepath"
	"testing"
)

// emptyEnv simulates starting with no environment at all.
func emptyEnv(t *testing.T) {
	t.Helper()
	for _, k := range []string{EnvDataDir, EnvAddr, "XDG_DATA_HOME", "HOME"} {
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
}

func TestEnvironmentOverrides(t *testing.T) {
	emptyEnv(t)
	t.Setenv(EnvDataDir, "/srv/drive")
	t.Setenv(EnvAddr, "127.0.0.1:9000")

	c, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if c.DataDir != "/srv/drive" || c.Addr != "127.0.0.1:9000" {
		t.Errorf("got %+v, want data dir /srv/drive and addr 127.0.0.1:9000", c)
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
		name    string
		dataDir string
		addr    string
		want    string // the value the error must name
	}{
		{"relative data dir", "relative/path", "", EnvDataDir},
		{"data dir with no leading slash", "srv", "", EnvDataDir},
		{"address with no port", "", "localhost", EnvAddr},
		{"port out of range", "", ":70000", EnvAddr},
		{"non-numeric port", "", ":http", EnvAddr},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			emptyEnv(t)
			t.Setenv(EnvDataDir, tc.dataDir)
			t.Setenv(EnvAddr, tc.addr)

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
