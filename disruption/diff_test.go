package disruption

import (
	"fmt"
	"testing"
)

type models struct {
	idx   *Service
	naive *NaiveService
}

func newModels() *models { return &models{idx: NewService(), naive: NewNaiveService()} }

func compareErr(a, b error) string {
	ka, oka := KindOf(a)
	kb, okb := KindOf(b)
	if oka != okb || ka != kb {
		return fmt.Sprintf("error kind mismatch: indexed=%v(%v) naive=%v(%v)", a, oka, b, okb)
	}
	if oka {
		var pa, pb PodID
		var ha, hb bool
		var ae *AdjudicationError
		if asAdj(a, &ae) {
			pa, ha = ae.Pod, ae.HasPod
		}
		var be *AdjudicationError
		if asAdj(b, &be) {
			pb, hb = be.Pod, be.HasPod
		}
		if ha != hb || (ha && pa != pb) {
			return fmt.Sprintf("error pod mismatch: indexed=%+v(%t) naive=%+v(%t)", pa, ha, pb, hb)
		}
	}
	return ""
}

func asAdj(err error, target **AdjudicationError) bool {
	*target = nil
	if ae, ok := err.(*AdjudicationError); ok {
		*target = ae
		return true
	}
	return false
}

func (m *models) snapshot(w *diffWorld) snapshot {
	snap := snapshot{quota: map[BudgetID]BudgetStatus{}}
	for _, ns := range w.namespaces {
		for _, n := range w.podNames {
			id := PodID{ns, n}
			r, ok := m.naive.PodReady(id)
			_ = r
			_ = ok
			ri, oki := m.idx.PodReady(id)
			rn, okn := m.naive.PodReady(id)
			if oki != okn || ri != rn {
				panic(fmt.Sprintf("pod presence/readiness mismatch %s: idx(%t,%t) naive(%t,%t)", id, ri, oki, rn, okn))
			}
			ei, en := m.idx.IsEvicting(id), m.naive.IsEvicting(id)
			if ei != en {
				panic(fmt.Sprintf("evicting mismatch %s: idx=%t naive=%t", id, ei, en))
			}
		}
	}
	for _, ns := range w.namespaces {
		for _, bn := range w.budNames {
			id := BudgetID{ns, bn}
			si, ei := m.idx.BudgetQuota(w.tick, id)
			sn, en := m.naive.BudgetQuota(w.tick, id)
			if (ei == nil) != (en == nil) {
				panic(fmt.Sprintf("budget presence mismatch %s: idx err=%v naive err=%v", id, ei, en))
			}
			if ei == nil && si != sn {
				panic(fmt.Sprintf("quota mismatch %s: idx=%+v naive=%+v", id, si, sn))
			}
			snap.quota[id] = si
		}
	}
	snap.now = w.tick
	return snap
}

// TestRandomDifferential drives many random operation sequences through both
// implementations and compares: error kind (+offending pod), decision, full
// per-pod presence/readiness/evicting state, and every budget quota. Each op
// logs inputs, both outputs and the decision basis.
func TestRandomDifferential(t *testing.T) {
	const (
		iterations = 60
		steps      = 600
	)
	for it := 0; it < iterations; it++ {
		seed := int64(1000 + it*37)
		w := newDiffWorld(seed)
		m := newModels()
		log := newOpLogger(t)
		// Seed a few pods/budgets so early decisions are meaningful.
		for i := 0; i < 6; i++ {
			p := pod(w.id().Namespace, w.podNames[w.rng.Intn(len(w.podNames))], w.phase(), w.rng.Intn(2) == 0, w.labels())
			now := w.adv()
			e1 := m.idx.UpsertPod(now, p)
			e2 := m.naive.UpsertPod(now, p)
			if d := compareErr(e1, e2); d != "" {
				t.Fatalf("seed=%d step=seed: %s", seed, d)
			}
		}
		for st := 0; st < steps; st++ {
			now := w.adv()
			switch w.rng.Intn(13) {
			case 0:
				p := Pod{ID: w.id(), Labels: w.labels(), Phase: w.phase(), Ready: w.rng.Intn(2) == 0}
				e1 := m.idx.UpsertPod(now, p)
				e2 := m.naive.UpsertPod(now, p)
				log.logf("UpsertPod input=%+v ready=%t phase=%s => idx=%v naive=%v (basis: incremental membership reconcile)", p.ID, p.Ready, p.Phase, e1, e2)
				if d := compareErr(e1, e2); d != "" {
					t.Fatalf("seed=%d step=%d UpsertPod: %s", seed, st, d)
				}
			case 1:
				id := w.id()
				e1 := m.idx.DeletePod(now, id)
				e2 := m.naive.DeletePod(now, id)
				log.logf("DeletePod input=%v => idx=%v naive=%v", id, e1, e2)
				if d := compareErr(e1, e2); d != "" {
					t.Fatalf("seed=%d step=%d DeletePod: %s", seed, st, d)
				}
			case 2:
				id := w.id()
				ready := w.rng.Intn(2) == 0
				e1 := m.idx.SetReady(now, id, ready)
				e2 := m.naive.SetReady(now, id, ready)
				log.logf("SetReady input=%v ready=%t => idx=%v naive=%v", id, ready, e1, e2)
				if d := compareErr(e1, e2); d != "" {
					t.Fatalf("seed=%d step=%d SetReady: %s", seed, st, d)
				}
			case 3:
				b := w.budget()
				e1 := m.idx.UpsertBudget(now, b)
				e2 := m.naive.UpsertBudget(now, b)
				log.logf("UpsertBudget input=%+v => idx=%v naive=%v", b.ID, e1, e2)
				if d := compareErr(e1, e2); d != "" {
					t.Fatalf("seed=%d step=%d UpsertBudget: %s", seed, st, d)
				}
			case 4:
				id := w.budgetID()
				e1 := m.idx.DeleteBudget(now, id)
				e2 := m.naive.DeleteBudget(now, id)
				log.logf("DeleteBudget input=%v => idx=%v naive=%v", id, e1, e2)
				if d := compareErr(e1, e2); d != "" {
					t.Fatalf("seed=%d step=%d DeleteBudget: %s", seed, st, d)
				}
			case 5, 6:
				id := w.id()
				g := w.grace()
				d1, e1 := m.idx.Evict(now, id, g)
				d2, e2 := m.naive.Evict(now, id, g)
				log.logf("Evict input=%v grace=%d => idx allowed=%t err=%v | naive allowed=%t err=%v (basis: clock->expiry->lookup->phase->evicting->conflict->allowance)",
					id, g, d1.Allowed, e1, d2.Allowed, e2)
				if d := compareErr(e1, e2); d != "" {
					t.Fatalf("seed=%d step=%d Evict: %s", seed, st, d)
				}
				if d1.Allowed != d2.Allowed {
					t.Fatalf("seed=%d step=%d Evict decision mismatch", seed, st)
				}
			case 7:
				n := 1 + w.rng.Intn(3)
				ids := make([]PodID, n)
				for i := range ids {
					ids[i] = w.id()
				}
				g := w.grace()
				d1, e1 := m.idx.EvictBatch(now, ids, g)
				d2, e2 := m.naive.EvictBatch(now, ids, g)
				log.logf("EvictBatch input=%v grace=%d => idx=%t/%v | naive=%t/%v", ids, g, d1.Allowed, e1, d2.Allowed, e2)
				if d := compareErr(e1, e2); d != "" {
					t.Fatalf("seed=%d step=%d EvictBatch: %s", seed, st, d)
				}
				if d1.Allowed != d2.Allowed {
					t.Fatalf("seed=%d step=%d batch decision mismatch", seed, st)
				}
			case 8:
				id := w.id()
				e1 := m.idx.Confirm(now, id)
				e2 := m.naive.Confirm(now, id)
				log.logf("Confirm input=%v => idx=%v naive=%v", id, e1, e2)
				if d := compareErr(e1, e2); d != "" {
					t.Fatalf("seed=%d step=%d Confirm: %s", seed, st, d)
				}
			case 9:
				id := w.id()
				e1 := m.idx.Cancel(now, id)
				e2 := m.naive.Cancel(now, id)
				log.logf("Cancel input=%v => idx=%v naive=%v", id, e1, e2)
				if d := compareErr(e1, e2); d != "" {
					t.Fatalf("seed=%d step=%d Cancel: %s", seed, st, d)
				}
			case 10:
				x1, e1 := m.idx.Expire(now)
				x2, e2 := m.naive.Expire(now)
				log.logf("Expire input=%d => idx=%v/%v naive=%v/%v", now, x1, e1, x2, e2)
				if d := compareErr(e1, e2); d != "" {
					t.Fatalf("seed=%d step=%d Expire: %s", seed, st, d)
				}
				if len(x1) != len(x2) {
					t.Fatalf("seed=%d step=%d expired set size mismatch %d vs %d", seed, st, len(x1), len(x2))
				}
			case 11:
				id := w.budgetID()
				s1, e1 := m.idx.BudgetQuota(now, id)
				s2, e2 := m.naive.BudgetQuota(now, id)
				log.logf("Quota input=%v => idx=%+v/%v naive=%+v/%v (basis: derived from current state, never stored)",
					id, s1, e1, s2, e2)
				if d := compareErr(e1, e2); d != "" {
					t.Fatalf("seed=%d step=%d Quota: %s", seed, st, d)
				}
				if e1 == nil && s1 != s2 {
					t.Fatalf("seed=%d step=%d quota %+v vs %+v", seed, st, s1, s2)
				}
			case 12:
				// Explicit clock-backtrack attempt: must match kind and leave
				// state unchanged.
				past := w.tick - Tick(1+w.rng.Intn(3))
				id := w.id()
				_, e1 := m.idx.Evict(past, id, 1)
				_, e2 := m.naive.Evict(past, id, 1)
				log.logf("ClockBacktrackProbe input=evict@%d(last=%d) %v => idx=%v naive=%v", past, w.tick, id, e1, e2)
				if d := compareErr(e1, e2); d != "" {
					t.Fatalf("seed=%d step=%d backtrack: %s (backtrack kind expected unless invalid arg)", seed, st, d)
				}
			}
			m.snapshot(w)
		}
		t.Logf("differential sequence seed=%d steps=%d completed with full state agreement", seed, steps)
	}
}
