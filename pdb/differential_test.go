package pdb

import (
	"math/rand"
	"reflect"
	"sort"
	"testing"
	"time"
)

// runOne applies op to both implementations and returns their normalized
// results. For query ops the service status is returned too.
func runOne(svc *Service, ref *naiveModel, op diffOp, now time.Time) (naiveResult, naiveResult) {
	var rs, rr naiveResult
	rs.index, rr.index = -1, -1
	svcErrToRes := func(err error) naiveResult {
		if err == nil {
			return naiveResult{index: -1}
		}
		de := err.(*DecisionError)
		return naiveResult{reason: de.Reason, index: de.Index}
	}
	switch op.kind {
	case opUpsertPod:
		rs = svcErrToRes(svc.UpsertPod(op.pod, now))
		rr = ref.upsertPod(op.pod, now)
	case opDeletePod:
		rs = svcErrToRes(svc.DeletePod(op.ref, now))
		rr = ref.deletePod(op.ref, now)
	case opUpsertBudget:
		rs = svcErrToRes(svc.UpsertBudget(op.bud, now))
		rr = ref.upsertBudget(op.bud, now)
	case opDeleteBudget:
		rs = svcErrToRes(svc.DeleteBudget(op.nn, now))
		rr = ref.deleteBudget(op.nn, now)
	case opEvict:
		rs = svcErrToRes(svc.Evict(op.ref, now))
		rr = ref.evict(op.ref, now)
	case opBatch:
		rs = svcErrToRes(svc.EvictBatch(op.batch, now))
		rr = ref.batch(op.batch, now)
	case opConfirm:
		rs = svcErrToRes(svc.Confirm(op.ref, now))
		rr = ref.confirm(op.ref, now, false)
	case opCancel:
		rs = svcErrToRes(svc.Cancel(op.ref, now))
		rr = ref.confirm(op.ref, now, true)
	case opQuery:
		st, err := svc.Budget(op.nn.Namespace, op.nn.Name, now)
		if err == nil {
			rs = naiveResult{index: -1, status: st, found: true}
		} else {
			de := err.(*DecisionError)
			rs = naiveResult{reason: de.Reason, index: -1}
		}
		rr = ref.budget(op.nn.Namespace, op.nn.Name, now)
	}
	return rs, rr
}

type fullState struct {
	pods     map[string]snapPod
	budgets  map[string]BudgetSpec
	evicting map[string]snapEv
	statuses map[string]BudgetStatus
}

type snapPod struct {
	phase  Phase
	ready  bool
	labels map[string]string
}

type snapEv struct {
	deadline time.Time
	wasReady bool
}

func takeSnap(t *testing.T, svc *Service, ref *naiveModel, now time.Time) (fullState, fullState) {
	sA := fullState{
		pods: map[string]snapPod{}, budgets: map[string]BudgetSpec{},
		evicting: map[string]snapEv{}, statuses: map[string]BudgetStatus{},
	}
	sB := fullState{
		pods: map[string]snapPod{}, budgets: map[string]BudgetSpec{},
		evicting: map[string]snapEv{}, statuses: map[string]BudgetStatus{},
	}
	svc.mu.Lock()
	for uid, pe := range svc.pods["ns"] {
		labels := map[string]string{}
		for k, v := range pe.pod.Labels {
			labels[k] = v
		}
		sA.pods[uid] = snapPod{pe.pod.Phase, pe.pod.Ready, labels}
		if pe.evicting != nil {
			sA.evicting[uid] = snapEv{pe.evicting.deadline, pe.evicting.wasReady}
		}
	}
	for name, b := range svc.budgets["ns"] {
		sA.budgets[name] = b.spec
	}
	svc.mu.Unlock()

	for r, p := range ref.pods {
		if r.Namespace != "ns" {
			continue
		}
		labels := map[string]string{}
		for k, v := range p.labels {
			labels[k] = v
		}
		sB.pods[r.UID] = snapPod{p.phase, p.ready, labels}
		if e, ok := ref.evicting[r]; ok {
			sB.evicting[r.UID] = snapEv{e.deadline, e.wasReady}
		}
	}
	for nn, b := range ref.budgets {
		if nn.Namespace == "ns" {
			sB.budgets[nn.Name] = b
		}
	}

	allNames := map[string]bool{}
	for n := range sA.budgets {
		allNames[n] = true
	}
	for n := range sB.budgets {
		allNames[n] = true
	}
	names := make([]string, 0, len(allNames))
	for n := range allNames {
		names = append(names, n)
	}
	sort.Strings(names)
	for _, name := range names {
		st, err := svc.Budget("ns", name, now)
		if err != nil {
			t.Fatalf("snapshot svc query %s: %v", name, err)
		}
		st.MatchedPods = sortedRefs(st.MatchedPods)
		sA.statuses[name] = st

		// ref.status() is pure (no expiry/clock effect); ensure the naive
		// clock matches `now`, which the public budget() call would set.
		ref.clock = now
		st2, ok := ref.status(NamespacedName{"ns", name})
		if !ok {
			t.Fatalf("snapshot naive query %s missing", name)
		}
		st2.MatchedPods = sortedRefs(st2.MatchedPods)
		sB.statuses[name] = st2
	}
	return sA, sB
}

func sortedRefs(in []PodRef) []PodRef {
	out := append([]PodRef(nil), in...)
	sort.Slice(out, func(i, j int) bool {
		if out[i].Namespace != out[j].Namespace {
			return out[i].Namespace < out[j].Namespace
		}
		return out[i].UID < out[j].UID
	})
	return out
}

func resultName(r naiveResult) string {
	if r.found {
		return "ALLOW(status)"
	}
	if r.reason == 0 {
		return "ALLOW"
	}
	return "DENY(" + ReasonName(r.reason) + ")"
}

// TestRandomDifferential drives both implementations through identical long
// randomized operation streams and requires identical result category,
// offending batch index, budget status and full state after every step.
func TestRandomDifferential(t *testing.T) {
	if testing.Short() {
		t.Skip("differential fuzzing skipped in -short")
	}
	lg := newOpLogger(t)
	const graceSecs = 10
	for seed := int64(1); seed <= 12; seed++ {
		rng := rand.New(rand.NewSource(seed))
		ops := generateOps(rng, 400)
		svc := New(graceSecs * 1e9)
		ref := newNaive(graceSecs * 1e9)
		for i, op := range ops {
			now := time.Unix(op.now, 0)
			rs, rr := runOne(svc, ref, op, now)
			if rs.reason != rr.reason ||
				(op.kind == opBatch && (rs.reason != 0 || rr.reason != 0) && rs.index != rr.index) {
				t.Fatalf("seed=%d step=%d %s now=%d: svc=%s idx=%d naive=%s idx=%d",
					seed, i, op.name(), op.now, ReasonName(rs.reason), rs.index,
					ReasonName(rr.reason), rr.index)
			}
			if rs.found || rr.found {
				if !(rs.found && rr.found) || !statusEqual(rs.status, rr.status) {
					t.Fatalf("seed=%d step=%d query: svc=%+v naive=%+v", seed, i, rs, rr)
				}
			}
			// A rollback op leaves the accepted clock untouched; snapshot
			// queries must use the persisted clock, not the rejected time.
			snapTime := now
			if svc.clock.After(snapTime) {
				snapTime = svc.clock
			}
			if ref.clock.After(snapTime) {
				snapTime = ref.clock
			}
			sA, sB := takeSnap(t, svc, ref, snapTime)
			if !reflect.DeepEqual(sA, sB) {
				t.Fatalf("seed=%d step=%d %s state mismatch\n svc: pods=%v ev=%v budgets=%v statuses=%v\n ref: pods=%v ev=%v budgets=%v statuses=%v",
					seed, i, op.name(), sA.pods, sA.evicting, sA.budgets, sA.statuses,
					sB.pods, sB.evicting, sB.budgets, sB.statuses)
			}
			lg.log("seed=%02d step=%03d now=%03d %-14s in=%-12q => %-26s | pods=%d evicting=%d budgets=%d statuses=match",
				seed, i, op.now, op.name(), opInput(op), resultName(rs),
				len(sA.pods), len(sA.evicting), len(sA.budgets))
		}
	}
}

func statusEqual(a, b BudgetStatus) bool {
	return a.Expected == b.Expected &&
		a.CurrentReady == b.CurrentReady &&
		a.RequiredReady == b.RequiredReady &&
		a.DisruptionAllowed == b.DisruptionAllowed &&
		reflect.DeepEqual(sortedRefs(a.MatchedPods), sortedRefs(b.MatchedPods))
}
