package cbf

import (
	"errors"
	"fmt"

	"ontology/hash"
)

// recompute 用精确 multiset 朴素重算全部 m 个计数器。
func recompute(p hash.Params, multiset map[int64]int) []uint8 {
	naive := make([]uint8, p.M())
	for x, n := range multiset {
		for _, q := range p.Positions(x) {
			naive[q] += uint8(n)
		}
	}
	return naive
}
func sumU8(s []uint8) int {
	t := 0
	for _, c := range s {
		t += int(c)
	}
	return t
}
func equalU8(a, b []uint8) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
func equalCounts(a, b map[int64]int) bool {
	if len(a) != len(b) {
		return false
	}
	for k, v := range a {
		if b[k] != v {
			return false
		}
	}
	return true
}

func cloneCounters(s []uint8) []uint8 { o := make([]uint8, len(s)); copy(o, s); return o }
func cloneCounts(m map[int64]int) map[int64]int {
	o := make(map[int64]int, len(m))
	for k, v := range m {
		o[k] = v
	}
	return o
}

// checkInvariants 核验不变量 1（无假阴性）、2（守恒）、3（朴素重算一致）。
func checkInvariants(f *Filter, tag string) error {
	if !equalU8(f.counters, recompute(f.p, f.counts)) {
		return fmt.Errorf("%s: counters disagree with naive recomputation", tag)
	}
	for x := range f.counts { // map 中只剩净次数 > 0 的键
		if !f.Contains(x) {
			return fmt.Errorf("%s: false negative for %d", tag, x)
		}
	}
	if got, want := sumU8(f.counters), f.p.K()*(f.inserts-f.deletes); got != want {
		return fmt.Errorf("%s: conservation broken: %d != %d", tag, got, want)
	}
	return nil
}

// SelfCheck 在 NOTES.md 的七步序列及一段成功/被拒混合序列上核验四条不变量。
func SelfCheck() error {
	p7, _ := hash.New(7, 3)
	g, _ := New(p7, 2)
	steps := []struct {
		apply func() error
		cnt   []uint8
		ovf   bool
	}{
		{func() error { return g.Insert(1) }, []uint8{0, 1, 0, 1, 0, 1, 0}, false},
		{func() error { return g.Insert(4) }, []uint8{1, 1, 1, 1, 1, 1, 0}, false},
		{func() error { return g.Insert(3) }, []uint8{2, 1, 1, 2, 2, 1, 0}, false},
		{func() error { return g.Insert(3) }, []uint8{2, 1, 1, 2, 2, 1, 0}, true},
	}
	for i, st := range steps {
		if e := st.apply(); errors.Is(e, ErrOverflow) != st.ovf || !equalU8(g.counters, st.cnt) {
			return fmt.Errorf("step %d: err=%v counters=%v", i+1, e, g.counters)
		}
	}
	if !g.Contains(2) {
		return errors.New("step 5: Contains(2) should be true (false positive)")
	}
	if !errors.Is(g.Delete(2), ErrDeleteUninserted) ||
		!equalU8(g.counters, []uint8{2, 1, 1, 2, 2, 1, 0}) || !g.Contains(1) {
		return errors.New("steps 6-7: Delete(2) must leave no trace and Contains(1) be true")
	}
	p101, _ := hash.New(101, 4)
	h, _ := New(p101, 9)
	seq := []int64{1, 4, 50, 4, 100, 7, 9, 4, 1, 33, 9, 100, 60, 4, 1}
	for i, x := range seq {
		bC, bM := cloneCounters(h.counters), cloneCounts(h.counts)
		var e error
		if i%5 == 2 {
			e = h.Delete(x)
		} else {
			e = h.Insert(x)
		}
		if e == nil {
			if err := checkInvariants(h, "mix"); err != nil {
				return err
			}
			continue
		}
		want := ErrOverflow
		if i%5 == 2 {
			want = ErrDeleteUninserted
		}
		if !errors.Is(e, want) || !equalU8(h.counters, bC) || !equalCounts(h.counts, bM) {
			return fmt.Errorf("i=%d: rejected op %d left a trace or wrong error", i, x)
		}
	}
	return nil
}

// CheckAccessComplexity 只对外给出通过与否：跨多个 m 档，一次操作访问的
// 计数器个数恒等于 k；不返回该非导出计数器的具体数值。
func CheckAccessComplexity() error {
	const k = 5
	for _, m := range []int{101, 251, 503, 1009, 4999, 9973} {
		p, err := hash.New(m, k)
		if err != nil {
			return err
		}
		f, err := New(p, 200)
		if err != nil {
			return err
		}
		if err := f.Insert(42); err != nil {
			return err
		}
		if f.visited.Load() != int64(k) {
			return fmt.Errorf("visited-count property failed at m=%d (value hidden)", m)
		}
	}
	return nil
}
