package orchestrator

import (
	"sync"
	"time"
)

// JournalEntry is one recorded fact about a worker session's life: launch,
// output lines, exit. Entries are append-only; the board reads them.
type JournalEntry struct {
	At   time.Time `json:"at"`
	Kind string    `json:"kind"` // launched | output | exited | stopped
	Text string    `json:"text"`
}

// Journal is the per-session event record. It is the daemon-side evidence
// trail (PRD F4): what the worker did, in order, kept even after the board
// lane changes.
type Journal struct {
	mu      sync.RWMutex
	entries map[SessionID][]JournalEntry
}

// NewJournal returns an empty journal.
func NewJournal() *Journal {
	return &Journal{entries: make(map[SessionID][]JournalEntry)}
}

// Append records one entry.
func (j *Journal) Append(id SessionID, kind, text string) {
	j.mu.Lock()
	defer j.mu.Unlock()
	j.entries[id] = append(j.entries[id], JournalEntry{At: time.Now(), Kind: kind, Text: text})
}

// Get returns a copy of the session's entries; unknown sessions read empty.
func (j *Journal) Get(id SessionID) []JournalEntry {
	j.mu.RLock()
	defer j.mu.RUnlock()
	out := make([]JournalEntry, len(j.entries[id]))
	copy(out, j.entries[id])
	return out
}
