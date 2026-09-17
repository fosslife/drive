package main

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// 14.4: the image is the same binary with a directory mounted into it, and the
// only way to know that is to build it and run it.
//
// Off by default: it needs a container engine, a network, and a few minutes,
// none of which belong in `go test ./...`. Run it with
// DRIVE_CONTAINER_TEST=1 go test ./cmd/drive -run Container.
func TestContainerImageServesFromAMountedDataDirectory(t *testing.T) {
	if os.Getenv("DRIVE_CONTAINER_TEST") == "" {
		t.Skip("set DRIVE_CONTAINER_TEST=1 to build and run the container image")
	}
	engine := containerEngine(t)
	repo, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	image := "localhost/drive-acceptance:test"

	ctx, cancel := context.WithTimeout(t.Context(), 15*time.Minute)
	defer cancel()
	engineRun(ctx, t, engine, "build", "-t", image, "-f", "Containerfile", repo)
	t.Cleanup(func() { exec.Command(engine, "rmi", "-f", image).Run() })

	data := t.TempDir()
	port := freeContainerPort(t)
	name := "drive-acceptance"
	exec.Command(engine, "rm", "-f", name).Run()
	engineRun(ctx, t, engine, "run", "-d", "--name", name,
		"-v", data+":/data:Z", "-p", fmt.Sprintf("127.0.0.1:%d:8080", port), image)
	t.Cleanup(func() { exec.Command(engine, "rm", "-f", name).Run() })

	// Plaintext, because no hostname is configured: in a container the thing in
	// front is a proxy or nothing at all, and neither wants a certificate from
	// in here.
	client := &http.Client{}
	url := fmt.Sprintf("http://127.0.0.1:%d/healthz", port)
	deadline := time.Now().Add(time.Minute)
	for {
		resp, err := client.Get(url)
		if err == nil {
			resp.Body.Close()
			if resp.StatusCode == http.StatusOK {
				break
			}
			err = fmt.Errorf("status %s", resp.Status)
		}
		if time.Now().After(deadline) {
			logs, _ := exec.Command(engine, "logs", name).CombinedOutput()
			t.Fatalf("%s never became reachable: %v\n%s", url, err, logs)
		}
		time.Sleep(500 * time.Millisecond)
	}

	// Reachable is half of it: the state has to be in the mount, or the next
	// `run` starts an empty drive.
	if _, err := os.Stat(filepath.Join(data, "index.db")); err != nil {
		entries, _ := os.ReadDir(data)
		t.Errorf("nothing was stored in the mounted directory: %v, contains %v", err, entries)
	}
}

func containerEngine(t *testing.T) string {
	t.Helper()
	for _, engine := range []string{"podman", "docker"} {
		if _, err := exec.LookPath(engine); err == nil {
			return engine
		}
	}
	t.Skip("no podman or docker on this machine")
	return ""
}

func engineRun(ctx context.Context, t *testing.T, name string, args ...string) {
	t.Helper()
	out, err := exec.CommandContext(ctx, name, args...).CombinedOutput()
	if err != nil {
		t.Fatalf("%s %s: %v\n%s", name, strings.Join(args, " "), err, out)
	}
}

func freeContainerPort(t *testing.T) int {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	return ln.Addr().(*net.TCPAddr).Port
}
