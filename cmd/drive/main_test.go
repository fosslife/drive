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
		want      string
	}{
		{"[::]:8080", false, "http://localhost:8080/setup?token=abc"},
		{"0.0.0.0:8080", false, "http://localhost:8080/setup?token=abc"},
		{"0.0.0.0:8080", true, "https://localhost:8080/setup?token=abc"},
		{"127.0.0.1:9000", false, "http://127.0.0.1:9000/setup?token=abc"},
		{"[::1]:9000", false, "http://[::1]:9000/setup?token=abc"},
	} {
		addr, err := net.ResolveTCPAddr("tcp", c.addr)
		if err != nil {
			t.Fatal(err)
		}
		if got := setupURL(addr, c.encrypted, "abc"); got != c.want {
			t.Errorf("setupURL(%s, encrypted=%v) = %q, want %q", c.addr, c.encrypted, got, c.want)
		}
	}

	// The token is escaped: it is base64url, but nothing should depend on that.
	addr, _ := net.ResolveTCPAddr("tcp", "127.0.0.1:80")
	if got := setupURL(addr, false, "a+b/c=d"); got != "http://127.0.0.1:80/setup?token=a%2Bb%2Fc%3Dd" {
		t.Errorf("token escaping: %q", got)
	}
}
