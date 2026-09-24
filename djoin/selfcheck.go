package djoin

import (
	"fmt"
	"sort"

	"ontology/rel"
)

// recompute 用两张表的当前状态全量重算 R⋈S（朴素参照，不走差分）。
func recompute(r, s *rel.Table) map[tkey]int64 {
	out := map[tkey]int64{}
	sm := map[int64]map[string]int64{}
	for _, t := range s.Snapshot() {
		if sm[t.K] == nil {
			sm[t.K] = map[string]int64{}
		}
		sm[t.K][t.V] = t.Mult
	}
	for _, x := range r.Snapshot() {
		for b, smb := range sm[x.K] {
			out[tkey{x.K, x.V, b}] += x.Mult * smb
		}
	}
	return out
}

// naiveDiff 返回全量求差 new−old 中多重性非零的元组。
func naiveDiff(old, nw map[tkey]int64) map[tkey]int64 {
	out := map[tkey]int64{}
	for key, m := range nw {
		if d := m - old[key]; d != 0 {
			out[key] = d
		}
	}
	for key, m := range old {
		if _, ok := nw[key]; !ok {
			out[key] = -m
		}
	}
	return out
}

func asMap(ds []Delta) map[tkey]int64 {
	m := map[tkey]int64{}
	for _, d := range ds {
		m[tkey{d.K, d.A, d.B}] = d.Mult
	}
	return m
}

func negative(m agg, t *rel.Table) bool {
	for k, bk := range m {
		for v, d := range bk {
			if t.Mult(k, v)+d < 0 {
				return true
			}
		}
	}
	return false
}
func apply(m agg, t *rel.Table) {
	for k, bk := range m {
		for v, d := range bk {
			t.Add(k, v, d)
		}
	}
}

// sorted 把 {元组→多重性} 转成按 (K,A,B) 升序的非零 Delta 列表。
func sorted(mm map[tkey]int64) []Delta {
	out := make([]Delta, 0, len(mm))
	for key, mult := range mm {
		if mult != 0 {
			out = append(out, Delta{K: key.k, A: key.a, B: key.b, Mult: mult})
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].K != out[j].K {
			return out[i].K < out[j].K
		}
		if out[i].A != out[j].A {
			return out[i].A < out[j].A
		}
		return out[i].B < out[j].B
	})
	return out
}

// SelfCheck 用内置批次序列核验四条不变量，全部通过返回 nil。
// 序列即 NOTES.md 第三节的三批，并追加一批多倍插入以覆盖多重性 >1。
func (e *Engine) SelfCheck() error {
	r := func(k int64, a string, sg int) rel.Row { return rel.Row{K: k, V: a, Sign: sg} }
	batches := [][2][]rel.Row{
		{{r(1, "x", 1), r(2, "y", 1)}, {r(1, "p", 1), r(2, "q", 1)}},
		{{r(1, "z", 1), r(2, "y", -1)}, {r(1, "r", 1), r(2, "q", 1)}},
		{{r(1, "x", -1)}, {r(1, "r", -1)}},
		{{r(7, "u", 1), r(7, "u", 1)}, {r(7, "v", 1), r(7, "v", 1)}},
	}
	downstream := map[tkey]int64{} // 模拟下游按顺序应用每个批次前缀
	for i, b := range batches {
		old := recompute(e.r, e.s)
		got, err := e.Feed(b[0], b[1])
		if err != nil {
			return fmt.Errorf("batch %d: unexpected error %v", i+1, err)
		}
		// 不变量 2：本批差分逐条等于全量求差。
		want := naiveDiff(old, recompute(e.r, e.s))
		if len(asMap(got)) != len(want) {
			return fmt.Errorf("batch %d: delta keys %d want %d", i+1, len(got), len(want))
		}
		for key, m := range want {
			if asMap(got)[key] != m {
				return fmt.Errorf("batch %d: delta %v got %d want %d", i+1, key, asMap(got)[key], m)
			}
		}
		// 不变量 3：下游应用本批后所有元组多重性非负。
		for key, m := range asMap(got) {
			downstream[key] += m
			if downstream[key] < 0 {
				return fmt.Errorf("batch %d: tuple %v negative after prefix: %d", i+1, key, downstream[key])
			}
		}
		// 不变量 1：物化结果等于全量重算。
		view := asMap(e.View())
		full := recompute(e.r, e.s)
		if len(view) != len(full) {
			return fmt.Errorf("batch %d: view size %d want %d", i+1, len(view), len(full))
		}
		for key, m := range full {
			if view[key] != m {
				return fmt.Errorf("batch %d: view %v got %d want %d", i+1, key, view[key], m)
			}
		}
	}
	return nil
}
