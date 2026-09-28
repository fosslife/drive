package main

import (
	"net"
	"testing"
)

// 6.1: the printed URL is the only way into a fresh instance, so it has to be
// one an operator can paste rather than one they have to interpret.
func TestSetupURL(t *testing.T) {
	for _, c := range []struct {
		addr      string
		encrypted bool
		hostname  string
		want      string
	}{
		{"[::]:8080", false, "", "http://localhost:8080/setup?token=abc"},
		{"0.0.0.0:8080", false, "", "http://localhost:8080/setup?token=abc"},
		{"0.0.0.0:8080", true, "", "https://localhost:8080/setup?token=abc"},
		{"127.0.0.1:9000", false, "", "http://127.0.0.1:9000/setup?token=abc"},
		{"[::1]:9000", false, "", "http://[::1]:9000/setup?token=abc"},
		// DRIVE_HOSTNAME is set: that name is the one an operator can reach,
		// and :443 is what the browser assumes anyway.
		{"[::]:443", true, "drive.example.com", "https://drive.example.com/setup?token=abc"},
		{"[::]:8443", true, "drive.example.com", "https://drive.example.com:8443/setup?token=abc"},
	} {
		addr, err := net.ResolveTCPAddr("tcp", c.addr)
		if err != nil {
			t.Fatal(err)
		}
		if got := setupURL(addr, c.encrypted, c.hostname, "abc"); got != c.want {
			t.Errorf("setupURL(%s, encrypted=%v, hostname=%q) = %q, want %q", c.addr, c.encrypted, c.hostname, got, c.want)
		}
	}

	// The token is escaped: it is base64url, but nothing should depend on that.
	addr, _ := net.ResolveTCPAddr("tcp", "127.0.0.1:80")
	if got := setupURL(addr, false, "", "a+b/c=d"); got != "http://127.0.0.1/setup?token=a%2Bb%2Fc%3Dd" {
		t.Errorf("token escaping: %q", got)
	}
}
