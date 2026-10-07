package lifecycle

import "time"

type EntryKind int

const (
	EntryInit EntryKind = iota
	EntryTime
	EntryAction
	EntryCascade
)

// Entry is one immutable fact in an instance's append-only history.
type Entry struct {
	Kind       EntryKind
	Transition TransitionID
	From       StateID
	To         StateID
	At         time.Time // logical state-entry time: the only clock source used for due-time math
	SettledAt  time.Time // wall time at which lazy settlement materialized the fact
	DueAt      time.Time // for time transitions: At of the predecessor state + Duration
	Generation int64
	Reason     string
}

type History struct {
	entries []Entry
}

func NewHistory(initial StateID, at time.Time) *History {
	return &History{entries: []Entry{{
		Kind: EntryInit,
		To:   initial,
		At:   at,
	}}}
}

func (h *History) Append(e Entry) {
	h.entries = append(h.entries, e)
}

func (h *History) Entries() []Entry {
	out := make([]Entry, len(h.entries))
	copy(out, h.entries)
	return out
}

// Replay re-derives the state at asOf without mutating anything.
//
// The replay uses only each entry's logical state-entry time (Entry.At):
// it therefore reproduces exactly the state that a lazy settlement would have
// materialized had an access happened at asOf, regardless of when settlement
// in fact occurred (Entry.SettledAt is deliberately ignored). Entries inside a
// single history have non-decreasing At by construction, and ties keep their
// append (causal) order.
func (h *History) Replay(asOf time.Time) StateID {
	state := h.entries[0].To
	for _, e := range h.entries[1:] {
		if e.At.After(asOf) {
			break
		}
		state = e.To
	}
	return state
}
