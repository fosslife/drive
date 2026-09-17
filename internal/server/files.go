package server

import (
	"archive/zip"
	"errors"
	"io"
	"log/slog"
	"mime"
	"net/http"
	"path"
	"strconv"
	"strings"

	"github.com/fosslife/drive/internal/files"
	"github.com/fosslife/drive/internal/storage"
)

// rootFor opens the requesting user's storage root. Every path in every handler
// is resolved through it, which is what confines a request — session or API
// token — to its owner's files.
func (s *Server) rootFor(r *http.Request) (*storage.Root, error) {
	return s.users.Root(userFrom(r.Context()))
}

func fileError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, files.ErrNotFound):
		writeError(w, http.StatusNotFound, err.Error())
	case errors.Is(err, files.ErrExists):
		writeError(w, http.StatusConflict, err.Error())
	case errors.Is(err, files.ErrStale):
		writeError(w, http.StatusPreconditionFailed, err.Error())
	case errors.Is(err, files.ErrInvalid), errors.Is(err, storage.ErrInvalidPath):
		writeError(w, http.StatusBadRequest, err.Error())
	case errors.Is(err, storage.ErrNoSpace):
		writeError(w, http.StatusInsufficientStorage, err.Error())
	default:
		slog.Error("file operation failed", "error", err)
		writeError(w, http.StatusInternalServerError, "the operation failed")
	}
}

func (s *Server) list(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	limit, _ := strconv.Atoi(q.Get("limit"))
	entries, next, err := files.List(s.db, userFrom(r.Context()).ID, q.Get("path"), q.Get("cursor"), limit)
	if err != nil {
		fileError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, struct {
		Path    string        `json:"path"`
		Entries []files.Entry `json:"entries"`
		// Next is the cursor for the following page, absent at the end. The
		// client pages rather than the server holding the folder open.
		Next     string `json:"next,omitempty"`
		Indexing bool   `json:"indexing"`
	}{q.Get("path"), entries, next, s.scanStatus().Indexing()})
}

// search matches on filename across the user's whole root. Like a listing it
// reports whether the index is still being rebuilt: a short result set during a
// scan means "not indexed yet", not "you do not have that file".
func (s *Server) search(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	limit, _ := strconv.Atoi(q.Get("limit"))
	entries, more, err := files.Search(s.db, userFrom(r.Context()).ID, q.Get("q"), limit)
	if err != nil {
		fileError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, struct {
		Query   string        `json:"query"`
		Entries []files.Entry `json:"entries"`
		// Truncated says there were more matches than fit. The client narrows
		// the term rather than paging: a filename search that needs page two is
		// a search that needs a better word.
		Truncated bool `json:"truncated"`
		Indexing  bool `json:"indexing"`
	}{q.Get("q"), entries, more, s.scanStatus().Indexing()})
}

func (s *Server) createFolder(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Path string `json:"path"`
	}
	if !decode(w, r, &in) {
		return
	}
	root, err := s.rootFor(r)
	if err != nil {
		fileError(w, err)
		return
	}
	defer root.Close()

	e, err := files.CreateFolder(s.db, userFrom(r.Context()).ID, root, in.Path)
	if err != nil {
		fileError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, e)
}

// move renames or relocates an entry. If-Match makes the change conditional on
// the caller having seen the current version; without it, last write wins.
func (s *Server) move(w http.ResponseWriter, r *http.Request) {
	var in struct {
		From    string `json:"from"`
		To      string `json:"to"`
		Replace bool   `json:"replace"`
	}
	if !decode(w, r, &in) {
		return
	}
	userID := userFrom(r.Context()).ID

	if want := r.Header.Get("If-Match"); want != "" {
		src, err := files.Lookup(s.db, userID, in.From)
		if err != nil {
			fileError(w, err)
			return
		}
		if err := files.CheckETag(src, want); err != nil {
			fileError(w, err)
			return
		}
	}

	root, err := s.rootFor(r)
	if err != nil {
		fileError(w, err)
		return
	}
	defer root.Close()

	e, err := files.Move(s.db, userID, root, in.From, in.To, in.Replace)
	if err != nil {
		fileError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, e)
}

// inlineTypes is the allowlist from the design: raster images, which browsers
// render without a scripting context.
//
// It is an allowlist of extensions rather than a denylist of dangerous ones
// because the dangerous set is open-ended — SVG is an image that carries
// script, PDF is a document that carries script, and the next one has not been
// invented yet. Anything not on this list is served as an opaque download.
var inlineTypes = map[string]string{
	".jpg":  "image/jpeg",
	".jpeg": "image/jpeg",
	".png":  "image/png",
	".gif":  "image/gif",
	".webp": "image/webp",
}

// contentHeaders decides how a stored file is served. Every answer includes
// nosniff and a policy that denies a served document everything it would need
// to do damage, so even a mistake in the allowlist is not account takeover.
func contentHeaders(h http.Header, name string, forceDownload bool) {
	h.Set("X-Content-Type-Options", "nosniff")
	// 'none' plus sandbox: no script, no plugins, no forms, no same-origin
	// context, whatever the type turns out to be.
	h.Set("Content-Security-Policy", "default-src 'none'; sandbox")

	ctype, inline := inlineTypes[strings.ToLower(path.Ext(name))]
	if !inline || forceDownload {
		// Not "text/plain": a browser renders that, and the point is that
		// stored bytes never render in this origin.
		ctype = "application/octet-stream"
	}
	h.Set("Content-Type", ctype)

	disposition := "attachment"
	if inline && !forceDownload {
		disposition = "inline"
	}
	// FormatMediaType handles quoting and the RFC 2231 encoding a non-ASCII
	// filename needs; an empty return means the name could not be encoded, and
	// a disposition with no filename is still a correct disposition.
	if formatted := mime.FormatMediaType(disposition, map[string]string{"filename": name}); formatted != "" {
		disposition = formatted
	}
	h.Set("Content-Disposition", disposition)
}

func (s *Server) download(w http.ResponseWriter, r *http.Request) {
	e, err := files.Lookup(s.db, userFrom(r.Context()).ID, r.PathValue("path"))
	if err != nil {
		fileError(w, err)
		return
	}
	if e.Kind != "file" {
		writeError(w, http.StatusBadRequest, "that is a folder; use the archive endpoint")
		return
	}

	root, err := s.rootFor(r)
	if err != nil {
		fileError(w, err)
		return
	}
	defer root.Close()

	f, err := root.Open(e.Path)
	if err != nil {
		fileError(w, err)
		return
	}
	defer f.Close()

	contentHeaders(w.Header(), e.Name, r.URL.Query().Has("download"))
	w.Header().Set("ETag", e.ETag)
	// ServeContent is where byte ranges, If-Range and If-None-Match come from.
	// The empty name keeps it from overriding the Content-Type decided above.
	http.ServeContent(w, r, "", e.ModTime, f)
}

// archive streams a zip of one or more selected items, produced as it is sent.
// Nothing is staged: no temporary file, no buffer holding the archive, so a
// folder larger than memory and larger than the free space still downloads.
func (s *Server) archive(w http.ResponseWriter, r *http.Request) {
	selection := r.URL.Query()["path"]
	if len(selection) == 0 {
		writeError(w, http.StatusBadRequest, "no items selected")
		return
	}
	userID := userFrom(r.Context()).ID

	// Resolve everything before a byte of the response is written: once the zip
	// starts there is no status code left to change.
	roots := make([]files.Entry, 0, len(selection))
	for _, p := range selection {
		e, err := files.Lookup(s.db, userID, p)
		if err != nil {
			fileError(w, err)
			return
		}
		roots = append(roots, e)
	}

	root, err := s.rootFor(r)
	if err != nil {
		fileError(w, err)
		return
	}
	defer root.Close()

	name := "drive.zip"
	if len(roots) == 1 {
		name = roots[0].Name + ".zip"
	}
	h := w.Header()
	h.Set("Content-Type", "application/zip")
	h.Set("X-Content-Type-Options", "nosniff")
	h.Set("Content-Security-Policy", "default-src 'none'; sandbox")
	if formatted := mime.FormatMediaType("attachment", map[string]string{"filename": name}); formatted != "" {
		h.Set("Content-Disposition", formatted)
	}

	zw := zip.NewWriter(w)
	for _, e := range roots {
		// Archive paths are relative to each selection's own parent, so a
		// folder arrives as itself rather than as its whole absolute path.
		base := strings.TrimSuffix(e.Path, e.Name)
		err := files.Descendants(s.db, userID, e, func(child files.Entry) error {
			return addToZip(zw, root, child, base)
		})
		if err != nil {
			// The response is already committed. Abandon the stream without
			// closing it cleanly: a truncated zip fails to open, which is a
			// better answer than a complete-looking archive missing files.
			slog.Error("archive failed partway", "path", e.Path, "error", err)
			return
		}
	}
	if err := zw.Close(); err != nil {
		slog.Error("finishing archive", "error", err)
	}
}

func addToZip(zw *zip.Writer, root *storage.Root, e files.Entry, base string) error {
	name := strings.TrimPrefix(e.Path, base)
	if e.Kind == "folder" {
		_, err := zw.CreateHeader(&zip.FileHeader{Name: name + "/", Modified: e.ModTime})
		return err
	}
	// Stored, not deflated: the payload is usually already-compressed media, so
	// compressing burns CPU for nothing and gets in the way of streaming.
	entry, err := zw.CreateHeader(&zip.FileHeader{Name: name, Method: zip.Store, Modified: e.ModTime})
	if err != nil {
		return err
	}
	f, err := root.Open(e.Path)
	if err != nil {
		return err
	}
	defer f.Close()
	_, err = io.Copy(entry, f)
	return err
}
