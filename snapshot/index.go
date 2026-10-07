package snapshot

// idIndex is a fixed-capacity open-addressing hash set (linear probing) used
// for dangling-reference checks.
//
// Complexity contract:
//   - Add/Has touch an expected constant number of slots independent of the
//     total number of indexed records; the target slice is never scanned.
//   - ProbeCount exposes the exact number of slot probes performed since
//     construction, so tests can empirically verify that membership checks do
//     not grow linearly with target-block size: for N random ids the average
//     probes per Has stays bounded as N grows (load factor is kept <= 0.5 by
//     resizing, giving an expected cost of <= ~1.5 probes at that density).
type idIndex struct {
	keys   []string
	used   []bool
	mask   uint64
	size   int
	probes int64
}

func newIDIndex(capHint int) *idIndex {
	n := 8
	for n < capHint*2 {
		n <<= 1
	}
	return &idIndex{keys: make([]string, n), used: make([]bool, n), mask: uint64(n - 1)}
}

// FNV-1a keeps the implementation dependency-free and deterministic.
func hashString(s string) uint64 {
	const (
		offset64 = 14695981039346656037
		prime64  = 1099511628211
	)
	h := uint64(offset64)
	for i := 0; i < len(s); i++ {
		h ^= uint64(s[i])
		h *= prime64
	}
	return h
}

func (idx *idIndex) Add(id string) {
	if (idx.size+1)*2 > len(idx.keys) {
		idx.grow()
	}
	h := hashString(id) & idx.mask
	for {
		idx.probes++
		if !idx.used[h] {
			idx.used[h] = true
			idx.keys[h] = id
			idx.size++
			return
		}
		if idx.keys[h] == id {
			return
		}
		h = (h + 1) & idx.mask
	}
}

func (idx *idIndex) Has(id string) bool {
	h := hashString(id) & idx.mask
	for {
		idx.probes++
		if !idx.used[h] {
			return false
		}
		if idx.keys[h] == id {
			return true
		}
		h = (h + 1) & idx.mask
	}
}

func (idx *idIndex) grow() {
	oldKeys, oldUsed := idx.keys, idx.used
	n := len(oldKeys) * 2
	idx.keys = make([]string, n)
	idx.used = make([]bool, n)
	idx.mask = uint64(n - 1)
	idx.size = 0
	for i, k := range oldKeys {
		if oldUsed[i] {
			idx.Add(k)
		}
	}
}

func (idx *idIndex) ProbeCount() int64 { return idx.probes }
func (idx *idIndex) Len() int          { return idx.size }
