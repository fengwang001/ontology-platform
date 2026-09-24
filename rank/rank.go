// Package rank maintains ROW_NUMBER/RANK/DENSE_RANK with sorted slices and
// order-statistic counts; no rank fields are stored, so mutations rewrite
// none regardless of set size.
package rank

import (
	"errors"
	"sort"
	"strconv"

	"ontology/ord"
)

// Distinct decidable sentinel errors for the three rejected operations.
var (
	ErrEmptyID     = errors.New("rank: empty id")
	ErrDuplicateID = errors.New("rank: id already exists")
	ErrIDNotFound  = errors.New("rank: id not found")
)

type ek struct {
	sc int64
	id string
}

func better(a, b ek) bool { // higher score first, then smaller id
	if a.sc != b.sc {
		return a.sc > b.sc
	}
	return a.id < b.id
}

// Set is the ranking view; not goroutine-safe itself (api serializes it).
type Set struct {
	els         []ek    // sorted by better
	scores      []int64 // distinct scores, descending
	by          map[string]int64
	cnt         map[int64]int
	lastRewrite int // unexported: stored rank fields rewritten by last Add/Remove
}

func New() *Set { return &Set{by: map[string]int64{}, cnt: map[int64]int{}} }

func pos(s []ek, x ek) int { // entries strictly before x (x may be absent)
	return sort.Search(len(s), func(i int) bool { return !better(s[i], x) })
}
func (s *Set) addEl(x ek) {
	i := pos(s.els, x)
	s.els = append(s.els, ek{})
	copy(s.els[i+1:], s.els[i:])
	s.els[i] = x
}
func (s *Set) delEl(x ek) {
	i := pos(s.els, x)
	copy(s.els[i:], s.els[i+1:])
	s.els = s.els[:len(s.els)-1]
}
func (s *Set) dpos(sc int64) int { // distinct scores strictly higher than sc
	return sort.Search(len(s.scores), func(i int) bool { return s.scores[i] <= sc })
}
func (s *Set) insScore(sc int64) {
	i := s.dpos(sc)
	s.scores = append(s.scores, 0)
	copy(s.scores[i+1:], s.scores[i:])
	s.scores[i] = sc
}
func (s *Set) remScore(sc int64) {
	i := s.dpos(sc)
	copy(s.scores[i:], s.scores[i+1:])
	s.scores = s.scores[:len(s.scores)-1]
}

// Add validates before touching structure, so rejections leave no trace.
func (s *Set) Add(id string, score int64) error {
	if id == "" {
		return ErrEmptyID
	}
	if _, ok := s.by[id]; ok {
		return ErrDuplicateID
	}
	s.addEl(ek{score, id})
	if s.cnt[score] == 0 {
		s.insScore(score)
	}
	s.cnt[score]++
	s.by[id] = score
	s.lastRewrite = 0 // successors move via counts, not stored rank fields
	return nil
}

// Remove rejects a missing id before touching structure.
func (s *Set) Remove(id string) error {
	score, ok := s.by[id]
	if !ok {
		return ErrIDNotFound
	}
	s.delEl(ek{score, id})
	delete(s.by, id)
	if s.cnt[score]--; s.cnt[score] == 0 { // the score value disappears
		s.remScore(score)
		delete(s.cnt, score)
	}
	s.lastRewrite = 0
	return nil
}

// Get derives the triple from counts; (sc,"") precedes every real id of sc.
func (s *Set) Get(id string) (ord.Triple, bool) {
	sc, ok := s.by[id]
	if !ok {
		return ord.Triple{}, false
	}
	return ord.Triple{
		RowNumber: 1 + pos(s.els, ek{sc, id}),
		Rank:      1 + pos(s.els, ek{sc, ""}),
		DenseRank: 1 + s.dpos(sc),
	}, true
}

func (s *Set) Snapshot() (out []ord.Element) {
	for id, sc := range s.by {
		out = append(out, ord.Element{ID: id, Score: sc})
	}
	return
}

// ComplexityBoundHolds reports (without revealing the count) that inserting a
// new top score rewrites an m-independent number of stored rank fields.
func ComplexityBoundHolds() bool {
	first := -1
	for _, m := range []int{100, 1000, 10000} {
		s := New()
		for i := 0; i < m; i++ {
			if err := s.Add(strconv.Itoa(i), int64(i)); err != nil {
				return false
			}
		}
		if err := s.Add("zzz", int64(m)); err != nil || s.lastRewrite > 3 {
			return false
		}
		if first == -1 {
			first = s.lastRewrite
		} else if s.lastRewrite != first {
			return false
		}
	}
	return true
}
