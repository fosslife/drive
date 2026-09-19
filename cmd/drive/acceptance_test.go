package main

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/cookiejar"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"testing"
	"time"
)

// Section 15 is the end-to-end pass: the claims in the proposal are about the
// product, not about a package, so these run the binary this repository builds
// as a process and talk to it over its own listener. What a unit test can prove
// about one function is already proved next to that function.

const acceptancePassword = "correct horse battery staple"

// built compiles the binary under test once for the whole package.
var built struct {
	sync.Once
	dir, path string
	err       error
}

func TestMain(m *testing.M) {
	code := m.Run()
	if built.dir != "" {
		os.RemoveAll(built.dir)
	}
	os.Exit(code)
}

func binary(t *testing.T) string {
	t.Helper()
	built.Do(func() {
		tool, err := goTool()
		if err != nil {
			built.err = err
			return
		}
		if built.dir, built.err = os.MkdirTemp("", "drive-acceptance-"); built.err != nil {
			return
		}
		built.path = filepath.Join(built.dir, "drive")
		// The working directory is this package, so "." is the command itself.
		if out, err := exec.Command(tool, "build", "-o", built.path, ".").CombinedOutput(); err != nil {
			built.err = fmt.Errorf("go build: %w\n%s", err, out)
		}
	})
	if built.err != nil {
		t.Fatal(built.err)
	}
	return built.path
}

func goTool() (string, error) {
	if p, err := exec.LookPath("go"); err == nil {
		return p, nil
	}
	// Go does not have to be on PATH to have started this test; it does have to
	// be somewhere, and the toolchain that built this binary knows where.
	if p := filepath.Join(runtime.GOROOT(), "bin", "go"); p != "" {
		if _, err := os.Stat(p); err == nil {
			return p, nil
		}
	}
	return "", errors.New("no go toolchain to build the binary under test with")
}

// drive is a running instance: its address, its data directory, and the log it
// is writing, which is where the first-run token comes from.
type drive struct {
	t       *testing.T
	base    string
	dataDir string
	cmd     *exec.Cmd
	out     *transcript
	client  *http.Client
	stopped sync.Once
}

// transcript collects the process output for the assertions and for the failure
// messages, which are useless without it.
type transcript struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (l *transcript) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.buf.Write(p)
}

func (l *transcript) String() string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.buf.String()
}

// launch starts the binary over dataDir and waits for it to answer. wrapper is
// prefixed to the command line, which is how the strace check below runs the
// same instance under a tracer.
func launch(t *testing.T, dataDir string, wrapper []string, env ...string) *drive {
	t.Helper()
	bin := binary(t)
	addr := fmt.Sprintf("127.0.0.1:%d", freeContainerPort(t))

	argv := append(append([]string{}, wrapper...), bin)
	cmd := exec.Command(argv[0], argv[1:]...)
	cmd.Env = append(os.Environ(), "DRIVE_DATA_DIR="+dataDir, "DRIVE_ADDR="+addr)
	cmd.Env = append(cmd.Env, env...)
	out := &transcript{}
	cmd.Stdout, cmd.Stderr = out, out
	// Its own process group, so stopping it stops the tracer with it.
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}

	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatal(err)
	}
	d := &drive{t: t, base: "http://" + addr, dataDir: dataDir, cmd: cmd, out: out,
		client: &http.Client{Jar: jar, Timeout: 10 * time.Minute}}
	t.Cleanup(d.stop)

	deadline := time.Now().Add(time.Minute)
	for {
		resp, err := d.client.Get(d.base + "/healthz")
		if err == nil {
			resp.Body.Close()
			if resp.StatusCode == http.StatusOK {
				return d
			}
		}
		if time.Now().After(deadline) {
			t.Fatalf("the drive never became ready:\n%s", out)
		}
		time.Sleep(50 * time.Millisecond)
	}
}

// newDrive is the common case: a fresh instance with an administrator and an
// API token to reach it with.
func newDrive(t *testing.T, env ...string) (*drive, string) {
	t.Helper()
	d := launch(t, t.TempDir(), nil, env...)
	return d, d.signUp("ada")
}

func (d *drive) stop() {
	d.stopped.Do(func() {
		syscall.Kill(-d.cmd.Process.Pid, syscall.SIGTERM)
		done := make(chan struct{})
		go func() { d.cmd.Wait(); close(done) }()
		select {
		case <-done:
		case <-time.After(30 * time.Second):
			syscall.Kill(-d.cmd.Process.Pid, syscall.SIGKILL)
			<-done
		}
	})
}

var setupToken = regexp.MustCompile(`/setup\?token=([^"\s]+)`)

// signUp completes the first-run flow with the token the process printed and
// returns an API token for the account it created.
func (d *drive) signUp(username string) string {
	d.t.Helper()
	var token string
	deadline := time.Now().Add(30 * time.Second)
	for token == "" {
		if m := setupToken.FindStringSubmatch(d.out.String()); m != nil {
			token = strings.ReplaceAll(m[1], "%3D", "=")
		} else if time.Now().After(deadline) {
			d.t.Fatalf("no setup URL was printed:\n%s", d.out)
		} else {
			time.Sleep(50 * time.Millisecond)
		}
	}

	resp := d.api("POST", "/api/setup", "", map[string]string{
		"token": token, "username": username, "password": acceptancePassword,
	})
	if resp.StatusCode != http.StatusCreated {
		d.t.Fatalf("setup: %s %s", resp.Status, body(d.t, resp))
	}
	resp.Body.Close()

	// The session from setup is what creates the token; everything after this
	// uses the token, which is the same access by a different credential.
	resp = d.api("POST", "/api/tokens", "", map[string]string{"name": "acceptance"})
	if resp.StatusCode != http.StatusCreated {
		d.t.Fatalf("creating an API token: %s %s", resp.Status, body(d.t, resp))
	}
	defer resp.Body.Close()
	var created struct {
		Secret string `json:"secret"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&created); err != nil {
		d.t.Fatal(err)
	}
	return created.Secret
}

// api sends one JSON request. bearer is an API token, or "" to fall back to
// whatever session cookie the client is holding.
func (d *drive) api(method, path, bearer string, in any) *http.Response {
	d.t.Helper()
	var r io.Reader
	if in != nil {
		b, err := json.Marshal(in)
		if err != nil {
			d.t.Fatal(err)
		}
		r = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(d.t.Context(), method, d.base+path, r)
	if err != nil {
		d.t.Fatal(err)
	}
	// What a browser sets and script cannot forge. Without it a state-changing
	// request is refused as cross-site, which is the point of that check.
	req.Header.Set("Sec-Fetch-Site", "same-origin")
	if bearer != "" {
		req.Header.Set("Authorization", "Bearer "+bearer)
	}
	resp, err := d.client.Do(req)
	if err != nil {
		d.t.Fatalf("%s %s: %v\n%s", method, path, err, d.out)
	}
	return resp
}

func body(t *testing.T, resp *http.Response) string {
	t.Helper()
	defer resp.Body.Close()
	b, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func unmarshal[T any](t *testing.T, resp *http.Response) T {
	t.Helper()
	var v T
	s := body(t, resp)
	if err := json.Unmarshal([]byte(s), &v); err != nil {
		t.Fatalf("decoding %q: %v", s, err)
	}
	return v
}

type entry struct {
	ID   int64  `json:"id"`
	Name string `json:"name"`
	Path string `json:"path"`
	Size int64  `json:"size"`
}

type listing struct {
	Entries []entry `json:"entries"`
}

func (d *drive) list(token, path string) []entry {
	d.t.Helper()
	resp := d.api("GET", "/api/list?path="+path, token, nil)
	if resp.StatusCode != http.StatusOK {
		d.t.Fatalf("listing %q: %s %s", path, resp.Status, body(d.t, resp))
	}
	return unmarshal[listing](d.t, resp).Entries
}

// createUpload reserves an upload and returns its id.
func (d *drive) createUpload(token, dir, name string, size int64) string {
	d.t.Helper()
	req, err := http.NewRequestWithContext(d.t.Context(), "POST", d.base+"/api/uploads", nil)
	if err != nil {
		d.t.Fatal(err)
	}
	b64 := base64.StdEncoding.EncodeToString
	req.Header.Set("Tus-Resumable", "1.0.0")
	req.Header.Set("Upload-Length", strconv.FormatInt(size, 10))
	req.Header.Set("Upload-Metadata", "filename "+b64([]byte(name))+",dir "+b64([]byte(dir)))
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err := d.client.Do(req)
	if err != nil {
		d.t.Fatalf("creating an upload: %v\n%s", err, d.out)
	}
	if resp.StatusCode != http.StatusCreated {
		d.t.Fatalf("creating an upload: %s %s", resp.Status, body(d.t, resp))
	}
	resp.Body.Close()
	return strings.TrimPrefix(resp.Header.Get("Location"), "/api/uploads/")
}

// patch sends one chunk of an upload from offset.
func (d *drive) patch(token, id string, offset int64, src io.Reader) *http.Response {
	d.t.Helper()
	resp, err := d.patchMaybe(token, id, offset, src)
	if err != nil {
		d.t.Fatalf("uploading at %d: %v\n%s", offset, err, d.out)
	}
	return resp
}

// patchMaybe is patch for a caller that expects the transfer to fail, which is
// the whole of the resumption story.
func (d *drive) patchMaybe(token, id string, offset int64, src io.Reader) (*http.Response, error) {
	d.t.Helper()
	req, err := http.NewRequestWithContext(d.t.Context(), "PATCH", d.base+"/api/uploads/"+id, src)
	if err != nil {
		d.t.Fatal(err)
	}
	req.Header.Set("Tus-Resumable", "1.0.0")
	req.Header.Set("Content-Type", "application/offset+octet-stream")
	req.Header.Set("Upload-Offset", strconv.FormatInt(offset, 10))
	req.Header.Set("Authorization", "Bearer "+token)
	return d.client.Do(req)
}

// uploadOffset asks the server how much of an upload it really has, which is
// the only answer a resuming client may believe. open is false once the upload
// is no longer in flight: the last byte arrived and the file was published,
// which is one of the ways a dropped connection can turn out.
func (d *drive) uploadOffset(token, id string) (offset int64, open bool) {
	d.t.Helper()
	req, err := http.NewRequestWithContext(d.t.Context(), "HEAD", d.base+"/api/uploads/"+id, nil)
	if err != nil {
		d.t.Fatal(err)
	}
	req.Header.Set("Tus-Resumable", "1.0.0")
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err := d.client.Do(req)
	if err != nil {
		d.t.Fatalf("reading the upload offset: %v\n%s", err, d.out)
	}
	resp.Body.Close()
	if resp.StatusCode == http.StatusNotFound {
		return 0, false
	}
	if resp.StatusCode != http.StatusOK {
		d.t.Fatalf("reading the upload offset: %s", resp.Status)
	}
	offset, err = strconv.ParseInt(resp.Header.Get("Upload-Offset"), 10, 64)
	if err != nil {
		d.t.Fatalf("Upload-Offset %q: %v", resp.Header.Get("Upload-Offset"), err)
	}
	return offset, true
}

// through is the same instance addressed through something in front of it.
func (d *drive) through(base string) *drive {
	return &drive{t: d.t, base: base, dataDir: d.dataDir, cmd: d.cmd, out: d.out, client: d.client}
}

// put uploads a small file in one chunk, the way every test here that is not
// about uploading needs a file to exist.
func (d *drive) put(token, dir, name, content string) entry {
	d.t.Helper()
	id := d.createUpload(token, dir, name, int64(len(content)))
	resp := d.patch(token, id, 0, strings.NewReader(content))
	if resp.StatusCode != http.StatusCreated {
		d.t.Fatalf("uploading %s: %s %s", name, resp.Status, body(d.t, resp))
	}
	return unmarshal[entry](d.t, resp)
}

func (d *drive) download(token, path string) string {
	d.t.Helper()
	resp := d.api("GET", "/api/download/"+path, token, nil)
	if resp.StatusCode != http.StatusOK {
		d.t.Fatalf("downloading %s: %s %s", path, resp.Status, body(d.t, resp))
	}
	return body(d.t, resp)
}

// scans is how many reconciliation passes have completed since startup.
func (d *drive) scans(token string) int {
	d.t.Helper()
	resp := d.api("GET", "/api/scan", token, nil)
	if resp.StatusCode != http.StatusOK {
		d.t.Fatalf("scan status: %s %s", resp.Status, body(d.t, resp))
	}
	return unmarshal[struct {
		Scans int `json:"scans"`
	}](d.t, resp).Scans
}

// waitForScan returns once a scan that started after this call has finished, so
// a change made on disk beforehand has certainly been seen.
func (d *drive) waitForScan(token string) {
	d.t.Helper()
	want := d.scans(token) + 2
	deadline := time.Now().Add(time.Minute)
	for d.scans(token) < want {
		if time.Now().After(deadline) {
			d.t.Fatalf("no scan completed within a minute:\n%s", d.out)
		}
		time.Sleep(100 * time.Millisecond)
	}
}

func (d *drive) userDir(username string) string {
	return filepath.Join(d.dataDir, "users", username)
}

// rss is the process's resident set size, which is the number an operator
// watches while a large upload runs. Go's own MemStats would not answer the
// question for a different process, and this test is about a different process.
func (d *drive) rss() int64 {
	d.t.Helper()
	b, err := os.ReadFile(fmt.Sprintf("/proc/%d/statm", d.cmd.Process.Pid))
	if err != nil {
		d.t.Skipf("resident memory is read from /proc, which this machine does not have: %v", err)
	}
	fields := strings.Fields(string(b))
	if len(fields) < 2 {
		d.t.Fatalf("unreadable statm: %q", b)
	}
	pages, err := strconv.ParseInt(fields[1], 10, 64)
	if err != nil {
		d.t.Fatal(err)
	}
	return pages * int64(os.Getpagesize())
}

// filler is an endless cheap source of bytes, so a four-gigabyte upload costs
// the test nothing to produce and never touches the disk on this side.
type filler struct {
	block []byte
	at    int
}

func newFiller() *filler { return &filler{block: bytes.Repeat([]byte("drive-acceptance"), 4096)} }

func (f *filler) Read(p []byte) (int, error) {
	n := copy(p, f.block[f.at:])
	f.at = (f.at + n) % len(f.block)
	return n, nil
}

// 15.1: the body goes from the connection to the file and nothing holds it, so
// resident memory is the same for four gigabytes as for four kilobytes.
//
// Off by default: it writes 4 GB and takes minutes. Run it with
// DRIVE_BIG_UPLOAD_TEST=1 go test ./cmd/drive -run FourGigabyte.
func TestFourGigabyteUploadDoesNotGrowResidentMemory(t *testing.T) {
	if os.Getenv("DRIVE_BIG_UPLOAD_TEST") == "" {
		t.Skip("set DRIVE_BIG_UPLOAD_TEST=1 to stream 4 GB through a real instance")
	}
	d, token := newDrive(t)

	const (
		total = 4 << 30
		chunk = 256 << 20
	)
	id := d.createUpload(token, "", "big.bin", total)

	// Sampled on a ticker rather than between chunks: a buffer that fills and is
	// released inside one request is exactly what this has to catch.
	baseline := d.rss()
	var peak atomic.Int64
	peak.Store(baseline)
	done := make(chan struct{})
	go func() {
		for {
			select {
			case <-done:
				return
			case <-time.After(200 * time.Millisecond):
			}
			if v := d.rss(); v > peak.Load() {
				peak.Store(v)
			}
		}
	}()

	src := newFiller()
	for at := int64(0); at < total; at += chunk {
		want := http.StatusNoContent
		if at+chunk >= total {
			want = http.StatusCreated
		}
		resp := d.patch(token, id, at, io.LimitReader(src, chunk))
		if resp.StatusCode != want {
			close(done)
			t.Fatalf("chunk at %d: %s %s", at, resp.Status, body(t, resp))
		}
		resp.Body.Close()
	}
	close(done)

	// Flat, with room for the runtime's own noise: buffering one chunk would
	// show 256 MiB here and buffering the file would show four gigabytes.
	const ceiling = 64 << 20
	t.Logf("resident memory over a %d-byte upload: %d at rest, %d at peak", int64(total), baseline, peak.Load())
	if grown := peak.Load() - baseline; grown > ceiling {
		t.Errorf("resident memory went from %d to %d during a %d-byte upload: it is being buffered",
			baseline, peak.Load(), int64(total))
	}
	st, err := os.Stat(filepath.Join(d.userDir("ada"), "big.bin"))
	if err != nil || st.Size() != total {
		t.Fatalf("stored file: %v %v, want %d bytes", st, err, int64(total))
	}
}

var (
	traceFsync  = regexp.MustCompile(`f(data)?sync\(\d+<([^>]*)>`)
	traceRename = regexp.MustCompile(`rename(at2?)?\(`)
)

// 15.2: durability is an ordering claim about syscalls, so it is checked
// against the syscalls. fsync on the temp file, then the rename, then fsync on
// the directory that now contains the name — the last one being the step
// everybody forgets and the one that makes the rename survive power loss.
//
// Skipped where strace is not installed rather than quietly passing: this
// asserts on kernel-level evidence and there is no substitute for it.
func TestUploadFsyncsBeforeAndAfterTheRename(t *testing.T) {
	tracer, err := exec.LookPath("strace")
	if err != nil {
		t.Skip("strace is not installed, and there is no honest way to check syscall order without it")
	}
	trace := filepath.Join(t.TempDir(), "trace")
	d := launch(t, t.TempDir(), []string{tracer, "-f", "-y", "-o", trace,
		"-e", "trace=fsync,fdatasync,rename,renameat,renameat2"})
	token := d.signUp("ada")
	d.put(token, "", "report.txt", "durable enough to survive the power going out\n")
	d.stop() // strace flushes its output when it exits

	raw, err := os.ReadFile(trace)
	if err != nil {
		t.Fatalf("reading the trace: %v", err)
	}
	lines := strings.Split(string(raw), "\n")

	published := -1
	for i, line := range lines {
		if traceRename.MatchString(line) && strings.Contains(line, `"report.txt"`) {
			published = i
			break
		}
	}
	if published < 0 {
		t.Fatalf("no rename published report.txt:\n%s", raw)
	}

	// The temp file is fsynced before it is renamed into place.
	synced := false
	for _, line := range lines[:published] {
		if m := traceFsync.FindStringSubmatch(line); m != nil && strings.Contains(m[2], "/.drive/tmp/") {
			synced = true
		}
	}
	if !synced {
		t.Errorf("nothing fsynced the temp file before the rename:\n%s", raw)
	}

	// And the directory the file now lives in is fsynced after it.
	root := d.userDir("ada")
	synced = false
	for _, line := range lines[published+1:] {
		if m := traceFsync.FindStringSubmatch(line); m != nil && strings.TrimRight(m[2], "/") == root {
			synced = true
		}
	}
	if !synced {
		t.Errorf("the parent directory %s was never fsynced after the rename:\n%s", root, raw)
	}
}

// 15.3: the reconciler has no delete path. A file added, moved and removed
// outside the application is adopted, followed and marked missing respectively,
// and in all of it nothing on disk and no row in the index is destroyed.
func TestTheReconcilerDestroysNothingOverAFullExternalCycle(t *testing.T) {
	d, token := newDrive(t, "DRIVE_SCAN_INTERVAL=1s")
	for _, name := range []string{"keep.txt", "moved.txt", "removed.txt"} {
		d.put(token, "notes", name, name+" content\n")
	}
	before := map[string]int64{}
	for _, e := range d.list(token, "notes") {
		before[e.Name] = e.ID
	}
	if len(before) != 3 {
		t.Fatalf("set-up listing is %v, want three files", before)
	}

	// The external cycle: a file manager, an rsync, a shell.
	notes := filepath.Join(d.userDir("ada"), "notes")
	if err := os.WriteFile(filepath.Join(notes, "added.txt"), []byte("arrived from outside\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(filepath.Join(notes, "moved.txt"), filepath.Join(notes, "renamed.txt")); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(notes, "removed.txt")); err != nil {
		t.Fatal(err)
	}
	onDisk := tree(t, d.userDir("ada"))
	d.waitForScan(token)

	after := map[string]int64{}
	for _, e := range d.list(token, "notes") {
		after[e.Name] = e.ID
	}
	if _, ok := after["added.txt"]; !ok {
		t.Errorf("a file added outside the application was not adopted: %v", after)
	}
	if after["renamed.txt"] != before["moved.txt"] {
		t.Errorf("an external rename gave the file a new identity: %d, was %d", after["renamed.txt"], before["moved.txt"])
	}
	if _, ok := after["removed.txt"]; ok {
		t.Error("a file removed outside the application is still listed")
	}

	// The scan touched no byte of anything it did not put there.
	if now := tree(t, d.userDir("ada")); !sameTree(onDisk, now) {
		t.Errorf("the reconciler changed the filesystem:\nbefore %v\nafter  %v", onDisk, now)
	}

	// Nothing else can be read out of the running process, so read the index:
	// a delete would be a row that is gone, not a row that says missing.
	d.stop()
	db := openIndex(t, d.dataDir)
	for name, id := range before {
		var state string
		if err := db.QueryRow(`SELECT state FROM files WHERE id = ?`, id).Scan(&state); err != nil {
			t.Errorf("%s (id %d) was deleted from the index: %v", name, id, err)
			continue
		}
		want := "present"
		if name == "removed.txt" {
			want = "missing"
		}
		if state != want {
			t.Errorf("%s is %q after the scan, want %q", name, state, want)
		}
	}
}

// tree is every path under root with its size, which is enough to tell whether
// anything was deleted, truncated or rewritten.
func tree(t *testing.T, root string) map[string]int64 {
	t.Helper()
	out := map[string]int64{}
	err := filepath.Walk(root, func(p string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(root, p)
		if err != nil {
			return err
		}
		if info.IsDir() {
			out[rel] = -1
			return nil
		}
		out[rel] = info.Size()
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return out
}

func sameTree(a, b map[string]int64) bool {
	if len(a) != len(b) {
		return false
	}
	for p, size := range a {
		if other, ok := b[p]; !ok || other != size {
			return false
		}
	}
	return true
}

// 15.4: the index is disposable. Deleting it on a populated instance loses the
// accounts and nothing else — re-create the account with the same username and
// the storage root, and everything in it, comes back.
func TestDeletingTheIndexLosesAccountsAndNoFiles(t *testing.T) {
	dataDir := t.TempDir()
	d := launch(t, dataDir, nil)
	token := d.signUp("ada")
	const content = "figures for the quarter\n"
	d.put(token, "reports", "quarterly.txt", content)
	d.put(token, "", "notes.txt", "top level\n")
	before := tree(t, filepath.Join(dataDir, "users", "ada"))
	d.stop()

	// The operator's disaster: the index is gone, the files are not.
	for _, suffix := range []string{"", "-wal", "-shm"} {
		if err := os.Remove(filepath.Join(dataDir, "index.db"+suffix)); err != nil && !os.IsNotExist(err) {
			t.Fatal(err)
		}
	}

	// The account is re-created after this instance started, so its root is
	// adopted by the next scan rather than the startup one. A short interval is
	// waiting less, not behaving differently.
	restarted := launch(t, dataDir, nil, "DRIVE_SCAN_INTERVAL=1s")
	token = restarted.signUp("ada") // same username, so the same storage root
	restarted.waitForScan(token)

	if got := restarted.download(token, "reports/quarterly.txt"); got != content {
		t.Errorf("after the index was deleted, quarterly.txt downloads as %q, want %q", got, content)
	}
	names := []string{}
	for _, e := range restarted.list(token, "reports") {
		names = append(names, e.Name)
	}
	if len(names) != 1 || names[0] != "quarterly.txt" {
		t.Errorf("the rebuilt listing of reports is %v", names)
	}
	resp := restarted.api("GET", "/api/search?q=quarter", token, nil)
	found := unmarshal[listing](t, resp)
	if len(found.Entries) != 1 || found.Entries[0].Path != "reports/quarterly.txt" {
		t.Errorf("searching the rebuilt index found %v", found.Entries)
	}
	if now := tree(t, filepath.Join(dataDir, "users", "ada")); !sameTree(before, now) {
		t.Errorf("rebuilding changed the filesystem:\nbefore %v\nafter  %v", before, now)
	}
}
