package orchestrator

import (
	"fmt"
	"sort"
	"strings"
)

// SessionCard is the board's projection of one session: what a card shows,
// nothing else.
type SessionCard struct {
	ID     string `json:"id"`
	Task   string `json:"task"`
	Agent  string `json:"agent"`
	Branch string `json:"branch"`
	PR     int    `json:"pr,omitempty"`
}

// Board groups every session's card by its fact-derived lane. All four lanes
// are present so a renderer can draw the board without nil checks.
func (f *Fleet) Board() map[Lane][]SessionCard {
	groups := make(map[Lane][]SessionCard, 4)
	for _, lane := range []Lane{LaneWorking, LaneNeedsYou, LaneInReview, LaneReadyToMerge} {
		groups[lane] = nil
	}

	f.mu.RLock()
	defer f.mu.RUnlock()
	for id, s := range f.sessions {
		card := SessionCard{
			ID:     string(id),
			Task:   s.Task,
			Agent:  s.Agent,
			Branch: s.Branch,
		}
		if p := s.Facts.PR; p != nil {
			card.PR = p.Number
		}
		lane := DeriveLane(s.Facts)
		groups[lane] = append(groups[lane], card)
	}

	for lane := range groups {
		cards := groups[lane]
		sort.Slice(cards, func(i, j int) bool { return cards[i].ID < cards[j].ID })
	}
	return groups
}

// RenderBoard draws the lanes as a plain-text board. It is the headless
// fallback view; the TUI and desktop apps render from Board() directly.
func RenderBoard(board map[Lane][]SessionCard) string {
	var b strings.Builder
	for _, lane := range []Lane{LaneWorking, LaneNeedsYou, LaneInReview, LaneReadyToMerge} {
		fmt.Fprintf(&b, "== %s ==\n", lane)
		cards := board[lane]
		if len(cards) == 0 {
			b.WriteString("  (empty)\n")
			continue
		}
		for _, card := range cards {
			fmt.Fprintf(&b, "  %-16s %-12s %-24s", card.ID, card.Agent, card.Branch)
			if card.PR != 0 {
				fmt.Fprintf(&b, " #%d", card.PR)
			}
			b.WriteString("\n")
			if card.Task != "" {
				fmt.Fprintf(&b, "  %-32s %s\n", "", card.Task)
			}
		}
	}
	return b.String()
}
