package thumb

import (
	"database/sql"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"runtime"
	"time"

	"github.com/fosslife/drive/internal/files"
	"github.com/fosslife/drive/internal/index"
	"github.com/fosslife/drive/internal/storage"
)

// Cache serves thumbnails, making them when it has to.
//
// Generation is bounded: a folder of a thousand new images turns into a
// thousand requests, and decoding a thousand photographs at once would take the
// machine down. Listings never come through here at all, which is the other
// half of "a listing never waits for a picture".
type Cache struct {
	// workers is a counting semaphore, not a pool of goroutines: the work
	// happens on the request's own goroutine, so a client that goes away stops
	// costing anything the moment it does.
	workers chan struct{}
}

// DefaultWorkers keeps decoding off every core at once: a browser opening a
// folder should not be able to starve the requests that serve files.
var DefaultWorkers = max(runtime.NumCPU()/2, 1)

func NewCache(workers int) *Cache {
	if workers <= 0 {
		workers = DefaultWorkers
	}
	return &Cache{workers: make(chan struct{}, workers)}
}

// Open returns a thumbnail for an entry, from the cache when it can and by
// making one when it cannot. The caller closes it.
//
// ErrUnsupported means there is no picture to be had — a video, an archive, a
// corrupt JPEG. It is recorded so the next request is a database read rather
// than another failed decode, and it is never an error about the file itself:
// the file is still listed, downloaded, and shared.
func (c *Cache) Open(db *index.DB, root *storage.Root, e files.Entry) (*os.File, error) {
	if e.Kind != "file" {
		return nil, fmt.Errorf("%w: %q is a folder", ErrUnsupported, e.Name)
	}
	state, version := record(db, e.ID)
	fresh := version == e.ETag

	// A failure is remembered against the version that failed. A new version of
	// the same file gets another try; the same bytes do not.
	if fresh && state == "failed" {
		return nil, fmt.Errorf("%w: %q could not be read as an image", ErrUnsupported, e.Name)
	}
	if fresh {
		f, err := root.OpenThumb(e.ID)
		if err == nil {
			return f, nil
		}
		if !errors.Is(err, fs.ErrNotExist) {
			return nil, err
		}
		// Recorded as ready but not on disk: the cache has been deleted, which
		// is a supported thing to do to derived data. Make it again.
	}
	return c.generate(db, root, e)
}

func (c *Cache) generate(db *index.DB, root *storage.Root, e files.Entry) (*os.File, error) {
	c.workers <- struct{}{}
	defer func() { <-c.workers }()

	src, err := root.Open(e.Path)
	if err != nil {
		return nil, err
	}
	defer src.Close()

	// ponytail: two requests for the same missing thumbnail both draw it. The
	// second write is an atomic rename over the first, so the cost is duplicated
	// work and never a torn file. Deduplicate with a keyed mutex if a gallery of
	// cold images ever shows up in a profile.
	data, err := Generate(src)
	if err != nil {
		remember(db, e.ID, e.ETag, "failed")
		return nil, err
	}
	if err := root.WriteThumb(e.ID, data); err != nil {
		return nil, err
	}
	remember(db, e.ID, e.ETag, "ready")
	return root.OpenThumb(e.ID)
}

// record returns what is known about an entry's thumbnail.
func record(db *index.DB, fileID int64) (state, version string) {
	err := db.QueryRow(`SELECT state, version FROM thumbs WHERE file_id = ?`, fileID).Scan(&state, &version)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		// Not worth failing a picture over: a lost row costs one regeneration.
		return "", ""
	}
	return state, version
}

func remember(db *index.DB, fileID int64, version, state string) {
	db.Exec(`INSERT INTO thumbs (file_id, version, state, updated_at) VALUES (?, ?, ?, ?)
	         ON CONFLICT(file_id) DO UPDATE SET version = excluded.version,
	                                            state = excluded.state,
	                                            updated_at = excluded.updated_at`,
		fileID, version, state, time.Now().Unix())
}
