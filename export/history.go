package export

import "sort"

import "sync"

// Record is one durable history entry for an exported write.
type Record struct {
	Link  string
	Seq   Position
	ID    string
	Epoch int
}

// HistoryStore is the append-only log of records that have been handed off.
type HistoryStore interface {
	Append(rec Record) error
	Records(link string) ([]Record, error)
	Seal(link string, epoch int, end Position) error
	SealedEnd(link string, epoch int) (Position, bool, error)
}

// MemHistory is an append-only, in-memory HistoryStore shared by every link
// of one data source. SealedEnd marks an epoch whose whole interval has been
// handed off; the end is confirmed only after the checkpoint lands.
type MemHistory struct {
	mu     sync.Mutex
	log    map[string][]Record
	sealed map[string]map[int]Position
}

// NewMemHistory creates an empty history store.
func NewMemHistory() *MemHistory {
	return &MemHistory{log: map[string][]Record{}, sealed: map[string]map[int]Position{}}
}

func (h *MemHistory) Append(rec Record) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.log[rec.Link] = append(h.log[rec.Link], rec)
	return nil
}

// Records returns a copy of a link's records, sorted by sequence.
func (h *MemHistory) Records(link string) ([]Record, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	src := h.log[link]
	out := make([]Record, len(src))
	copy(out, src)
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Seq != out[j].Seq {
			return out[i].Seq < out[j].Seq
		}
		return out[i].Epoch < out[j].Epoch
	})
	return out, nil
}

func (h *MemHistory) Seal(link string, epoch int, end Position) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.sealed[link] == nil {
		h.sealed[link] = map[int]Position{}
	}
	h.sealed[link][epoch] = end
	return nil
}

func (h *MemHistory) SealedEnd(link string, epoch int) (Position, bool, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.sealed[link] == nil {
		return 0, false, nil
	}
	end, ok := h.sealed[link][epoch]
	return end, ok, nil
}

// NextEpoch returns an epoch number never previously used by the link, even
// by abandoned, unsealed cycles. Reusing an abandoned epoch number would mix
// its leftover records into a later sealed epoch.
func (h *MemHistory) NextEpoch(link string) int {
	h.mu.Lock()
	defer h.mu.Unlock()
	max := 0
	for e := range h.sealed[link] {
		if e > max {
			max = e
		}
	}
	for _, r := range h.log[link] {
		if r.Epoch > max {
			max = r.Epoch
		}
	}
	return max + 1
}

// Erase simulates damage to the history medium: every record of link with
// seq in (lo, hi] disappears. Sealed boundaries are intentionally left
// intact so re-derivation must notice the hole and refuse to trust it.
func (h *MemHistory) Erase(link string, lo, hi Position) int {
	h.mu.Lock()
	defer h.mu.Unlock()
	kept := h.log[link][:0]
	removed := 0
	for _, r := range h.log[link] {
		if r.Seq > lo && r.Seq <= hi {
			removed++
			continue
		}
		kept = append(kept, r)
	}
	h.log[link] = append([]Record(nil), kept...)
	return removed
}
