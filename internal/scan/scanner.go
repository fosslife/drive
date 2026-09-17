package scan

import (
	"context"
	"fmt"
	"log/slog"
	"path/filepath"
	"sync"
	"time"

	"github.com/fosslife/drive/internal/index"
	"github.com/fosslife/drive/internal/storage"
)

// Scanner runs reconciliation over every storage root: once at startup, then on
// an interval, then whenever something asks. It owns a goroutine and nothing
// else; requests are served the whole time it is working, because a full
// rebuild of a large root takes minutes and a server that looks hung for that
// long reads as broken.
type Scanner struct {
	db       *index.DB
	dataDir  string
	interval time.Duration
	trigger  chan struct{}

	mu     sync.Mutex
	status Status
}

// Status is what the scanner will admit to. It is safe to read at any time and
// exists so listings can say "this is what I have so far" rather than implying
// the index is complete.
type Status struct {
	Running  bool      `json:"running"`
	Root     string    `json:"root,omitempty"` // storage root being scanned
	Seen     int       `json:"seen"`           // entries walked in the current or last scan
	Started  time.Time `json:"started,omitzero"`
	Finished time.Time `json:"finished,omitzero"`
	Scans    int       `json:"scans"` // completed scans since startup
	Error    string    `json:"error,omitempty"`
}

// Indexing reports that the index may be incomplete: either a scan is running
// or none has finished yet. Callers that return file listings should say so.
func (s Status) Indexing() bool { return s.Running || s.Scans == 0 }

func NewScanner(db *index.DB, dataDir string, interval time.Duration) *Scanner {
	return &Scanner{
		db:       db,
		dataDir:  dataDir,
		interval: interval,
		trigger:  make(chan struct{}, 1),
	}
}

// Status reports what the scanner is doing.
func (s *Scanner) Status() Status {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.status
}

// Trigger asks for a scan. It never blocks and never queues more than one:
// a second request while one is already pending is the same request.
func (s *Scanner) Trigger() {
	select {
	case s.trigger <- struct{}{}:
	default:
	}
}

// Run scans immediately, then on the interval, then on demand, until ctx is
// cancelled. Start it with `go`: it must not gate startup or request serving.
func (s *Scanner) Run(ctx context.Context) {
	ticker := time.NewTicker(s.interval)
	defer ticker.Stop()
	for {
		if err := s.ScanAll(ctx); err != nil && ctx.Err() == nil {
			// Reported, not fatal. A root that cannot be read this minute may
			// be back the next, and the index still describes what we last saw.
			slog.Error("scan failed", "error", err)
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		case <-s.trigger:
		}
	}
}

// ScanAll reconciles every user's storage root. A root that cannot be read
// fails that root alone, leaving its index entries exactly as they were: one
// unmounted disk must not stop the other accounts from being reconciled.
func (s *Scanner) ScanAll(ctx context.Context) error {
	users, err := s.users()
	if err != nil {
		return err
	}

	s.begin()
	var failed error
	for _, u := range users {
		if ctx.Err() != nil {
			break
		}
		if err := s.scanOne(ctx, u); err != nil {
			failed = err
			slog.Error("scanning storage root", "root", u.root, "error", err)
		}
	}
	s.finish(failed)
	return failed
}

type user struct {
	id   int64
	root string // relative to the data directory, e.g. users/ada
}

func (s *Scanner) users() ([]user, error) {
	rs, err := s.db.Query(`SELECT id, storage_root FROM users WHERE disabled = 0 ORDER BY id`)
	if err != nil {
		return nil, fmt.Errorf("listing accounts to scan: %w", err)
	}
	defer rs.Close()

	var users []user
	for rs.Next() {
		var u user
		if err := rs.Scan(&u.id, &u.root); err != nil {
			return nil, fmt.Errorf("listing accounts to scan: %w", err)
		}
		users = append(users, u)
	}
	return users, rs.Err()
}

func (s *Scanner) scanOne(ctx context.Context, u user) error {
	// minFree is zero because scanning writes nothing to the root. The guard
	// belongs on the paths that do.
	root, err := storage.Open(filepath.Join(s.dataDir, filepath.FromSlash(u.root)), 0)
	if err != nil {
		return err
	}
	defer root.Close()

	s.setRoot(u.root)
	res, err := Scan(s.db, u.id, root, s.setSeen)
	if err != nil {
		return err
	}
	if res.Added+res.Updated+res.Moved+res.Missing > 0 {
		slog.Info("reconciled storage root", "root", u.root,
			"files", res.Files, "dirs", res.Dirs, "hashed", res.Hashed,
			"added", res.Added, "updated", res.Updated, "moved", res.Moved, "missing", res.Missing)
	}
	return ctx.Err()
}

func (s *Scanner) begin() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.status.Running = true
	s.status.Started = time.Now()
	s.status.Seen = 0
	s.status.Error = ""
}

func (s *Scanner) finish(err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.status.Running = false
	s.status.Root = ""
	s.status.Finished = time.Now()
	s.status.Scans++
	if err != nil {
		s.status.Error = err.Error()
	}
}

func (s *Scanner) setRoot(root string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.status.Root = root
}

func (s *Scanner) setSeen(files int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.status.Seen = files
}
