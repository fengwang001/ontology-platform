package numa

import (
	"fmt"
	"math/rand"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// simAlloc 是完全独立于生产代码、按规格逐步写就的暴力模拟状态。
type simAlloc struct {
	n      int
	policy Policy
	caps   []int64
	used   map[string][]int64
	log    *strings.Builder
}

func newSim(n int, caps []int64, policy Policy) *simAlloc {
	return &simAlloc{n: n, policy: policy, caps: append([]int64(nil), caps...),
		used: map[string][]int64{}, log: &strings.Builder{}}
}

func simBits(m uint16) int {
	b := 0
	for m != 0 {
		b += int(m & 1)
		m >>= 1
	}
	return b
}

type simHint struct {
	mask uint16
	pref bool
}

// simResult 记录模拟的完整判定。
type simResult struct {
	errReason RejectReason
	mask      uint16
	pref      bool
	cpus      []int64
}

func (s *simAlloc) free() []int64 {
	f := make([]int64, s.n)
	copy(f, s.caps)
	for _, u := range s.used {
		for i := range f {
			f[i] -= u[i]
		}
	}
	return f
}

func simBetter(a, b simHint) bool {
	if a.pref != b.pref {
		return a.pref
	}
	if simBits(a.mask) != simBits(b.mask) {
		return simBits(a.mask) < simBits(b.mask)
	}
	return a.mask < b.mask
}

func simAllocate(free []int64, mask uint16, req int64) []int64 {
	cpus := make([]int64, len(free))
	need := req
	for i := range free {
		if mask&(uint16(1)<<uint(i)) != 0 {
			t := free[i]
			if t > need {
				t = need
			}
			cpus[i] = t
			need -= t
			if need == 0 {
				return cpus
			}
		}
	}
	for i := range free {
		if mask&(uint16(1)<<uint(i)) == 0 {
			t := free[i]
			if t > need {
				t = need
			}
			cpus[i] += t
			need -= t
			if need == 0 {
				return cpus
			}
		}
	}
	return cpus
}

func simFmtHints(n int, hs []simHint) string {
	parts := make([]string, 0, len(hs))
	for _, h := range hs {
		parts = append(parts, fmt.Sprintf("(%0*b,%v)", n, h.mask, h.pref))
	}
	return "[" + strings.Join(parts, " ") + "]"
}

func (s *simAlloc) release(id string) RejectReason {
	if _, ok := s.used[id]; !ok {
		return ReasonContainerMissing
	}
	delete(s.used, id)
	return ""
}

func (s *simAlloc) admit(id string, req int64, providers []*[]Hint) simResult {
	full := uint16(1)<<uint(s.n) - 1
	fmt.Fprintf(s.log, "ADMIT id=%q req=%d providers=%d\n", id, req, len(providers))

	badMask := false
	for _, p := range providers {
		if p == nil {
			continue
		}
		for _, h := range *p {
			if h.Mask == 0 || h.Mask > full {
				badMask = true
			}
		}
	}
	switch {
	case id == "":
		fmt.Fprintln(s.log, "  reject invalid-argument: empty id")
		return simResult{errReason: ReasonInvalidArgument}
	case req < 1 || req > 1_000_000_000_000:
		fmt.Fprintln(s.log, "  reject invalid-argument: req range")
		return simResult{errReason: ReasonInvalidArgument}
	case badMask:
		fmt.Fprintln(s.log, "  reject invalid-argument: mask")
		return simResult{errReason: ReasonInvalidArgument}
	}
	if _, ok := s.used[id]; ok {
		fmt.Fprintln(s.log, "  reject container-exists")
		return simResult{errReason: ReasonContainerExists}
	}

	free := s.free()
	var total int64
	for _, v := range free {
		total += v
	}
	if total < req {
		fmt.Fprintf(s.log, "  reject insufficient-cpu total=%d\n", total)
		return simResult{errReason: ReasonInsufficientCPU}
	}

	if s.policy == PolicyNone {
		c := simAllocate(free, full, req)
		s.used[id] = c
		fmt.Fprintf(s.log, "  none admit mask=%0*b cpus=%v\n", s.n, full, c)
		return simResult{mask: full, pref: true, cpus: c}
	}

	// 内置提示：枚举所有非空掩码，求 Zc 后全部可行掩码均产出。
	var builtin []simHint
	zc := s.n + 1
	feasible := map[uint16]bool{}
	for m := uint16(1); m <= full; m++ {
		var sum int64
		for i := 0; i < s.n; i++ {
			if m&(uint16(1)<<uint(i)) != 0 {
				sum += free[i]
			}
		}
		if sum >= req {
			feasible[m] = true
			if simBits(m) < zc {
				zc = simBits(m)
			}
		}
	}
	if zc <= s.n {
		for m := uint16(1); m <= full; m++ {
			if feasible[m] {
				builtin = append(builtin, simHint{m, simBits(m) == zc})
			}
		}
	}
	fmt.Fprintf(s.log, "  builtin(zc=%d): %s\n", zc, simFmtHints(s.n, builtin))

	normalizeOne := func(list []simHint) []simHint {
		if len(list) == 0 {
			return []simHint{{full, false}}
		}
		if s.policy == PolicySingleNUMANode {
			var kept []simHint
			for _, h := range list {
				if simBits(h.mask) == 1 {
					kept = append(kept, h)
				}
			}
			if len(kept) == 0 {
				return []simHint{{full, false}}
			}
			list = kept
		}
		return list
	}

	lists := [][]simHint{normalizeOne(builtin)}
	for _, p := range providers {
		if p == nil {
			lists = append(lists, []simHint{{full, true}})
		} else {
			lh := make([]simHint, 0, len(*p))
			for _, h := range *p {
				lh = append(lh, simHint{h.Mask, h.Preferred})
			}
			lists = append(lists, normalizeOne(lh))
		}
	}

	combos := 1
	lens := make([]int, len(lists))
	for i, l := range lists {
		lens[i] = len(l)
		combos *= len(l)
	}
	if combos > 100_000 {
		fmt.Fprintf(s.log, "  reject too-many-combinations combos=%d lens=%v\n", combos, lens)
		return simResult{errReason: ReasonTooManyCombos}
	}

	// 暴力笛卡尔积（里程表）。
	type cand struct {
		mask uint16
		all  bool
	}
	var cands []cand
	idx := make([]int, len(lists))
	for {
		mask := full
		all := true
		for i := range lists {
			mask &= lists[i][idx[i]].mask
			all = all && lists[i][idx[i]].pref
		}
		if mask != 0 {
			cands = append(cands, cand{mask, all})
		}
		k := len(lists) - 1
		for k >= 0 {
			idx[k]++
			if idx[k] < len(lists[k]) {
				break
			}
			idx[k] = 0
			k--
		}
		if k < 0 {
			break
		}
	}

	best := simHint{full, false}
	if len(cands) > 0 {
		z := 99
		haveAll := false
		for _, c := range cands {
			if c.all {
				haveAll = true
				if simBits(c.mask) < z {
					z = simBits(c.mask)
				}
			}
		}
		for _, c := range cands {
			ch := simHint{c.mask, haveAll && c.all && simBits(c.mask) == z}
			if simBetter(ch, best) {
				best = ch
			}
		}
	}
	fmt.Fprintf(s.log, "  candidates=%d best=%0*b pref=%v\n", len(cands), s.n, best.mask, best.pref)

	if !best.pref && (s.policy == PolicyRestricted || s.policy == PolicySingleNUMANode) {
		fmt.Fprintln(s.log, "  reject hint-not-satisfied")
		return simResult{errReason: ReasonHintNotSatisfied}
	}

	c := simAllocate(free, best.mask, req)
	s.used[id] = c
	fmt.Fprintf(s.log, "  admit mask=%0*b pref=%v cpus=%v\n", s.n, best.mask, best.pref, c)
	return simResult{mask: best.mask, pref: best.pref, cpus: c}
}

// TestRandomAgainstBruteForce 对 2000 组随机配置与操作序列与暴力模拟逐步对照，
// 完整输入、输出与判定依据写入日志文件。
func TestRandomAgainstBruteForce(t *testing.T) {
	if testing.Short() {
		t.Skip()
	}
	logPath := filepath.Join(os.TempDir(), "numa-crosscheck.log")
	logFile, err := os.Create(logPath)
	if err != nil {
		t.Fatal(err)
	}
	defer logFile.Close()
	t.Logf("cross-check log: %s", logPath)

	rng := rand.New(rand.NewSource(20261001))
	policies := []Policy{PolicyNone, PolicyBestEffort, PolicyRestricted, PolicySingleNUMANode}

	for iter := 0; iter < 2000; iter++ {
		n := 1 + rng.Intn(8)
		caps := make([]int64, n)
		for i := range caps {
			caps[i] = int64(1 + rng.Intn(12))
		}
		policy := policies[rng.Intn(len(policies))]
		m, err := NewManager(n, caps, policy)
		if err != nil {
			t.Fatalf("iter %d: %v", iter, err)
		}
		sim := newSim(n, caps, policy)
		fmt.Fprintf(logFile, "=== iter %d n=%d caps=%v policy=%s ===\n", iter, n, caps, policy)

		live := map[string]bool{}
		nops := 4 + rng.Intn(6)
		for opi := 0; opi < nops; opi++ {
			if len(live) > 0 && rng.Intn(10) < 3 {
				var id string
				for k := range live {
					id = k
					break
				}
				delete(live, id)
				got := reason(m.Release(id))
				want := sim.release(id)
				fmt.Fprintf(logFile, "RELEASE id=%q -> got=%s want=%s\n", id, got, want)
				if got != want {
					t.Fatalf("iter %d release %s: got=%s want=%s", iter, id, got, want)
				}
				continue
			}

			id := fmt.Sprintf("c%d", rng.Intn(8))
			req := int64(1 + rng.Intn(20))
			if rng.Intn(15) == 0 {
				req = 1_000_000_000_000 + 1
			}
			nprov := rng.Intn(3)
			var sprov []*[]Hint
			var aprov []Provider
			for p := 0; p < nprov; p++ {
				switch rng.Intn(3) {
				case 0:
					sprov = append(sprov, nil)
					aprov = append(aprov, nil)
				case 1:
					empty := []Hint{}
					sprov = append(sprov, &empty)
					aprov = append(aprov, &empty)
				default:
					lh := 1 + rng.Intn(4)
					hs := make([]Hint, lh)
					for i := range hs {
						top := int(uint16(1)<<uint(n)) - 1
						hs[i] = Hint{Mask: uint16(1 + rng.Intn(top)), Preferred: rng.Intn(2) == 0}
					}
					if rng.Intn(20) == 0 {
						hs[0] = Hint{Mask: uint16(1) << uint(n), Preferred: false}
					}
					sprov = append(sprov, &hs)
					aprov = append(aprov, &hs)
				}
			}

			gotR, gotErr := m.Admit(id, req, aprov)
			want := sim.admit(id, req, sprov)
			fmt.Fprint(logFile, sim.log.String())
			sim.log.Reset()

			gotReason := reason(gotErr)
			if gotReason != want.errReason {
				fmt.Fprintf(logFile, "MISMATCH reason got=%s want=%s\n", gotReason, want.errReason)
				t.Fatalf("iter %d admit id=%s req=%d: reason got=%s want=%s (see %s)",
					iter, id, req, gotReason, want.errReason, logPath)
			}
			if gotErr == nil {
				if gotR.Mask != want.mask || gotR.Preferred != want.pref || !eqCPUs(gotR.CPUs, want.cpus) {
					fmt.Fprintf(logFile, "MISMATCH result got=(%0*b,%v,%v) want=(%0*b,%v,%v)\n",
						n, gotR.Mask, gotR.Preferred, gotR.CPUs, n, want.mask, want.pref, want.cpus)
					t.Fatalf("iter %d admit mismatch (see %s)", iter, logPath)
				}
				var sum int64
				for _, c := range gotR.CPUs {
					sum += c
				}
				if sum != req || gotR.Mask == 0 || gotR.Mask > uint16(1)<<uint(n)-1 {
					t.Fatalf("iter %d invariant violation", iter)
				}
				live[id] = true
			}

			// 每步交叉校验各节点累计用量不超 cap。
			used := make([]int64, n)
			for id := range live {
				q, qerr := m.Query(id)
				if qerr != nil {
					t.Fatalf("iter %d query %s: %v", iter, id, qerr)
				}
				for i, c := range q.CPUs {
					used[i] += c
				}
			}
			for i, u := range used {
				if u > caps[i] {
					t.Fatalf("iter %d node %d overcommitted %d > %d", iter, i, u, caps[i])
				}
			}
		}
	}
}
