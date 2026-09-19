package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"testing"
	"time"
)

// 15.5: a drop mid-upload is the case tus exists for, and in a real deployment
// the connection that drops is the one to a reverse proxy rather than to the
// drive. Each proxy is run from the configuration someone actually writes, so
// its own defaults are what is under test.
//
// The finding: Caddy carries an 8 MiB upload chunk unconfigured, and nginx does
// not — `client_max_body_size` defaults to 1 MiB and refuses the chunk 413
// before the drive hears about it. That is the operator's setting to raise, so
// it is checked from both sides: the refusal, which the interface explains and
// docs/proxies.md names, and the resume once the documented line is in place.
//
// Off by default: it pulls two images and needs a container engine and a
// network. Run it with DRIVE_PROXY_TEST=1 go test ./cmd/drive -run Proxy.
//
// Cloudflare is not here. It cannot be: it needs an account, a public hostname
// and a tunnel out of this machine, none of which a test can conjure. Its
// documented limits are in docs/proxies.md, marked as unverified.
func TestInterruptedUploadResumesThroughAProxy(t *testing.T) {
	if os.Getenv("DRIVE_PROXY_TEST") == "" {
		t.Skip("set DRIVE_PROXY_TEST=1 to run nginx and Caddy in front of a real instance")
	}
	engine := containerEngine(t)
	d, token := newDrive(t)
	behind := drivePort(t, d.base)

	for _, proxy := range []struct {
		name, image, dest, config string
		check                     func(*testing.T, *drive, string, string)
	}{
		{
			name: "nginx-unconfigured", image: "docker.io/library/nginx:alpine", dest: "/etc/nginx/nginx.conf",
			config: "events {}\nhttp {\n  server {\n    listen 127.0.0.1:%d;\n    location / { proxy_pass http://127.0.0.1:%d; }\n  }\n}\n",
			check:  refusesTheChunk,
		},
		{
			name: "nginx", image: "docker.io/library/nginx:alpine", dest: "/etc/nginx/nginx.conf",
			// The two lines docs/proxies.md asks for: no body limit, and the body
			// streamed rather than buffered so a drop is resumed where it stopped.
			config: "events {}\nhttp {\n  server {\n    listen 127.0.0.1:%d;\n    location / {\n      proxy_pass http://127.0.0.1:%d;\n      client_max_body_size 0;\n      proxy_request_buffering off;\n    }\n  }\n}\n",
			check:  resumeThroughProxy,
		},
		{
			name: "caddy", image: "docker.io/library/caddy:alpine", dest: "/etc/caddy/Caddyfile",
			config: "{\n  admin off\n  auto_https off\n}\n:%d {\n  reverse_proxy 127.0.0.1:%d\n}\n",
			check:  resumeThroughProxy,
		},
	} {
		t.Run(proxy.name, func(t *testing.T) {
			port := freeContainerPort(t)
			conf := filepath.Join(t.TempDir(), "config")
			if err := os.WriteFile(conf, fmt.Appendf(nil, proxy.config, port, behind), 0o644); err != nil {
				t.Fatal(err)
			}

			ctx, cancel := context.WithTimeout(t.Context(), 10*time.Minute)
			defer cancel()
			name := "drive-proxy-" + proxy.name
			exec.Command(engine, "rm", "-f", name).Run()
			// Host networking, so the proxy reaches the drive on loopback exactly
			// as it would when installed on the machine rather than beside it.
			engineRun(ctx, t, engine, "run", "-d", "--name", name, "--network=host",
				"-v", conf+":"+proxy.dest+":ro,Z", proxy.image)
			t.Cleanup(func() { exec.Command(engine, "rm", "-f", name).Run() })

			via := d.through(fmt.Sprintf("http://127.0.0.1:%d", port))
			waitFor(t, engine, name, via.base+"/healthz")
			proxy.check(t, via, token, proxy.name+".bin")
		})
	}
}

// refusesTheChunk is the nginx default: 1 MiB, so the 8 MiB chunk the web
// client sends never reaches the drive. The drive itself has no request size
// limit, so a 413 can only have come from in front of it — which is what the
// interface's message for that status says, naming the setting to raise.
func refusesTheChunk(t *testing.T, via *drive, token, name string) {
	t.Helper()
	const size = 8 << 20
	id := via.createUpload(token, "", name, size)
	resp := via.patch(token, id, 0, io.LimitReader(newFiller(), size))
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusRequestEntityTooLarge {
		t.Errorf("an %d-byte chunk through an unconfigured nginx: %s, want 413 — docs/proxies.md says so", size, resp.Status)
	}
	// Nothing of the refused chunk landed, so the upload is still resumable
	// once the operator raises the limit.
	if offset, open := via.uploadOffset(token, id); !open || offset != 0 {
		t.Errorf("the refused chunk left %d bytes on the server (still open: %v)", offset, open)
	}
}

// resumeThroughProxy uploads a file the size a browser would send in one chunk,
// drops the connection halfway through it, and resumes from whatever offset the
// server reports. The file that lands has to match the source byte for byte.
func resumeThroughProxy(t *testing.T, via *drive, token, name string) {
	t.Helper()
	const size = 8 << 20 // the chunk size the web client uses
	content := make([]byte, size)
	if _, err := io.ReadFull(newFiller(), content); err != nil {
		t.Fatal(err)
	}
	want := sha256.Sum256(content)

	id := via.createUpload(token, "", name, size)
	resp, err := via.patchMaybe(token, id, 0, &dropped{content: content, left: size / 2})
	if err == nil {
		defer resp.Body.Close()
		// A proxy that buffers the whole request body never forwards a truncated
		// one, so the drive may not have heard about this at all.
		t.Logf("%s: the truncated chunk was answered %s rather than dropped", name, resp.Status)
	}

	// The server's offset is the only one a client may believe, and a proxy is
	// what makes that rule earn its keep: a buffering one has forwarded nothing,
	// so the offset is still zero, and a streaming one goes on forwarding what it
	// had buffered after the client is gone, so the offset can move between being
	// read and being used. The server answers that with 409 and its current
	// offset, and re-reading is the whole of the response — the same rule the web
	// client follows.
	published := false
	for attempt := 0; !published && attempt < 6; attempt++ {
		offset, open := via.uploadOffset(token, id)
		t.Logf("%s: the server has %d of %d bytes", name, offset, size)
		if !open {
			published = true // the drop delivered the last byte after all
			break
		}
		// bytes.Reader rather than a plain one: the request then carries a
		// Content-Length, which is what a proxy decides about a large body on.
		resp := via.patch(token, id, offset, bytes.NewReader(content[offset:]))
		status := resp.Status
		resp.Body.Close()
		switch resp.StatusCode {
		case http.StatusCreated:
			published = true
		case http.StatusConflict:
			continue
		default:
			t.Fatalf("resuming at %d: %s", offset, status)
		}
	}
	if !published {
		t.Fatalf("%s never finished uploading", name)
	}

	sum := sha256.New()
	resp = via.api("GET", "/api/download/"+name, token, nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("downloading through the proxy: %s %s", resp.Status, body(t, resp))
	}
	defer resp.Body.Close()
	if _, err := io.Copy(sum, resp.Body); err != nil {
		t.Fatal(err)
	}
	if got := hex.EncodeToString(sum.Sum(nil)); got != hex.EncodeToString(want[:]) {
		t.Errorf("the resumed file hashes to %s, want %s", got, hex.EncodeToString(want[:]))
	}
}

// dropped is a request body that stops mid-transfer, which is what a client
// losing its connection looks like from this side.
type dropped struct {
	content  []byte
	at, left int
}

func (d *dropped) Read(p []byte) (int, error) {
	if d.left <= 0 {
		return 0, errors.New("the connection dropped")
	}
	n := copy(p, d.content[d.at:min(d.at+d.left, len(d.content))])
	d.at += n
	d.left -= n
	return n, nil
}

// waitFor blocks until url answers, reporting the container's own log if it
// never does: a proxy that failed to start says why there and nowhere else.
func waitFor(t *testing.T, engine, container, url string) {
	t.Helper()
	deadline := time.Now().Add(time.Minute)
	for {
		resp, err := http.Get(url)
		if err == nil {
			resp.Body.Close()
			if resp.StatusCode == http.StatusOK {
				return
			}
			err = errors.New(resp.Status)
		}
		if time.Now().After(deadline) {
			logs, _ := exec.Command(engine, "logs", container).CombinedOutput()
			t.Fatalf("%s never answered through %s: %v\n%s", url, container, err, logs)
		}
		time.Sleep(200 * time.Millisecond)
	}
}

// drivePort is the port the instance itself listens on, which is what the
// proxy config has to name.
func drivePort(t *testing.T, base string) int {
	t.Helper()
	u, err := url.Parse(base)
	if err != nil {
		t.Fatal(err)
	}
	port, err := strconv.Atoi(u.Port())
	if err != nil {
		t.Fatalf("no port in %q: %v", base, err)
	}
	return port
}
