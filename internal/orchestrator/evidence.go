package orchestrator

import (
	"net/http"
	"time"
)

// Evidence is the per-worker audit package (PRD F4): what the session did,
// in facts. The UI shows it; RDD receipts join it in a later sprint.
type Evidence struct {
	SessionID string         `json:"sessionId"`
	Task      string         `json:"task"`
	Agent     string         `json:"agent"`
	Branch    string         `json:"branch"`
	Worktree  string         `json:"worktree,omitempty"`
	Status    string         `json:"status"`
	StartedAt *time.Time     `json:"startedAt,omitempty"`
	UpdatedAt *time.Time     `json:"updatedAt,omitempty"`
	Files     []DiffFile     `json:"files,omitempty"`
	Added     int            `json:"totalAdditions"`
	Deleted   int            `json:"totalDeletions"`
	Journal   []JournalEntry `json:"journal"`
}

// sessionEvidence assembles the worker's evidence package: identity, live
// status, worktree diff summary, and the full journal.
func (d *DaemonHTTP) sessionEvidence(w http.ResponseWriter, r *http.Request) {
	s, err := d.fleet.Get(SessionID(r.PathValue("id")))
	if err != nil {
		http.Error(w, "unknown session", http.StatusNotFound)
		return
	}

	ev := Evidence{
		SessionID: string(s.ID),
		Task:      s.Task,
		Agent:     s.Agent,
		Branch:    s.Branch,
		Worktree:  s.Worktree,
		Status:    cardStatus(&s.Facts),
		Journal:   []JournalEntry{},
	}
	if d.journal != nil {
		ev.Journal = d.journal.Get(s.ID)
		if len(ev.Journal) > 0 {
			first, last := ev.Journal[0].At, ev.Journal[len(ev.Journal)-1].At
			ev.StartedAt = &first
			ev.UpdatedAt = &last
		}
	}
	if s.Facts.UpdatedAt != (time.Time{}) {
		ev.UpdatedAt = &s.Facts.UpdatedAt
	}

	if info, diffErr := worktreeDiff(s.Worktree, s.Base); diffErr == nil {
		ev.Files = info.Files
		for _, f := range info.Files {
			ev.Added += f.Additions
			ev.Deleted += f.Deletions
		}
	}
	writeJSON(w, http.StatusOK, ev)
}
