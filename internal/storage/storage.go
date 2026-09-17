// Package storage owns the bytes on disk.
//
// Every user file is an ordinary file at the path the user sees. Nothing here
// may depend on the index: the index is rebuilt from what this package stores,
// never the other way round.
package storage

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"strings"
	"syscall"
	"time"
)

// ErrNoSpace means the write was refused before it started, to keep the volume
// from filling. Reads, listings and deletes are unaffected by design.
var ErrNoSpace = errors.New("insufficient free space")

// ErrExists means the destination was occupied and the operation refused to
// replace it. Replacing is a caller's explicit decision, and it goes via Trash.
var ErrExists = errors.New("already exists")

// Root is one user's storage root.
type Root struct {
	root    *os.Root
	dir     string
	minFree int64
}

// Info describes a file's content as stored.
type Info struct {
	Size     int64
	Checksum string // hex SHA-256
	ModTime  time.Time
}

// Open opens dir as a storage root, creating it and the .drive layout if they
// do not exist yet. minFree is the number of bytes of headroom to keep free on
// the volume; writes are refused below it.
func Open(dir string, minFree int64) (*Root, error) {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, fmt.Errorf("creating storage root %s: %w", dir, err)
	}
	root, err := os.OpenRoot(dir)
	if err != nil {
		return nil, fmt.Errorf("opening storage root %s: %w", dir, err)
	}
	for _, sub := range []string{Internal, Internal + "/tmp", Internal + "/trash", Internal + "/thumbs"} {
		if err := root.MkdirAll(sub, 0o700); err != nil {
			root.Close()
			return nil, fmt.Errorf("creating %s in %s: %w", sub, dir, err)
		}
	}
	return &Root{root: root, dir: dir, minFree: minFree}, nil
}

func (r *Root) Dir() string  { return r.dir }
func (r *Root) Close() error { return r.root.Close() }

// Write streams src to rel: temp file, fsync the file, rename into place,
// fsync the parent directory. rel does not exist until its content is
// complete and durable, so an interrupted write cannot be observed.
//
// size is the expected byte count for the space guard, or -1 when unknown.
//
// The final rename never follows a symlink already at rel: the link is
// replaced by the new file. That is deliberate, and it is what stops a write
// from reaching a target outside the root.
func (r *Root) Write(rel string, src io.Reader, size int64) (Info, error) {
	name, err := relPath(rel)
	if err != nil {
		return Info{}, err
	}
	if err := r.checkSpace(size); err != nil {
		return Info{}, err
	}

	if dir := path.Dir(name); dir != "." {
		if err := r.root.MkdirAll(dir, 0o700); err != nil {
			return Info{}, fmt.Errorf("creating %s: %w", dir, err)
		}
	}

	tmp, f, err := r.createTemp()
	if err != nil {
		return Info{}, err
	}
	committed := false
	defer func() {
		f.Close()
		if !committed {
			r.root.Remove(tmp) // never leave temp data behind on a failed write
		}
	}()

	sum := sha256.New()
	written, err := io.Copy(f, io.TeeReader(src, sum))
	if err != nil {
		return Info{}, fmt.Errorf("writing %s: %w", rel, err)
	}
	if err := f.Sync(); err != nil {
		return Info{}, fmt.Errorf("flushing %s: %w", rel, err)
	}
	if err := f.Close(); err != nil {
		return Info{}, fmt.Errorf("closing %s: %w", rel, err)
	}
	if err := r.root.Rename(tmp, name); err != nil {
		return Info{}, fmt.Errorf("publishing %s: %w", rel, err)
	}
	committed = true
	// A rename is atomic but not durable until the containing directory is
	// synced. Without this, "upload succeeded" plus power loss can lose the file.
	if err := r.syncDir(path.Dir(name)); err != nil {
		return Info{}, err
	}

	info := Info{Size: written, Checksum: hex.EncodeToString(sum.Sum(nil))}
	if st, err := r.root.Stat(name); err == nil {
		info.ModTime = st.ModTime()
	}
	return info, nil
}

// Open opens a file for reading. The caller closes it. It is an *os.File so a
// download can seek, which is what makes byte ranges free.
func (r *Root) Open(rel string) (*os.File, error) {
	name, err := relPath(rel)
	if err != nil {
		return nil, err
	}
	return r.root.Open(name)
}

// Stat describes what is at rel without following a symlink at the end of the
// path: a link is not a file we own, and we never serve through one.
func (r *Root) Stat(rel string) (os.FileInfo, error) {
	name, err := relPath(rel)
	if err != nil {
		return nil, err
	}
	return r.root.Lstat(name)
}

// MkdirAll creates rel and any missing parents.
func (r *Root) MkdirAll(rel string) error {
	name, err := relPath(rel)
	if err != nil {
		return err
	}
	if err := r.root.MkdirAll(name, 0o700); err != nil {
		return fmt.Errorf("creating %s: %w", rel, err)
	}
	return r.syncDir(path.Dir(name))
}

// Rename moves from to to, creating any missing parent of to.
//
// It refuses to replace anything already at to. A rename that clobbers destroys
// bytes with no recovery, and only a permanent delete may do that: a caller
// that means to replace moves the existing entry to Trash first.
//
// ponytail: the existence check is a separate syscall from the rename, so a
// file created in between would still be clobbered. Linux has RENAME_NOREPLACE
// for this; os.Root does not expose it. The window is one user racing
// themselves, and the API-level rejection is what the spec asks for.
func (r *Root) Rename(from, to string) error {
	src, err := relPath(from)
	if err != nil {
		return err
	}
	dst, err := relPath(to)
	if err != nil {
		return err
	}
	switch _, err := r.root.Lstat(dst); {
	case err == nil:
		return fmt.Errorf("%w: %q", ErrExists, to)
	case !errors.Is(err, fs.ErrNotExist):
		return err
	}
	if dir := path.Dir(dst); dir != "." {
		if err := r.root.MkdirAll(dir, 0o700); err != nil {
			return fmt.Errorf("creating %s: %w", dir, err)
		}
	}
	if err := r.root.Rename(src, dst); err != nil {
		return fmt.Errorf("moving %s to %s: %w", from, to, err)
	}
	// Both ends: the entry left one directory and joined another, and neither
	// change is durable until its directory is synced.
	if err := r.syncDir(path.Dir(src)); err != nil {
		return err
	}
	return r.syncDir(path.Dir(dst))
}

// Trash moves rel into .drive/trash/<id>/, which the reconciler skips and
// listings exclude. The id namespaces it so two files deleted from different
// folders with the same name do not collide; the original path stays in the
// index as the restore target.
//
// This is a move, not a delete. Nothing here frees a byte.
func (r *Root) Trash(id int64, rel string) error {
	name, err := relPath(rel)
	if err != nil {
		return err
	}
	dir := fmt.Sprintf("%s/trash/%d", Internal, id)
	if err := r.root.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("preparing trash for %s: %w", rel, err)
	}
	if err := r.root.Rename(name, dir+"/"+path.Base(name)); err != nil {
		return fmt.Errorf("trashing %s: %w", rel, err)
	}
	if err := r.syncDir(path.Dir(name)); err != nil {
		return err
	}
	return r.syncDir(dir)
}

// CreateUpload opens an empty file under .drive/tmp to receive a resumable
// upload and returns its name, which the caller records so a later request can
// carry on where this one stopped.
func (r *Root) CreateUpload() (string, error) {
	name, f, err := r.createTemp()
	if err != nil {
		return "", err
	}
	return name, f.Close()
}

// UploadOffset is how many bytes of an upload are actually on disk.
//
// The file is the offset. A column in the index can disagree with the disk
// after a crash, and telling a client "I have n bytes" when the last n-k were
// never fsynced is how a resumed upload finishes with a corrupt file.
func (r *Root) UploadOffset(temp string) (int64, error) {
	name, err := tempPath(temp)
	if err != nil {
		return 0, err
	}
	st, err := r.root.Stat(name)
	if err != nil {
		return 0, err
	}
	return st.Size(), nil
}

// AppendUpload appends src to an upload and returns the new offset, fsynced.
// It writes straight from the request body to the file: nothing here holds more
// than a copy buffer, whatever the size of the upload.
func (r *Root) AppendUpload(temp string, src io.Reader) (int64, error) {
	name, err := tempPath(temp)
	if err != nil {
		return 0, err
	}
	f, err := r.root.OpenFile(name, os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return 0, err
	}
	defer f.Close()

	// A short copy is not an error to recover from: whatever arrived is on
	// disk, and the offset we report afterwards is what the client resumes at.
	_, copyErr := io.Copy(f, src)
	if err := f.Sync(); err != nil {
		return 0, fmt.Errorf("flushing upload: %w", err)
	}
	st, err := f.Stat()
	if err != nil {
		return 0, err
	}
	return st.Size(), copyErr
}

// FinishUpload publishes an upload at rel and returns what was stored. It
// refuses an occupied destination: a caller that means to replace moves the
// existing entry to Trash first.
func (r *Root) FinishUpload(temp, rel string) (Info, error) {
	src, err := tempPath(temp)
	if err != nil {
		return Info{}, err
	}
	name, err := relPath(rel)
	if err != nil {
		return Info{}, err
	}
	switch _, err := r.root.Lstat(name); {
	case err == nil:
		return Info{}, fmt.Errorf("%w: %q", ErrExists, rel)
	case !errors.Is(err, fs.ErrNotExist):
		return Info{}, err
	}
	if dir := path.Dir(name); dir != "." {
		if err := r.root.MkdirAll(dir, 0o700); err != nil {
			return Info{}, fmt.Errorf("creating %s: %w", dir, err)
		}
	}
	if err := r.root.Rename(src, name); err != nil {
		return Info{}, fmt.Errorf("publishing %s: %w", rel, err)
	}
	if err := r.syncDir(path.Dir(name)); err != nil {
		return Info{}, err
	}
	// ponytail: the checksum is computed by reading the finished file back,
	// one extra sequential pass. crypto/sha256 can marshal its state between
	// chunks if that ever costs more than the fsync per chunk already does.
	return r.Checksum(rel)
}

// DiscardUpload removes an upload's temporary data. It is the one removal in
// this package, and it can only reach a file no user has ever seen.
func (r *Root) DiscardUpload(temp string) error {
	name, err := tempPath(temp)
	if err != nil {
		return err
	}
	if err := r.root.Remove(name); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("discarding upload: %w", err)
	}
	return nil
}

// CheckSpace reports whether a write of size bytes would eat into the reserve.
// Upload creation asks before accepting rather than failing 4 GB in.
func (r *Root) CheckSpace(size int64) error { return r.checkSpace(size) }

// Entry is one filesystem entry under a root, at a path relative to it.
type Entry struct {
	Path    string
	IsDir   bool
	Size    int64
	ModTime time.Time
}

// Walk visits every directory and regular file under the root, skipping
// .drive and anything that is neither — symlinks, sockets, devices — since
// those own no bytes we can serve and a symlink is a path we refuse to follow.
//
// A directory that cannot be read aborts the walk with an error. Callers must
// treat that as a failed scan and not as an empty root: concluding "everything
// is gone" from a read error is how sync products delete people's files.
func (r *Root) Walk(fn func(Entry) error) error {
	return fs.WalkDir(r.root.FS(), ".", func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return fmt.Errorf("reading %s: %w", p, err)
		}
		if p == "." {
			return nil
		}
		if p == Internal {
			return fs.SkipDir
		}
		if !d.IsDir() && !d.Type().IsRegular() {
			return nil
		}
		info, err := d.Info()
		if err != nil {
			return fmt.Errorf("reading %s: %w", p, err)
		}
		return fn(Entry{Path: p, IsDir: d.IsDir(), Size: info.Size(), ModTime: info.ModTime()})
	})
}

// Checksum hashes a file's current contents without modifying it. Used when a
// file that arrived outside the application is indexed for the first time, and
// by Verify.
func (r *Root) Checksum(rel string) (Info, error) {
	name, err := relPath(rel)
	if err != nil {
		return Info{}, err
	}
	f, err := r.root.Open(name)
	if err != nil {
		return Info{}, err
	}
	defer f.Close()

	st, err := f.Stat()
	if err != nil {
		return Info{}, err
	}
	if st.IsDir() {
		return Info{}, fmt.Errorf("%w: %q is a directory", ErrInvalidPath, rel)
	}
	sum := sha256.New()
	if _, err := io.Copy(sum, f); err != nil {
		return Info{}, fmt.Errorf("reading %s: %w", rel, err)
	}
	return Info{Size: st.Size(), Checksum: hex.EncodeToString(sum.Sum(nil)), ModTime: st.ModTime()}, nil
}

// Mismatch is one file whose contents no longer match what was recorded.
type Mismatch struct {
	Path string
	Want string
	Got  string // empty when the file could not be read
	Err  error
}

// Verify recomputes checksums for the given paths and reports the ones that no
// longer match. It only reads: nothing is deleted, quarantined, or repaired.
// Reporting is the whole job, because the recorded checksum can be the wrong
// one just as easily as the bytes can be.
//
// ponytail: takes the expected set in memory. Feed it in pages from the index
// if a root ever holds more paths than that comfortably fits.
func (r *Root) Verify(want map[string]string) []Mismatch {
	var bad []Mismatch
	for rel, checksum := range want {
		info, err := r.Checksum(rel)
		switch {
		case err != nil:
			bad = append(bad, Mismatch{Path: rel, Want: checksum, Err: err})
		case info.Checksum != checksum:
			bad = append(bad, Mismatch{Path: rel, Want: checksum, Got: info.Checksum})
		}
	}
	return bad
}

func (r *Root) createTemp() (string, *os.File, error) {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", nil, fmt.Errorf("naming temp file: %w", err)
	}
	name := tempPrefix + hex.EncodeToString(b[:])
	f, err := r.root.OpenFile(name, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return "", nil, fmt.Errorf("creating temp file: %w", err)
	}
	return name, f, nil
}

const tempPrefix = Internal + "/tmp/"

// tempPath rejects anything that is not a name this package handed out. The
// temp name comes back from the index on every resume, and a path that could
// point elsewhere would turn an upload into a write-anywhere primitive.
func tempPath(temp string) (string, error) {
	rest, ok := strings.CutPrefix(temp, tempPrefix)
	if !ok || rest == "" || strings.ContainsAny(rest, "/.") {
		return "", fmt.Errorf("%w: %q is not an upload", ErrInvalidPath, temp)
	}
	return temp, nil
}

func (r *Root) syncDir(dir string) error {
	d, err := r.root.Open(dir)
	if err != nil {
		return fmt.Errorf("opening %s to flush it: %w", dir, err)
	}
	defer d.Close()
	if err := d.Sync(); err != nil {
		return fmt.Errorf("flushing directory %s: %w", dir, err)
	}
	return nil
}

func (r *Root) checkSpace(size int64) error {
	if size < 0 {
		size = 0
	}
	avail, err := freeBytes(r.dir)
	if err != nil {
		return err
	}
	if avail-size < r.minFree {
		return fmt.Errorf("%w: %d bytes free, this write needs %d and %d are reserved",
			ErrNoSpace, avail, size, r.minFree)
	}
	return nil
}

func freeBytes(dir string) (int64, error) {
	var st syscall.Statfs_t
	if err := syscall.Statfs(dir, &st); err != nil {
		return 0, fmt.Errorf("checking free space on %s: %w", dir, err)
	}
	return int64(st.Bavail) * int64(st.Bsize), nil
}
