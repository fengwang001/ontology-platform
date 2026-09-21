package ontology

import (
	"sort"
	"sync"
)

// entry is one retained row with its precomputed ranking keys.
type entry struct {
	row   map[string]any
	score float64
	tie   string
	hash  uint64
	canon string
}

// better reports whether a ranks strictly ahead of b.
func better(a, b entry) bool {
	if a.score != b.score {
		return a.score > b.score
	}
	if a.tie != b.tie {
		return a.tie < b.tie
	}
	if a.hash != b.hash {
		return a.hash < b.hash
	}
	return a.canon < b.canon
}

// group holds the bounded, best-first sorted buffer of one group.
type group struct {
	key        GroupKey
	buf        []entry
	skipNonNum int
	skipNaN    int
}

// Selector is a concurrent-safe, per-group Top-N selector.
type Selector struct {
	mu        sync.Mutex
	cfg       Config
	groups    map[string]*group
	processed int64
}

// Add feeds one row. Rows with a missing/non-numeric or NaN score are
// counted per group but never retained.
func (s *Selector) Add(row map[string]any) {
	key := groupKeyOf(row, s.cfg.GroupKey)
	score, numeric, nan := scoreOf(row, s.cfg.ScoreKey)

	s.mu.Lock()
	defer s.mu.Unlock()
	s.processed++

	g, ok := s.groups[key.encoded()]
	if !ok {
		g = &group{key: key}
		s.groups[key.encoded()] = g
	}

	if !numeric {
		g.skipNonNum++
		return
	}
	if nan {
		g.skipNaN++
		return
	}

	hash, canon := contentID(row)
	e := entry{row: row, score: score, tie: tieOf(row, s.cfg.TieKey), hash: hash, canon: canon}

	if len(g.buf) == s.cfg.N && !better(e, g.buf[len(g.buf)-1]) {
		return
	}
	pos := sort.Search(len(g.buf), func(i int) bool { return better(e, g.buf[i]) })
	g.buf = append(g.buf, entry{})
	copy(g.buf[pos+1:], g.buf[pos:])
	g.buf[pos] = e
	if len(g.buf) > s.cfg.N {
		g.buf = g.buf[:s.cfg.N]
	}
}

// Stats returns the number of rows currently held and the number of groups.
// held is always <= groups * N.
func (s *Selector) Stats() (held, groupCount int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, g := range s.groups {
		held += len(g.buf)
	}
	return held, len(s.groups)
}

// Processed returns the total number of rows passed to Add.
func (s *Selector) Processed() int64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.processed
}

// Skips returns the per-group counts of rows excluded from ranking:
// nonNumeric counts missing/non-numeric scores, nan counts NaN scores.
// ok is false if the group does not exist.
func (s *Selector) Skips(key GroupKey) (nonNumeric, nan int, ok bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	g, found := s.groups[key.encoded()]
	if !found {
		return 0, 0, false
	}
	return g.skipNonNum, g.skipNaN, true
}
