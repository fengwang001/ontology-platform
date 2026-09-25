// Package hist holds the versioned value history of a single key.
// Versions are stored in a fixed-size ring so that dropping the oldest
// version on overflow is O(1): one slot is overwritten, nothing is moved.
package hist

// Entry is one retained version.
type Entry struct {
	Version int64
	Value   string
}

// History is the bounded version history of one key. The zero value is
// not usable; create it with New.
type History struct {
	ring []Entry // len == K, slots reused cyclically once full
	head int     // index of the oldest retained version
	n    int     // number of retained versions (0..K)
	maxV int64   // highest version ever appended

	// lastCleanupMoves counts the versions the most recent Append had to
	// inspect/overwrite for cleanup. Unexported on purpose: it is an
	// internal complexity probe, never part of the public API.
	lastCleanupMoves int
}

// New creates a history retaining at most k versions.
func New(k int) *History {
	return &History{ring: make([]Entry, k)}
}

// Append records value under the externally assigned version and eagerly
// removes the oldest version if the history is full.
func (h *History) Append(version int64, value string) {
	h.maxV = version
	if h.n < len(h.ring) {
		h.ring[(h.head+h.n)%len(h.ring)] = Entry{version, value}
		h.n++
		h.lastCleanupMoves = 0
		return
	}
	// Full: overwrite the single oldest slot and advance the head by one.
	h.ring[h.head] = Entry{version, value}
	h.head = (h.head + 1) % len(h.ring)
	h.lastCleanupMoves = 1
}

// Len is the number of versions currently physically retained.
func (h *History) Len() int { return h.n }

// MaxVersion is the highest version ever assigned to this key.
func (h *History) MaxVersion() int64 { return h.maxV }

// OldestVersion is the lowest version still physically present.
func (h *History) OldestVersion() int64 {
	if h.n == 0 {
		return 0
	}
	return h.maxV - int64(h.n) + 1
}

// Latest returns the highest-version value; false if the history is empty.
func (h *History) Latest() (string, bool) {
	if h.n == 0 {
		return "", false
	}
	e := h.ring[(h.head+h.n-1)%len(h.ring)]
	return e.Value, true
}

// At returns the value of version v. It hits iff v is inside the retained
// window [OldestVersion, MaxVersion]; cleaned, future and non-positive
// versions all miss (non-positive versions are additionally rejected as
// invalid arguments by the upper layer).
func (h *History) At(v int64) (string, bool) {
	if h.n == 0 || v <= 0 {
		return "", false
	}
	oldest := h.OldestVersion()
	if v < oldest || v > h.maxV {
		return "", false
	}
	idx := (h.head + int(v-oldest)) % len(h.ring)
	return h.ring[idx].Value, true
}

// All returns the retained versions oldest-first, for self-checks.
func (h *History) All() []Entry {
	out := make([]Entry, 0, h.n)
	for i := 0; i < h.n; i++ {
		out = append(out, h.ring[(h.head+i)%len(h.ring)])
	}
	return out
}
