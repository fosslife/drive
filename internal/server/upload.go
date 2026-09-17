package server

import (
	"encoding/base64"
	"errors"
	"net/http"
	"strconv"
	"strings"

	"github.com/fosslife/drive/internal/files"
	"github.com/fosslife/drive/internal/storage"
)

// tus 1.0, implemented directly rather than through tusd: it is three handlers
// over the temp-file pipeline atomic writes already need, and the library
// brings a storage abstraction that fights the .drive layout.
const tusVersion = "1.0.0"

// tusHeaders are required on every tus response, including the error ones.
func tusHeaders(w http.ResponseWriter) {
	w.Header().Set("Tus-Resumable", tusVersion)
}

// tusVersionOK refuses a client speaking a protocol version this does not
// implement, which the spec requires rather than guessing.
func tusVersionOK(w http.ResponseWriter, r *http.Request) bool {
	if got := r.Header.Get("Tus-Resumable"); got != tusVersion {
		writeError(w, http.StatusPreconditionFailed, "this server speaks tus "+tusVersion+", not "+strconv.Quote(got))
		return false
	}
	return true
}

func (s *Server) uploadOptions(w http.ResponseWriter, r *http.Request) {
	h := w.Header()
	h.Set("Tus-Resumable", tusVersion)
	h.Set("Tus-Version", tusVersion)
	h.Set("Tus-Extension", "creation,termination")
	w.WriteHeader(http.StatusNoContent)
}

// parseMetadata reads the tus Upload-Metadata header: comma-separated
// `key base64value` pairs, where a key with no value is a flag.
func parseMetadata(header string) map[string]string {
	out := map[string]string{}
	for _, pair := range strings.Split(header, ",") {
		key, value, _ := strings.Cut(strings.TrimSpace(pair), " ")
		if key == "" {
			continue
		}
		decoded, err := base64.StdEncoding.DecodeString(strings.TrimSpace(value))
		if err != nil {
			decoded = nil
		}
		out[key] = string(decoded)
	}
	return out
}

func (s *Server) createUpload(w http.ResponseWriter, r *http.Request) {
	tusHeaders(w)
	if !tusVersionOK(w, r) {
		return
	}
	size, err := strconv.ParseInt(r.Header.Get("Upload-Length"), 10, 64)
	if err != nil || size < 0 {
		writeError(w, http.StatusBadRequest, "Upload-Length must be the byte count of the file")
		return
	}
	meta := parseMetadata(r.Header.Get("Upload-Metadata"))

	root, err := s.rootFor(r)
	if err != nil {
		fileError(w, err)
		return
	}
	defer root.Close()

	_, replace := meta["replace"]
	u, err := files.NewUpload(s.db, userFrom(r.Context()).ID, root,
		meta["dir"], meta["filename"], size, replace)
	if err != nil {
		fileError(w, err)
		return
	}

	w.Header().Set("Location", "/api/uploads/"+u.ID)
	w.Header().Set("Upload-Offset", "0")
	w.WriteHeader(http.StatusCreated)
}

// upload resolves the upload named in the path, scoped to its owner.
func (s *Server) upload(w http.ResponseWriter, r *http.Request) (files.Upload, *storage.Root, bool) {
	root, err := s.rootFor(r)
	if err != nil {
		fileError(w, err)
		return files.Upload{}, nil, false
	}
	u, err := files.GetUpload(s.db, userFrom(r.Context()).ID, root, r.PathValue("id"))
	if err != nil {
		root.Close()
		fileError(w, err)
		return files.Upload{}, nil, false
	}
	return u, root, true
}

// uploadStatus is how a client that was interrupted finds out where to carry on.
func (s *Server) uploadStatus(w http.ResponseWriter, r *http.Request) {
	tusHeaders(w)
	if !tusVersionOK(w, r) {
		return
	}
	u, root, ok := s.upload(w, r)
	if !ok {
		return
	}
	defer root.Close()

	h := w.Header()
	h.Set("Upload-Offset", strconv.FormatInt(u.Offset, 10))
	h.Set("Upload-Length", strconv.FormatInt(u.Size, 10))
	// An offset read from a cache is worse than no offset at all.
	h.Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusOK)
}

// uploadChunk appends to an upload and, on the last byte, publishes it.
//
// The body goes straight from the connection to the file. Nothing here holds
// more than a copy buffer, so a 4 GB upload costs what a 4 KB one does.
func (s *Server) uploadChunk(w http.ResponseWriter, r *http.Request) {
	tusHeaders(w)
	if !tusVersionOK(w, r) {
		return
	}
	if ct := r.Header.Get("Content-Type"); ct != "application/offset+octet-stream" {
		writeError(w, http.StatusUnsupportedMediaType, "Content-Type must be application/offset+octet-stream")
		return
	}
	offset, err := strconv.ParseInt(r.Header.Get("Upload-Offset"), 10, 64)
	if err != nil || offset < 0 {
		writeError(w, http.StatusBadRequest, "Upload-Offset must be the byte the chunk starts at")
		return
	}

	u, root, ok := s.upload(w, r)
	if !ok {
		return
	}
	defer root.Close()

	u, err = files.Append(s.db, root, u, offset, r.Body)
	if errors.Is(err, files.ErrOffsetConflict) {
		w.Header().Set("Upload-Offset", strconv.FormatInt(u.Offset, 10))
		writeError(w, http.StatusConflict, err.Error())
		return
	}
	if err != nil {
		// Whatever arrived is on disk and the offset below is honest, so the
		// client can simply resume. A dropped connection is not a failure.
		w.Header().Set("Upload-Offset", strconv.FormatInt(u.Offset, 10))
		fileError(w, err)
		return
	}

	w.Header().Set("Upload-Offset", strconv.FormatInt(u.Offset, 10))
	if u.Offset < u.Size {
		w.WriteHeader(http.StatusNoContent)
		return
	}

	e, err := files.Finish(s.db, root, u)
	if err != nil {
		fileError(w, err)
		return
	}
	// The stored entry, which may be under a different name than was asked for
	// if something was already there and replacement was not requested.
	writeJSON(w, http.StatusCreated, e)
}

// deleteUpload abandons an upload. The destination was never written, so there
// is nothing at it to undo and nothing of an existing file to repair.
func (s *Server) deleteUpload(w http.ResponseWriter, r *http.Request) {
	tusHeaders(w)
	if !tusVersionOK(w, r) {
		return
	}
	u, root, ok := s.upload(w, r)
	if !ok {
		return
	}
	defer root.Close()

	if err := files.Abort(s.db, root, u); err != nil {
		fileError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
