package mig

import (
	"sort"

	"ontology/keyenc"
)

// model is the naive reference: it mirrors the logical view as one map and
// additionally tracks A/B ownership by the watermark, including migration
// residues created by StepPartial so Step, Finish and Recover counts match.
type model struct {
	data    map[int64]int64 // logical view; always equals visibleA ∪ visibleB
	a       map[int64]int64 // physical A (may hold <w residue after p=2)
	b       map[int64]int64 // physical B (may hold >=w residue after p=1)
	w       int64
	crashed bool
}

func visible(data map[int64]int64, pred func(int64) bool) map[int64]int64 {
	out := make(map[int64]int64)
	for k, v := range data {
		if pred(k) {
			out[k] = v
		}
	}
	return out
}

func newModel() *model {
	return &model{
		data: map[int64]int64{},
		a:    map[int64]int64{},
		b:    map[int64]int64{},
		w:    keyenc.MinKey,
	}
}

func (m *model) rebuild() {
	m.data = make(map[int64]int64)
	for k, v := range m.b {
		if k < m.w {
			m.data[k] = v
		}
	}
	for k, v := range m.a {
		if k >= m.w {
			m.data[k] = v
		}
	}
}

func (m *model) put(k, v int64) error {
	if !validKey(k) || v < -1e9 || v > 1e9 {
		return ErrInvalidArg
	}
	if m.crashed {
		return ErrCrashed
	}
	if k < m.w {
		m.b[k] = v
	} else {
		m.a[k] = v
	}
	m.rebuild()
	return nil
}

func (m *model) del(k int64) (bool, error) {
	if !validKey(k) {
		return false, ErrInvalidArg
	}
	if m.crashed {
		return false, ErrCrashed
	}
	var side map[int64]int64
	if k < m.w {
		side = m.b
	} else {
		side = m.a
	}
	_, ok := side[k]
	delete(side, k)
	m.rebuild()
	return ok, nil
}

func (m *model) get(k int64) (int64, bool, error) {
	if !validKey(k) {
		return 0, false, ErrInvalidArg
	}
	m.rebuild()
	v, ok := m.data[k]
	return v, ok, nil
}

func (m *model) smallestA(n int) []int64 {
	ks := make([]int64, 0, n)
	for k := range m.a {
		ks = append(ks, k)
	}
	sort.Slice(ks, func(i, j int) bool { return ks[i] < ks[j] })
	if len(ks) > n {
		ks = ks[:n]
	}
	return ks
}

func (m *model) step(n int) (int, error) {
	if n < 1 || n > 10_000 {
		return 0, ErrInvalidArg
	}
	if m.crashed {
		return 0, ErrCrashed
	}
	moved := 0
	for _, k := range m.smallestA(n) {
		m.b[k] = m.a[k]
		delete(m.a, k)
		m.w = k + 1
		moved++
	}
	m.rebuild()
	return moved, nil
}

func (m *model) stepPartial(p int) error {
	if p != 1 && p != 2 {
		return ErrInvalidArg
	}
	if m.crashed {
		return ErrCrashed
	}
	ks := m.smallestA(1)
	if len(ks) == 0 {
		return ErrDrained
	}
	m.b[ks[0]] = m.a[ks[0]] // step 1: write B (A still holds the key)
	if p == 2 {
		m.w = ks[0] + 1 // step 2: commit watermark (step 3 delete A skipped)
	}
	m.crashed = true
	m.rebuild()
	return nil
}

func (m *model) finish() error {
	if m.crashed {
		return ErrCrashed
	}
	if len(m.a) != 0 {
		return ErrNotDrained
	}
	m.w = keyenc.MaxKey + 1
	m.rebuild()
	return nil
}

func (m *model) scan(lo, hi int64) ([]KV, error) {
	if lo < keyenc.MinKey || hi > keyenc.MaxKey+1 || lo > hi {
		return nil, ErrInvalidArg
	}
	m.rebuild()
	ks := make([]int64, 0)
	for k := range m.data {
		if k >= lo && k < hi {
			ks = append(ks, k)
		}
	}
	sort.Slice(ks, func(i, j int) bool { return ks[i] < ks[j] })
	out := make([]KV, len(ks))
	for i, k := range ks {
		out[i] = KV{Key: k, Value: m.data[k]}
	}
	return out, nil
}

func (m *model) recover() (int, int, error) {
	if !m.crashed {
		return 0, 0, ErrNotCrashed
	}
	bDeleted, aDeleted := 0, 0
	for k := range m.b { // p=1 residue: B keys >= w -> roll back
		if k >= m.w {
			delete(m.b, k)
			bDeleted++
		}
	}
	for k, v := range m.a { // p=2 residue: A keys < w -> roll forward to B
		if k < m.w {
			m.b[k] = v
			delete(m.a, k)
			aDeleted++
		}
	}
	m.crashed = false
	m.rebuild()
	return bDeleted, aDeleted, nil
}
