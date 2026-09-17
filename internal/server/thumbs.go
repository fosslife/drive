package server

import (
	"errors"
	"net/http"

	"github.com/fosslife/drive/internal/files"
	"github.com/fosslife/drive/internal/storage"
	"github.com/fosslife/drive/internal/thumb"
)

// thumbnail serves a preview of one of the caller's own files.
func (s *Server) thumbnail(w http.ResponseWriter, r *http.Request) {
	e, err := files.Lookup(s.db, userFrom(r.Context()).ID, r.PathValue("path"))
	if err != nil {
		fileError(w, err)
		return
	}
	root, err := s.rootFor(r)
	if err != nil {
		fileError(w, err)
		return
	}
	defer root.Close()

	s.serveThumb(w, r, root, e)
}

// shareThumb serves a preview through a share link. It resolves the same way
// the share's listing and download do, so a thumbnail is reachable exactly when
// the file behind it is — including not at all, once the link is revoked.
func (s *Server) shareThumb(w http.ResponseWriter, r *http.Request) {
	a, ok := s.visitor(w, r)
	if !ok {
		return
	}
	e, err := a.Entry(s.db, r.PathValue("path"))
	if err != nil {
		shareError(w, err)
		return
	}
	root, err := s.rootOf(a.UserID)
	if err != nil {
		shareError(w, err)
		return
	}
	defer root.Close()

	s.serveThumb(w, r, root, e)
}

func (s *Server) serveThumb(w http.ResponseWriter, r *http.Request, root *storage.Root, e files.Entry) {
	f, err := s.thumbs.Open(s.db, root, e)
	if errors.Is(err, thumb.ErrUnsupported) {
		// Not an error about the file, which is still listed, downloadable and
		// shareable. It is the answer "there is no picture of this".
		writeError(w, http.StatusUnsupportedMediaType, err.Error())
		return
	}
	if err != nil {
		fileError(w, err)
		return
	}
	defer f.Close()

	h := w.Header()
	// This JPEG was produced here from decoded pixels, so unlike stored content
	// it is inline-able: there is nothing of the original file left in it.
	h.Set("Content-Type", "image/jpeg")
	h.Set("Content-Disposition", "inline")
	h.Set("X-Content-Type-Options", "nosniff")
	h.Set("Content-Security-Policy", "default-src 'none'; sandbox")
	// The source's version tags the thumbnail: a changed file is a changed
	// thumbnail, which is exactly when a client should stop using its copy.
	h.Set("ETag", e.ETag)
	h.Set("Cache-Control", "private, max-age=3600")
	http.ServeContent(w, r, "", e.ModTime, f)
}
