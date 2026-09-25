// Package mrg: k-way run merge (heap, ties by run ID) + sort-merge join.
package mrg

import (
	"errors"
	"ontology/run"
)

// ErrBadFanIn is returned when maxFanIn < 2.
var ErrBadFanIn = errors.New("mrg: maxFanIn must be >= 2")

// Pair is one join output tuple pair.
type Pair struct{ R, S int }

// hentry is a run's current head; less orders by key, ties by run ID.
type hentry struct{ key, run int }

func less(a, b hentry) bool { return a.key < b.key || (a.key == b.key && a.run < b.run) }

// merger is a min-heap over run heads; probes (non-exported) counts
// run heads inspected per next call, reset by every next.
type merger struct {
	runs   [][]int
	pos    []int
	heap   []hentry
	probes int
}

func newMerger(runs [][]int) *merger {
	m := &merger{runs: runs, pos: make([]int, len(runs))}
	for i, r := range runs {
		if len(r) > 0 {
			m.heap = append(m.heap, hentry{r[0], i})
		}
	}
	for i := len(m.heap)/2 - 1; i >= 0; i-- {
		m.down(i)
	}
	return m
}

// down sifts i down; each comparison inspects one run head and is
// charged to probes: ≤2 per level ⇒ ≤2*ceil(log2 n) per next call.
func (m *merger) down(i int) {
	for {
		s := i
		for _, c := range []int{2*i + 1, 2*i + 2} {
			if c < len(m.heap) {
				m.probes++
				if less(m.heap[c], m.heap[s]) {
					s = c
				}
			}
		}
		if s == i {
			return
		}
		m.heap[i], m.heap[s] = m.heap[s], m.heap[i]
		i = s
	}
}

// next pops the smallest head and advances that run's cursor.
func (m *merger) next() (int, bool) {
	if len(m.heap) == 0 {
		return 0, false
	}
	m.probes = 0
	top := m.heap[0]
	m.pos[top.run]++
	if m.pos[top.run] < len(m.runs[top.run]) { // same run supplies next head
		m.heap[0] = hentry{m.runs[top.run][m.pos[top.run]], top.run}
	} else { // run exhausted: move last heap entry to the top
		m.heap[0], m.heap = m.heap[len(m.heap)-1], m.heap[:len(m.heap)-1]
	}
	if len(m.heap) > 0 {
		m.down(0)
	}
	return top.key, true
}
func drain(parts [][]int) []int {
	mg := newMerger(parts)
	var out []int
	for k, ok := mg.next(); ok; k, ok = mg.next() {
		out = append(out, k)
	}
	return out
}

// Sorted merges runs into one ascending sequence (0 or 1 runs are the
// sequence itself), cascading in rounds of ≤maxFanIn runs if needed.
func Sorted(runs []run.Run, maxFanIn int) ([]int, error) {
	if maxFanIn < 2 {
		return nil, ErrBadFanIn
	}
	if len(runs) == 0 {
		return nil, nil
	}
	level := make([][]int, len(runs))
	for i, r := range runs {
		level[i] = r.Keys
	}
	for len(level) > 1 {
		var next [][]int
		for i := 0; i < len(level); i += maxFanIn {
			next = append(next, drain(level[i:min(i+maxFanIn, len(level))]))
		}
		level = next
	}
	return level[0], nil
}

// Joiner merges two run sets and joins them; the merged sequences are
// materialized once and never mutated, so Join is read-only.
type Joiner struct{ r, s []int }

// New builds a Joiner. maxFanIn must be >= 2.
func New(runsR, runsS []run.Run, maxFanIn int) (*Joiner, error) {
	j := &Joiner{}
	var err error
	if j.r, err = Sorted(runsR, maxFanIn); err != nil {
		return nil, err
	}
	if j.s, err = Sorted(runsS, maxFanIn); err != nil {
		return nil, err
	}
	return j, nil
}

// Join returns all equal-key pairs: on equal keys the whole equal-key
// runs on both sides are cross-producted, then both cursors skip past.
func (j *Joiner) Join() []Pair {
	var out []Pair
	i, k := 0, 0
	for i < len(j.r) && k < len(j.s) {
		switch {
		case j.r[i] < j.s[k]:
			i++
		case j.r[i] > j.s[k]:
			k++
		default: // equal: cross-product both equal-key runs, skip past
			i2, k2 := i+1, k+1
			for i2 < len(j.r) && j.r[i2] == j.r[i] {
				i2++
			}
			for k2 < len(j.s) && j.s[k2] == j.s[k] {
				k2++
			}
			for n := (i2 - i) * (k2 - k); n > 0; n-- {
				out = append(out, Pair{j.r[i], j.s[k]})
			}
			i, k = i2, k2
		}
	}
	return out
}
