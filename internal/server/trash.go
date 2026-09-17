package server

import (
	"net/http"
	"strconv"

	"github.com/fosslife/drive/internal/files"
	"github.com/fosslife/drive/internal/storage"
)

// deleteFile moves an entry to the trash. It is a DELETE because that is what a
// user is doing; nothing is destroyed, and /api/trash is where it went.
func (s *Server) deleteFile(w http.ResponseWriter, r *http.Request) {
	userID := userFrom(r.Context()).ID
	e, err := files.Lookup(s.db, userID, r.PathValue("path"))
	if err != nil {
		fileError(w, err)
		return
	}
	if err := files.CheckETag(e, r.Header.Get("If-Match")); err != nil {
		fileError(w, err)
		return
	}

	root, err := s.rootFor(r)
	if err != nil {
		fileError(w, err)
		return
	}
	defer root.Close()

	if err := files.Trash(s.db, userID, root, e); err != nil {
		fileError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, e)
}

func (s *Server) listTrash(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	limit, _ := strconv.Atoi(q.Get("limit"))
	entries, next, err := files.ListTrash(s.db, userFrom(r.Context()).ID, q.Get("cursor"), limit)
	if err != nil {
		fileError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, struct {
		Entries []files.TrashEntry `json:"entries"`
		Next    string             `json:"next,omitempty"`
	}{entries, next})
}

// restore returns a deleted item to where it came from. The response carries
// the entry's actual path, which differs from the original when something else
// has taken it in the meantime.
func (s *Server) restore(w http.ResponseWriter, r *http.Request) {
	id, root, ok := s.trashTarget(w, r)
	if !ok {
		return
	}
	defer root.Close()

	e, err := files.Restore(s.db, userFrom(r.Context()).ID, root, id)
	if err != nil {
		fileError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, e)
}

// purge destroys one trashed item. This endpoint is the only way a user can
// lose a byte, so it names an item already in the trash and nothing else.
func (s *Server) purge(w http.ResponseWriter, r *http.Request) {
	id, root, ok := s.trashTarget(w, r)
	if !ok {
		return
	}
	defer root.Close()

	if err := files.Purge(s.db, userFrom(r.Context()).ID, root, id); err != nil {
		fileError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) emptyTrash(w http.ResponseWriter, r *http.Request) {
	root, err := s.rootFor(r)
	if err != nil {
		fileError(w, err)
		return
	}
	defer root.Close()

	n, err := files.EmptyTrash(s.db, userFrom(r.Context()).ID, root)
	if err != nil {
		fileError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]int{"deleted": n})
}

func (s *Server) trashTarget(w http.ResponseWriter, r *http.Request) (int64, *storage.Root, bool) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		writeError(w, http.StatusBadRequest, "that is not a trash item id")
		return 0, nil, false
	}
	root, err := s.rootFor(r)
	if err != nil {
		fileError(w, err)
		return 0, nil, false
	}
	return id, root, true
}
