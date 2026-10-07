package ontology

import (
	"math/rand"
	"reflect"
	"testing"
)

// scriptedReq 描述一个请求及其确定性串行调度。
// hooks[k] 规定其第 k 次尝试“重读之后—提交之前”窗口里串行插入的链接变更。
// 每个请求使用独立目标实例，因此脚本间无跨请求状态耦合，
// 生成器可以精确预测每次插队后的占用状态。
type scriptedReq struct {
	other string
	add   bool
	hooks map[int][]Op
}

func (r scriptedReq) hooksFlat() []Op {
	var out []Op
	for _, ops := range r.hooks {
		out = append(out, ops...)
	}
	return out
}

func targetName(i int) string { return "x" + itoa(i) }

func normalizeResult(r *Result) (bool, uint64, Code, []AttemptRecord) {
	code := Code("")
	if r.Reject != nil {
		code = r.Reject.Code
	}
	return r.Committed, r.Version, code, r.Attempts
}

func assertResultEqual(t *testing.T, idx int, got, want *Result) {
	t.Helper()
	c1, v1, code1, a1 := normalizeResult(got)
	c2, v2, code2, a2 := normalizeResult(want)
	if c1 != c2 || v1 != v2 || code1 != code2 {
		t.Fatalf("req %d summary mismatch: got(%v,%d,%s) want(%v,%d,%s)",
			idx, c1, v1, code1, c2, v2, code2)
	}
	if len(a1) != len(a2) {
		t.Fatalf("req %d attempt count mismatch: %d vs %d", idx, len(a1), len(a2))
	}
	for i := range a1 {
		x, y := a1[i], a2[i]
		if x.Index != y.Index || x.VersionRead != y.VersionRead || x.Outcome != y.Outcome {
			t.Fatalf("req %d attempt %d header mismatch: %+v vs %+v", idx, i+1, x, y)
		}
		if !reflect.DeepEqual(x.Verdicts, y.Verdicts) {
			t.Fatalf("req %d attempt %d verdicts mismatch:\n got %+v\nwant %+v", idx, i+1, x.Verdicts, y.Verdicts)
		}
		if !reflect.DeepEqual(x.Snapshot, y.Snapshot) {
			t.Fatalf("req %d attempt %d snapshot mismatch:\n got %+v\nwant %+v", idx, i+1, x.Snapshot, y.Snapshot)
		}
	}
}

// targetToggleHook 针对特定实例的确定性插队钩子。
type targetToggleHook struct {
	store    *Store
	target   string
	schedule map[int][]Op
}

func (h *targetToggleHook) hook(_ Request, attempt int, _ uint64) {
	if ops, ok := h.schedule[attempt]; ok && len(ops) > 0 {
		if ok2, err := h.store.commitRaider(h.target, ops); err != nil || !ok2 {
			panic("illegal scripted raid (must be cardinality-valid)")
		}
	}
}

// setupWorlds 为每个请求建立独立目标实例；seeded[i] 为该实例的初始占用者。
func setupWorlds(t *testing.T, n int, seeded map[int]string) (*Store, *naiveWorld, map[string]bool) {
	t.Helper()
	st := NewStore()
	if err := st.RegisterLinkType(LinkType{ID: testLinkType, CardinalityA: &Cardinality{Max: 1}}); err != nil {
		t.Fatal(err)
	}
	w := newNaiveWorld()
	w.registerType(LinkType{ID: testLinkType, CardinalityA: &Cardinality{Max: 1}})
	others := map[string]bool{}
	for i := 0; i < n; i++ {
		tg := targetName(i)
		if err := st.CreateInstance(tg); err != nil {
			t.Fatal(err)
		}
		w.create(tg)
		if occ, ok := seeded[i]; ok {
			others[occ] = true
			if !st.InstanceExists(occ) {
				if err := st.CreateInstance(occ); err != nil {
					t.Fatal(err)
				}
			}
			if _, exists := w.nlinks[occ]; !exists {
				w.create(occ)
			}
			if ok2, err := st.commitRaider(tg, []Op{{TypeID: testLinkType, Side: SideA, Other: occ, Add: true}}); err != nil || !ok2 {
				t.Fatal(err)
			}
			w.seedLink(Link{TypeID: testLinkType, A: tg, B: occ})
		}
	}
	return st, w, others
}

func snapshotStore(st *Store, id string) (uint64, []Link, map[ConstraintKey]int) {
	v, _ := st.readVersion(id)
	ls, _ := st.LinkSnapshot(id)
	counts := map[ConstraintKey]int{}
	st.catalogMu.RLock()
	for _, lt := range st.types {
		for _, side := range []Side{SideA, SideB} {
			if lt.Constraint(side) != nil {
				k := ConstraintKey{TypeID: lt.ID, Side: side}
				c, _ := st.Counter(id, k)
				counts[k] = c
			}
		}
	}
	st.catalogMu.RUnlock()
	return v, ls, counts
}

func snapshotNaive(w *naiveWorld, id string) (uint64, []Link, map[ConstraintKey]int) {
	counts := map[ConstraintKey]int{}
	for k, v := range w.ncount[id] {
		counts[k] = v
	}
	return w.nver[id], w.snapshot(id), counts
}

func runDifferentialOnce(t *testing.T, seed int64, maxAttempts int, reqs []scriptedReq, seeded map[int]string) {
	t.Helper()
	t.Logf("SEED=%d seeded=%+v reqs=%+v", seed, seeded, reqs)

	st, w, others := setupWorlds(t, len(reqs), seeded)
	for _, r := range reqs {
		others[r.other] = true
		for _, op := range r.hooksFlat() {
			others[op.Other] = true
		}
	}
	for o := range others {
		if !st.InstanceExists(o) {
			if err := st.CreateInstance(o); err != nil {
				t.Fatal(err)
			}
		}
		if _, ok := w.nlinks[o]; !ok {
			w.create(o)
		}
	}

	for i, rq := range reqs {
		target := targetName(i)
		hook := &targetToggleHook{store: st, target: target, schedule: rq.hooks}
		eng := NewEngine(st, maxAttempts, hook.hook)
		req := Request{Instance: target, Baseline: 0, Ops: []Op{{
			TypeID: testLinkType, Side: SideA, Other: rq.other, Add: rq.add,
		}}}

		got, err := eng.Update(req)
		if err != nil {
			t.Fatalf("seed %d req %d engine error: %v", seed, i, err)
		}
		want := naiveRun(w, req, maxAttempts, func(_ Request, attempt int, _ uint64) {
			w.raiderApply(target, rq.hooks[attempt])
		})

		assertResultEqual(t, i, got, want)

		gv, gl, gc := snapshotStore(st, target)
		nv, nl, nc := snapshotNaive(w, target)
		if gv != nv || !reflect.DeepEqual(gl, nl) || !reflect.DeepEqual(gc, nc) {
			t.Fatalf("seed %d req %d state mismatch:\n got(v=%d,%+v,%+v)\nwant(v=%d,%+v,%+v)",
				seed, i, gv, gl, gc, nv, nl, nc)
		}
	}
}

// TestDifferentialRandomized 随机生成“冲突、抢占、空出、反复、耗尽”脚本，
// 要求生产引擎与朴素串行重试模型在每次尝试的读取状态、判定依据、
// 结局以及逐步可见状态上完全一致（即存在同一等价串行顺序）。
func TestDifferentialRandomized(t *testing.T) {
	const maxAttempts = 4
	pool := []string{"g0", "g1", "g2", "g3", "g4", "g5"}

	for seed := int64(0); seed < 400; seed++ {
		rng := rand.New(rand.NewSource(seed))
		nReq := 1 + rng.Intn(4)
		reqs := make([]scriptedReq, nReq)
		seeded := map[int]string{}

		for i := range reqs {
			target := targetName(i)
			// 生成器为该实例同步维护占用者与版本，保证每个插队动作合法。
			occupant := ""
			if rng.Intn(2) == 1 {
				occupant = pool[rng.Intn(len(pool))]
				seeded[i] = occupant
			}
			rq := scriptedReq{
				other: pool[rng.Intn(len(pool))],
				add:   true,
				hooks: map[int][]Op{},
			}
			for a := 1; a < maxAttempts; a++ {
				switch rng.Intn(3) {
				case 0:
					// 不插队
				case 1:
					// 原子替换占用者（满→满），始终合法。
					if occupant != "" {
						who := pool[rng.Intn(len(pool))]
						rq.hooks[a] = []Op{
							{TypeID: testLinkType, Side: SideA, Other: occupant, Add: false},
							{TypeID: testLinkType, Side: SideA, Other: who, Add: true},
						}
						occupant = who
					}
				case 2:
					// 原子空出名额（满→空）。
					if occupant != "" {
						rq.hooks[a] = []Op{{TypeID: testLinkType, Side: SideA, Other: occupant, Add: false}}
						occupant = ""
					}
				}
			}
			// 无插队时，生成器视角的请求结果：
			// 若名额空闲则请求自身成功占用；若已满则第二次尝试（基线追平）被基数拒绝。
			_ = target
			reqs[i] = rq
		}

		runDifferentialOnce(t, seed, maxAttempts, reqs, seeded)
	}
}

// 固定脚本：空出名额后成功。
func TestDifferentialFixedFreeOneSlot(t *testing.T) {
	reqs := []scriptedReq{{
		other: "g9",
		add:   true,
		hooks: map[int][]Op{
			1: {{TypeID: testLinkType, Side: SideA, Other: "g0", Add: false}},
		},
	}}
	runDifferentialOnce(t, -1, 3, reqs, map[int]string{0: "g0"})
}

// 固定脚本：每次尝试都被原子替换占用，预算恰好耗尽。
func TestDifferentialFixedExhausted(t *testing.T) {
	reqs := []scriptedReq{{
		other: "g9",
		add:   true,
		hooks: map[int][]Op{
			1: {
				{TypeID: testLinkType, Side: SideA, Other: "g0", Add: false},
				{TypeID: testLinkType, Side: SideA, Other: "g1", Add: true},
			},
			2: {
				{TypeID: testLinkType, Side: SideA, Other: "g1", Add: false},
				{TypeID: testLinkType, Side: SideA, Other: "g2", Add: true},
			},
			3: {
				{TypeID: testLinkType, Side: SideA, Other: "g2", Add: false},
				{TypeID: testLinkType, Side: SideA, Other: "g3", Add: true},
			},
		},
	}}
	runDifferentialOnce(t, -2, 3, reqs, map[int]string{0: "g0"})
}
