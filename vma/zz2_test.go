package vma

import "testing"

func TestZ2Gap(t *testing.T) {
	m, _ := New(1, 28, 8, 4, 40)
	m.Mmap(0, 9, 7, 0, 0, 0)  // top placement [19? ...] build manually instead
	// Build exact table: [1,10) anon perm7, [10,11) growsdown, [11,14) perm7, [23,25) perm1
	m.mu.Lock()
	vis := new(int)
	for _, v := range []VMA{
		{Start: 1, End: 10, Perm: 7},
		{Start: 10, End: 11, Perm: 0, GrowsDown: true},
		{Start: 11, End: 14, Perm: 7},
		{Start: 23, End: 25, Perm: 1},
	} {
		m.root = m.t.insert(m.root, v, vis)
	}
	m.count = 4
	m.mu.Unlock()
	lo, hi, ok := m.t.gapSearch(m.root, 4, m.high, new(int))
	t.Logf("gap ok=%v [%d,%d)", ok, lo, hi)
}
