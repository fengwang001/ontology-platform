// Package hist implements the bounded version history of a single key:
// append with monotonically increasing version numbers, eager retention
// cleanup to the newest K versions, and read-back by version number.
package hist

// version is one retained entry: a version number and its value.
type version struct {
	v   int64
	val string
}

// Hist is the version history of one key. Versions start at 1, increase
// monotonically and are never reused. At most k newest versions are kept;
// older ones are physically removed eagerly on Put.
type Hist struct {
	k     int
	vers  []version // ascending by v, len <= k
	maxV  int64     // highest version ever allocated
	moved int       // versions examined/moved for cleanup in the last Put
}

// New returns an empty history keeping at most k versions. k must be > 0.
func New(k int) *Hist {
	return &Hist{k: k}
}

// Put appends value as the next version and returns its version number.
// If the count exceeds k, the oldest versions are physically removed at
// once; since each Put adds exactly one version, at most one is removed.
func (h *Hist) Put(val string) int64 {
	h.maxV++
	h.vers = append(h.vers, version{v: h.maxV, val: val})
	h.moved = 0
	for len(h.vers) > h.k {
		h.vers = h.vers[1:] // O(1) head drop of the oldest version
		h.moved++
	}
	return h.maxV
}

// Get returns the newest (highest-version) value.
func (h *Hist) Get() (string, bool) {
	if len(h.vers) == 0 {
		return "", false
	}
	return h.vers[len(h.vers)-1].val, true
}

// GetAt returns the value of version v. It hits iff v is still physically
// retained, i.e. oldest <= v <= maxV. v <= 0 is rejected by the caller.
func (h *Hist) GetAt(v int64) (string, bool) {
	if len(h.vers) == 0 {
		return "", false
	}
	oldest := h.vers[0].v
	if v < oldest || v > h.maxV {
		return "", false
	}
	return h.vers[v-oldest].val, true
}

// Len returns the number of currently retained versions.
func (h *Hist) Len() int {
	return len(h.vers)
}

// MaxV returns the highest version ever allocated (0 if none).
func (h *Hist) MaxV() int64 {
	return h.maxV
}
