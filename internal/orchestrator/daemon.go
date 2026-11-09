package orchestrator

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"time"
)

// DaemonHTTP serves the fleet over local HTTP. It is the read/write surface
// for desktop, web, and mobile clients: one protocol, localhost bound.
type DaemonHTTP struct {
	fleet *Fleet
}

// NewDaemonHTTP returns the fleet's HTTP handler.
func NewDaemonHTTP(fleet *Fleet) http.Handler {
	d := &DaemonHTTP{fleet: fleet}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /board", d.board)
	mux.HandleFunc("GET /board/live", d.liveBoard)
	mux.HandleFunc("POST /sessions", d.startSession)
	return mux
}

// liveBoard streams the board as Server-Sent Events: one snapshotted board
// per tick until the client disconnects or the request context ends. It is
// the zero-dependency live channel for the spike; a WebSocket transport can
// replace it in the Tauri phase without changing the payload.
func (d *DaemonHTTP) liveBoard(w http.ResponseWriter, r *http.Request) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming unsupported", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.WriteHeader(http.StatusOK)

	ticker := time.NewTicker(DefaultPollInterval)
	defer ticker.Stop()

	writeBoard := func() {
		payload, err := json.Marshal(d.fleet.Board())
		if err != nil {
			return
		}
		fmt.Fprintf(w, "event: board\ndata: %s\n\n", payload)
		flusher.Flush()
	}

	writeBoard()
	for {
		select {
		case <-r.Context().Done():
			return
		case <-ticker.C:
			writeBoard()
		}
	}
}

func (d *DaemonHTTP) board(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, d.fleet.Board())
}

// startSessionRequest is the JSON body of POST /sessions.
type startSessionRequest struct {
	ID       string `json:"id"`
	Task     string `json:"task"`
	Agent    string `json:"agent"`
	Branch   string `json:"branch"`
	Worktree string `json:"worktree"`
}

func (d *DaemonHTTP) startSession(w http.ResponseWriter, r *http.Request) {
	var req startSessionRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid JSON body", http.StatusBadRequest)
		return
	}
	if req.ID == "" || req.Branch == "" {
		http.Error(w, "id and branch are required", http.StatusBadRequest)
		return
	}

	err := d.fleet.Start(Session{
		ID:       SessionID(req.ID),
		Task:     req.Task,
		Agent:    req.Agent,
		Branch:   req.Branch,
		Worktree: req.Worktree,
	})
	switch {
	case err == nil:
		card := fleetCard(d.fleet, SessionID(req.ID))
		writeJSON(w, http.StatusCreated, card)
	case errors.Is(err, ErrDuplicateSession):
		http.Error(w, "session already registered", http.StatusConflict)
	default:
		http.Error(w, "internal error", http.StatusInternalServerError)
	}
}

// fleetCard snapshots one session as its card; used for the 201 response.
func fleetCard(f *Fleet, id SessionID) SessionCard {
	card := SessionCard{ID: string(id)}
	if s, err := f.Get(id); err == nil {
		card.Task, card.Agent, card.Branch = s.Task, s.Agent, s.Branch
	}
	return card
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
