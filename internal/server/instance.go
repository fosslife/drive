package server

import (
	"net/http"
	"time"

	"github.com/fosslife/drive/internal/scan"
	"github.com/fosslife/drive/internal/storage"
)

// instanceState is what an operator needs to answer "is this thing all right"
// without a shell on the box: what is running, where its data is, how much room
// is left, and what the reconciler last did.
func (s *Server) instanceState(w http.ResponseWriter, r *http.Request) {
	free, total, err := storage.Space(s.instance.DataDir)
	if err != nil {
		// The volume not answering is itself the news, so it is reported rather
		// than turned into a 500 that says nothing.
		writeJSON(w, http.StatusOK, instanceReport{
			Version:    s.instance.Version,
			DataDir:    s.instance.DataDir,
			UptimeSec:  int64(time.Since(s.instance.Started).Seconds()),
			Scan:       s.scanStatus(),
			SpaceError: err.Error(),
		})
		return
	}
	writeJSON(w, http.StatusOK, instanceReport{
		Version:    s.instance.Version,
		DataDir:    s.instance.DataDir,
		UptimeSec:  int64(time.Since(s.instance.Started).Seconds()),
		FreeBytes:  free,
		TotalBytes: total,
		Scan:       s.scanStatus(),
	})
}

type instanceReport struct {
	Version    string      `json:"version"`
	DataDir    string      `json:"data_dir"`
	UptimeSec  int64       `json:"uptime_seconds"`
	FreeBytes  int64       `json:"free_bytes"`
	TotalBytes int64       `json:"total_bytes"`
	Scan       scan.Status `json:"scan"`
	SpaceError string      `json:"space_error,omitempty"`
}

// rescan asks for a pass over every storage root. Asking while one is running
// is the same request, so this is safe to press twice: the scanner queues at
// most one and a scan already in flight is left alone.
func (s *Server) rescan(w http.ResponseWriter, r *http.Request) {
	s.instance.Scan()
	writeJSON(w, http.StatusAccepted, s.scanStatus())
}
