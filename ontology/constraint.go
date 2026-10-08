package ontology

import "fmt"

// compiledConstraint is a category constraint sequence compiled into a
// tiny NFA over category ranks. States are positions 0..k (k = number of
// positions). Position i (0-based) consumes matcher i and moves to i+1.
// A starred first position adds a self loop on state 0 and an epsilon
// 0->1; a starred last position adds a self loop on state k and makes
// state k-1 accepting (epsilon k-1->k). State k is the accept state.
type compiledConstraint struct {
	k         int
	any       []bool
	rank      []int
	starFront bool
	starBack  bool
}

// compile validates the constraint sequence and compiles it. Validation
// of parameters always happens before any graph search.
func (s *Store) compile(qc []ConstraintPos) (*compiledConstraint, error) {
	if len(qc) == 0 {
		return nil, fmt.Errorf("%w: constraint sequence is empty", ErrInvalidParams)
	}
	cc := &compiledConstraint{
		k:         len(qc),
		any:       make([]bool, len(qc)),
		rank:      make([]int, len(qc)),
		starFront: qc[0].Star,
		starBack:  qc[len(qc)-1].Star,
	}
	for i, p := range qc {
		if p.Star && i != 0 && i != len(qc)-1 {
			return nil, fmt.Errorf("%w: repeat marker at middle position %d", ErrInvalidParams, i)
		}
		cc.any[i] = p.Any
		if !p.Any {
			r, ok := s.catRank[p.Category]
			if !ok {
				return nil, fmt.Errorf("%w: unknown category %q", ErrInvalidParams, p.Category)
			}
			cc.rank[i] = r
		}
	}
	return cc, nil
}

func (cc *compiledConstraint) matches(pos, r int) bool {
	return cc.any[pos] || cc.rank[pos] == r
}

// closure returns the epsilon closure of state p (including p).
func (cc *compiledConstraint) closure(p int) []int {
	seen := map[int]bool{p: true}
	out := []int{p}
	for i := 0; i < len(out); i++ {
		q := out[i]
		add := func(n int) {
			if n >= 0 && n <= cc.k && !seen[n] {
				seen[n] = true
				out = append(out, n)
			}
		}
		if q == 0 && cc.starFront {
			add(1) // skip the repeatable first position entirely
		}
		if q == cc.k-1 && cc.starBack {
			add(cc.k) // zero repetitions of the last position
		}
	}
	return out
}

// next returns the post-closure successor states of p after consuming a
// category with rank r.
func (cc *compiledConstraint) next(p, r int) []int {
	seen := map[int]bool{}
	var out []int
	add := func(n int) {
		for _, m := range cc.closure(n) {
			if !seen[m] {
				seen[m] = true
				out = append(out, m)
			}
		}
	}
	for _, q := range cc.closure(p) {
		switch {
		case q < cc.k && cc.matches(q, r):
			add(q + 1)
		}
		if q == 0 && cc.starFront && cc.matches(0, r) {
			add(0) // repeat the first position
		}
		if q == cc.k && cc.starBack && cc.matches(cc.k-1, r) {
			add(cc.k) // repeat the last position
		}
	}
	return out
}

// accepting reports whether state p accepts (possibly via epsilon).
func (cc *compiledConstraint) accepting(p int) bool {
	for _, q := range cc.closure(p) {
		if q == cc.k {
			return true
		}
	}
	return false
}
