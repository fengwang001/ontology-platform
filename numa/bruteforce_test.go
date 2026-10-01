package numa

import (
	"fmt"
	"math/bits"
	"math/rand"
	"testing"
)

// bruteSim 是完全按题面规则逐步写成的暴力参照实现，刻意不复用生产代码。
type bruteSim struct {
	n      int
	full   uint64
	caps   []int64
	free   []int64
	policy Policy
	recs   map[string]bruteRec
}

type bruteRec struct {
	req   int64
	mask  uint64
	pref  bool
	alloc []int64
}

func newBruteSim(n int, caps []int64, policy Policy) *bruteSim {
	free := append([]int64(nil), caps...)
	return &bruteSim{n: n, full: 1<<uint(n) - 1, caps: append([]int64(nil), caps...), free: free, policy: policy, recs: map[string]bruteRec{}}
}

func (s *bruteSim) builtin(req int64) []Hint {
	var feasible []uint64
	for m := uint64(1); m <= s.full; m++ {
		var sum int64
		for i := 0; i < s.n; i++ {
			if m&(1<<uint(i)) != 0 {
				sum += s.free[i]
			}
		}
		if sum >= req {
			feasible = append(feasible, m)
		}
	}
	if len(feasible) == 0 {
		return nil
	}
	zc := bits.OnesCount64(feasible[0])
	for _, m := range feasible[1:] {
		if c := bits.OnesCount64(m); c < zc {
			zc = c
		}
	}
	out := make([]Hint, 0, len(feasible))
	for _, m := range feasible {
		out = append(out, Hint{Mask: m, Preferred: bits.OnesCount64(m) == zc})
	}
	return out
}

func (s *bruteSim) normalize(raw ProviderHints, list []Hint) []Hint {
	if raw.NoPreference {
		return []Hint{{Mask: s.full, Preferred: true}}
	}
	in := list
	if s.policy == PolicySingleNUMANode && len(in) > 0 {
		var kept []Hint
		for _, h := range in {
			if bits.OnesCount64(h.Mask) == 1 {
				kept = append(kept, h)
			}
		}
		in = kept
	}
	if len(in) == 0 {
		return []Hint{{Mask: s.full, Preferred: false}}
	}
	return in
}

func (s *bruteSim) admit(id string, req int64, raws []ProviderHints) (*Result, error) {
	if id == "" || req < 1 || req > 1_000_000_000_000 {
		return nil, ErrInvalidArgument
	}
	for _, p := range raws {
		for _, h := range p.Hints {
			if h.Mask == 0 || h.Mask > s.full {
				return nil, ErrInvalidArgument
			}
		}
	}
	if _, ok := s.recs[id]; ok {
		return nil, ErrContainerExists
	}
	var total int64
	for _, f := range s.free {
		total += f
	}
	if total < req {
		return nil, ErrInsufficientCapacity
	}
	if s.policy == PolicyNone {
		alloc := s.allocate(s.full, req)
		s.commit(id, req, s.full, true, alloc)
		return &Result{Mask: s.full, Preferred: true, Allocation: alloc}, nil
	}

	all := [][]Hint{s.normalize(ProviderHints{}, s.builtin(req))}
	product := len(all[0])
	for _, p := range raws {
		l := s.normalize(p, p.Hints)
		all = append(all, l)
		product *= len(l)
	}
	if product > maxCombinations {
		return nil, ErrTooManyCombinations
	}

	type cand struct {
		mask    uint64
		allPref bool
	}
	var cands []cand
	var dfs func(k int, m uint64, pref bool)
	dfs = func(k int, m uint64, pref bool) {
		if k == len(all) {
			if m != 0 {
				cands = append(cands, cand{m, pref})
			}
			return
		}
		for _, h := range all[k] {
			dfs(k+1, m&h.Mask, pref && h.Preferred)
		}
	}
	dfs(0, s.full, true)

	best := Hint{Mask: s.full, Preferred: false}
	if len(cands) > 0 {
		z := 0
		for _, c := range cands {
			if c.allPref {
				if b := bits.OnesCount64(c.mask); z == 0 || b < z {
					z = b
				}
			}
		}
		type scored struct {
			h Hint
		}
		var hs []scored
		for _, c := range cands {
			hs = append(hs, scored{Hint{Mask: c.mask, Preferred: c.allPref && z > 0 && bits.OnesCount64(c.mask) == z}})
		}
		best = hs[0].h
		for _, sc := range hs[1:] {
			h := sc.h
			if h.Preferred && !best.Preferred {
				best = h
			} else if h.Preferred == best.Preferred {
				ca, cb := bits.OnesCount64(h.Mask), bits.OnesCount64(best.Mask)
				if ca < cb || (ca == cb && h.Mask < best.Mask) {
					best = h
				}
			}
		}
	}

	if (s.policy == PolicyRestricted || s.policy == PolicySingleNUMANode) && !best.Preferred {
		return nil, ErrHintNotSatisfied
	}
	alloc := s.allocate(best.Mask, req)
	s.commit(id, req, best.Mask, best.Preferred, alloc)
	return &Result{Mask: best.Mask, Preferred: best.Preferred, Allocation: alloc}, nil
}

func (s *bruteSim) allocate(mask uint64, req int64) []int64 {
	alloc := make([]int64, s.n)
	rem := req
	for i := 0; i < s.n && rem > 0; i++ {
		if mask&(1<<uint(i)) != 0 {
			t := s.free[i]
			if t > rem {
				t = rem
			}
			alloc[i] += t
			rem -= t
		}
	}
	for i := 0; i < s.n && rem > 0; i++ {
		if mask&(1<<uint(i)) == 0 {
			t := s.free[i]
			if t > rem {
				t = rem
			}
			alloc[i] += t
			rem -= t
		}
	}
	return alloc
}

func (s *bruteSim) commit(id string, req int64, mask uint64, pref bool, alloc []int64) {
	for i, a := range alloc {
		s.free[i] -= a
	}
	s.recs[id] = bruteRec{req: req, mask: mask, pref: pref, alloc: append([]int64(nil), alloc...)}
}

func (s *bruteSim) release(id string) error {
	r, ok := s.recs[id]
	if !ok {
		return ErrReleaseNotFound
	}
	for i, a := range r.alloc {
		s.free[i] += a
	}
	delete(s.recs, id)
	return nil
}

func (s *bruteSim) query(id string) (*Result, error) {
	r, ok := s.recs[id]
	if !ok {
		return nil, ErrQueryNotFound
	}
	return &Result{Mask: r.mask, Preferred: r.pref, Allocation: append([]int64(nil), r.alloc...)}, nil
}

func randomProviders(r *rand.Rand, n, full uint64) []ProviderHints {
	k := r.Intn(4)
	out := make([]ProviderHints, 0, k)
	for i := 0; i < k; i++ {
		switch r.Intn(3) {
		case 0:
			out = append(out, ProviderHints{NoPreference: true})
		case 1:
			out = append(out, ProviderHints{Hints: []Hint{}})
		default:
			l := r.Intn(6)
			hs := make([]Hint, 0, l)
			for j := 0; j < l; j++ {
				mask := uint64(1 + r.Intn(int(full)))
				hs = append(hs, Hint{Mask: mask, Preferred: r.Intn(2) == 0})
			}
			out = append(out, ProviderHints{Hints: hs})
		}
	}
	return out
}

// 与暴力枚举模拟器对照 2000 组随机操作序列：掩码、preferred、
// 准入判定、错误原因与各节点分配必须逐项一致。
func TestRandomDifferential2000(t *testing.T) {
	const cases = 2000
	policies := []Policy{PolicyNone, PolicyBestEffort, PolicyRestricted, PolicySingleNUMANode}
	r := rand.New(rand.NewSource(20261001))
	for tc := 0; tc < cases; tc++ {
		n := 1 + r.Intn(8)
		full := uint64(1<<uint(n) - 1)
		caps := make([]int64, n)
		for i := range caps {
			caps[i] = int64(1 + r.Intn(12))
		}
		policy := policies[r.Intn(len(policies))]
		m, err := NewManager(n, caps, policy)
		if err != nil {
			t.Fatalf("case %d: %v", tc, err)
		}
		sim := newBruteSim(n, caps, policy)

		active := []string{}
		steps := 6 + r.Intn(10)
		t.Logf("CASE %d policy=%s n=%d caps=%v", tc, policy, n, caps)
		for step := 0; step < steps; step++ {
			switch r.Intn(10) {
			case 0, 1:
				if len(active) == 0 {
					step--
					continue
				}
				idx := r.Intn(len(active))
				id := active[idx]
				e1 := m.Release(id)
				e2 := sim.release(id)
				if !sameErr(e1, e2) {
					t.Fatalf("case %d step %d release %s: %v vs %v", tc, step, id, e1, e2)
				}
				active = append(active[:idx], active[idx+1:]...)
				t.Logf("  step %d RELEASE %s => %v", step, id, e1)
			case 2:
				id := []string{"", "x", "new"}[r.Intn(3)]
				_, e1 := m.Query(id)
				_, e2 := sim.query(id)
				if !sameErr(e1, e2) {
					t.Fatalf("case %d step %d query %s: %v vs %v", tc, step, id, e1, e2)
				}
			default:
				id := "c" + string(rune('a'+r.Intn(6)))
				req := int64(1 + r.Intn(18))
				// 小概率构造非法 req 或非法掩码以覆盖参数拒绝路径。
				var provs []ProviderHints
				if r.Intn(12) == 0 {
					req = int64(1_000_000_000_001)
				}
				provs = randomProviders(r, uint64(n), full)
				if r.Intn(14) == 0 {
					provs = append(provs, ProviderHints{Hints: []Hint{{Mask: full + 1}}})
				}
				r1, e1 := m.Admit(id, req, provs)
				r2, e2 := sim.admit(id, req, provs)
				t.Logf("  step %d ADMIT id=%s req=%d provs=%v => real=%+v(%v) sim=%+v(%v)",
					step, id, req, provs, resLog(r1), e1, resLog(r2), e2)
				if !sameErr(e1, e2) {
					t.Fatalf("case %d step %d admit error: %v vs %v", tc, step, e1, e2)
				}
				if e1 == nil {
					if r1.Mask != r2.Mask || r1.Preferred != r2.Preferred || !eqAlloc(r1.Allocation, r2.Allocation) {
						t.Fatalf("case %d step %d result mismatch: %+v vs %+v", tc, step, r1, r2)
					}
					active = append(active, id)
				}
			}

			// 每个步骤后比较全部已登记容器与空闲池。
			m.mu.Lock()
			if len(m.records) != len(sim.recs) {
				t.Fatalf("case %d record count %d vs %d", tc, len(m.records), len(sim.recs))
			}
			for id, rec := range m.records {
				sr, ok := sim.recs[id]
				if !ok {
					t.Fatalf("case %d missing sim record %s", tc, id)
				}
				if rec.mask != sr.mask || rec.pref != sr.pref || !eqAlloc(rec.alloc, sr.alloc) {
					t.Fatalf("case %d record %s mismatch: %+v vs %+v", tc, id, rec, sr)
				}
			}
			if !eqAlloc(m.free, sim.free) {
				t.Fatalf("case %d free pool mismatch: %v vs %v", tc, m.free, sim.free)
			}
			m.mu.Unlock()
		}
	}
}

func sameErr(a, b error) bool { return a == b }

func resLog(r *Result) string {
	if r == nil {
		return "<nil>"
	}
	return formatRes(r)
}

func formatRes(r *Result) string {
	return fmt.Sprintf("mask=%b pref=%v alloc=%v", r.Mask, r.Preferred, r.Allocation)
}
