package exec

import (
	"errors"
	"fmt"
	"math/rand"
	"testing"

	"ontology/action"
)

type scenarioStep struct {
	kind          string
	digest        string
	plat          action.Platform
	prio          int
	skip          bool
	worker        string
	props         []action.KV
	slots         int
	attempt, exit int
	infra         bool
	cancelID      int
}

var (
	diffDigests = []string{"d0", "d1", "d2", "d3", "d4", "d5"}
	diffWorkers = []string{"W0", "W1", "W2", "W3"}
	diffPlats   = []action.Platform{
		{},
		{{Key: "os", Value: "linux"}},
		{{Key: "os", Value: "win"}},
		{{Key: "os", Value: "linux"}, {Key: "arch", Value: "x86"}},
		{{Key: "arch", Value: "arm"}},
	}
	diffProps = [][]action.KV{
		{},
		{{Key: "os", Value: "linux"}},
		{{Key: "os", Value: "win"}},
		{{Key: "os", Value: "linux"}, {Key: "arch", Value: "x86"}},
		{{Key: "os", Value: "linux"}, {Key: "arch", Value: "arm"}},
	}
)

func genStep(rng *rand.Rand, n *naive) scenarioStep {
	roll := rng.Intn(100)
	switch {
	case roll < 38:
		d := diffDigests[rng.Intn(len(diffDigests))]
		p := diffPlats[rng.Intn(len(diffPlats))]
		if rng.Intn(10) < 2 {
			p = diffPlats[rng.Intn(len(diffPlats))]
		}
		return scenarioStep{kind: "exec", digest: d, plat: p, prio: rng.Intn(10), skip: rng.Intn(5) == 0}
	case roll < 46:
		var unreg []string
		for _, w := range diffWorkers {
			if _, ok := n.workers[w]; !ok {
				unreg = append(unreg, w)
			}
		}
		if len(unreg) == 0 {
			return genStep(rng, n)
		}
		return scenarioStep{kind: "reg", worker: unreg[rng.Intn(len(unreg))],
			props: diffProps[rng.Intn(len(diffProps))], slots: 1 + rng.Intn(3)}
	case roll < 66:
		var reg []string
		for w := range n.workers {
			reg = append(reg, w)
		}
		if len(reg) == 0 {
			return genStep(rng, n)
		}
		return scenarioStep{kind: "poll", worker: reg[rng.Intn(len(reg))]}
	case roll < 84:
		type wh struct{ w, d string }
		var held []wh
		for name, w := range n.workers {
			for d := range w.held {
				held = append(held, wh{name, d})
			}
		}
		if len(held) == 0 {
			return genStep(rng, n)
		}
		pick := held[rng.Intn(len(held))]
		correct := n.inflight[pick.d].losses + 1
		att := correct
		if rng.Intn(10) < 3 {
			if rng.Intn(2) == 0 {
				att = correct + 1 + rng.Intn(2)
			} else if correct > 1 {
				att = correct - 1
			}
		}
		return scenarioStep{kind: "complete", worker: pick.w, digest: pick.d,
			attempt: att, exit: rng.Intn(5), infra: rng.Intn(4) == 0}
	case roll < 92:
		var reg []string
		for w := range n.workers {
			reg = append(reg, w)
		}
		if len(reg) == 0 {
			return genStep(rng, n)
		}
		return scenarioStep{kind: "lost", worker: reg[rng.Intn(len(reg))]}
	default:
		if n.wid == 0 {
			return genStep(rng, n)
		}
		return scenarioStep{kind: "cancel", cancelID: 1 + rng.Intn(n.wid)}
	}
}

func errKey(err error) string {
	switch {
	case err == nil:
		return "nil"
	case errors.Is(err, action.ErrInvalid):
		return "invalid"
	case errors.Is(err, action.ErrNotFound):
		return "notfound"
	case errors.Is(err, action.ErrExists):
		return "exists"
	case errors.Is(err, action.ErrState):
		return "state"
	case errors.Is(err, action.ErrAttemptStale):
		return "stale"
	case errors.Is(err, action.ErrNoFreeSlot):
		return "noslot"
	default:
		return err.Error()
	}
}

func basis(st scenarioStep) string {
	switch st.kind {
	case "exec":
		return "缓存命中且未skip->Cached；否则附着同摘要在途(platform须完全相同)；否则新建入队,seq=入队序"
	case "reg":
		return "参数非法优先于已存在；slots 1..64"
	case "poll":
		return "不存在>无空槽；按(有效优先级降序,seq升序)跳过不匹配队首,attempt=失数+1"
	case "complete":
		return "不持有->状态不符；attempt不符->尝试过期；infra=一次丢失；exit0写缓存覆盖"
	case "lost":
		return "注销工作者；持有操作按seq升序丢失:无人删,失数>=M全员Lost,否则带原seq回队"
	case "cancel":
		return "不存在>已终局状态不符；Queued无人则删,Assigned无人则弃置继续执行"
	}
	return ""
}

func runNaive(n *naive, st scenarioStep) stepResult {
	var r stepResult
	switch st.kind {
	case "exec":
		r = n.execute(st.digest, st.plat, st.prio, st.skip)
	case "reg":
		r = n.register(st.worker, st.props, st.slots)
	case "poll":
		r = n.poll(st.worker)
	case "complete":
		r = n.complete(st.worker, st.digest, st.attempt, st.exit, st.infra)
	case "lost":
		r = n.workerLost(st.worker)
	case "cancel":
		r = n.cancel(st.cancelID)
	default:
		r = stepResult{err: action.ErrInvalid}
	}
	// 被拒不改状态：即便模型提前返回，队列与缓存快照仍反映当前世界。
	if r.queue == nil {
		r.queue = n.queueOrder()
	}
	if r.cache == nil {
		r.cache = n.snapCache()
	}
	return r
}

var _ = fmt.Sprintf

func runReal(s *Scheduler, st scenarioStep) (id int, ok bool, digest string, att int, err error) {
	switch st.kind {
	case "exec":
		id, err = s.Execute(st.digest, st.plat, st.prio, st.skip)
		if err == nil {
			digest = st.digest
		}
	case "reg":
		err = s.Register(st.worker, st.props, st.slots)
	case "poll":
		var r PollResult
		r, err = s.Poll(st.worker)
		if err == nil && r.Op != nil {
			ok, digest, att = true, r.Op.Digest, r.Attempt
		}
	case "complete":
		o, exists := s.registry.Get(st.digest)
		if !exists {
			// 朴素模型在此情形必然返回 ErrState（工作者无法持有不存在操作）。
			err = s.Complete(st.worker, (*action.Op)(nil), st.attempt, st.exit, st.infra)
		} else {
			err = s.Complete(st.worker, o, st.attempt, st.exit, st.infra)
		}
	case "lost":
		err = s.WorkerLost(st.worker)
	case "cancel":
		err = s.Cancel(st.cancelID)
	}
	return
}

func drainReal(s *Scheduler, seen map[int]action.Outcome) map[int]action.Outcome {
	got := map[int]action.Outcome{}
	for id, w := range s.waiters {
		select {
		case o := <-w.Done():
			got[id] = o
			if prev, dup := seen[id]; dup {
				panic(fmt.Sprintf("waiter %d got second outcome: %v then %v", id, prev, o))
			}
			seen[id] = o
		default:
		}
	}
	return got
}

func sameOutcomes(a, b map[int]action.Outcome) bool {
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

func sameIntMap(a, b map[string]int) bool {
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

func checkInvariants(t *testing.T, trial, step int, s *Scheduler) {
	t.Helper()
	used := 0
	for _, name := range s.pool.Names() {
		w, _ := s.pool.Get(name)
		used += w.Used()
	}
	dispatched := 0
	for _, o := range s.registry.All() {
		if o.State == action.Assigned || o.State == action.Abandoned {
			dispatched++
		}
		if o.State == action.Queued && len(o.Waiters) == 0 {
			t.Fatalf("trial %d step %d: queued op %s without waiters", trial, step, o.Digest)
		}
		if o.State == action.Queued {
			for _, w := range o.Waiters {
				if w.Terminal() {
					t.Fatalf("trial %d step %d: terminal waiter still in queued op %s", trial, step, o.Digest)
				}
			}
		}
	}
	if used != dispatched {
		t.Fatalf("trial %d step %d: used slots %d != dispatched ops %d", trial, step, used, dispatched)
	}
	// 同一摘要至多一个在途操作（Registry 的 key 已保证，这里复查无重复指针异常）。
	digests := map[string]bool{}
	for _, o := range s.registry.All() {
		if digests[o.Digest] {
			t.Fatalf("trial %d step %d: duplicate inflight digest %s", trial, step, o.Digest)
		}
		digests[o.Digest] = true
	}
}

func TestRandomAgainstNaive(t *testing.T) {
	const trials, steps = 1500, 40
	for trial := 0; trial < trials; trial++ {
		rng := rand.New(rand.NewSource(int64(1000 + trial)))
		M := 1 + rng.Intn(5)
		s := New(M)
		n := newNaive(M)
		seen := map[int]action.Outcome{}

		for si := 0; si < steps; si++ {
			st := genStep(rng, n)
			want := runNaive(n, st)
			id, ok, digest, att, err := runReal(s, st)

			t.Logf("trial=%d step=%d input={kind=%s digest=%q worker=%q prio=%d skip=%v plat=%v att=%d exit=%d infra=%v cancel=%d} | 输出 id=%d ok=%v poll=%q att=%d err=%s | 模型 id=%d ok=%v poll=%q att=%d err=%s | 判定: %s",
				trial, si, st.kind, st.digest, st.worker, st.prio, st.skip, st.plat,
				st.attempt, st.exit, st.infra, st.cancelID,
				id, ok, digest, att, errKey(err),
				want.id, want.ok, want.digest, want.att, errKey(want.err), basis(st))

			if errKey(err) != errKey(want.err) {
				t.Fatalf("trial %d step %d %+v: err %s vs model %s", trial, si, st, errKey(err), errKey(want.err))
			}
			if err == nil {
				if st.kind == "exec" && id != want.id {
					t.Fatalf("trial %d step %d: waiter %d vs model %d", trial, si, id, want.id)
				}
				if st.kind == "poll" && (ok != want.ok || digest != want.digest || att != want.att) {
					t.Fatalf("trial %d step %d: poll (%v %s %d) vs model (%v %s %d)",
						trial, si, ok, digest, att, want.ok, want.digest, want.att)
				}
			}

			got := drainReal(s, seen)
			if !sameOutcomes(got, want.outcomes) {
				t.Fatalf("trial %d step %d: outcomes real=%v model=%v", trial, si, got, want.outcomes)
			}
			if rq := s.QueueOrder(); !eqStrings(rq, want.queue) {
				t.Fatalf("trial %d step %d: queue real=%v model=%v", trial, si, rq, want.queue)
			}
			if rc := s.cache.Snapshot(); !sameIntMap(rc, want.cache) {
				t.Fatalf("trial %d step %d: cache real=%v model=%v", trial, si, rc, want.cache)
			}
			checkInvariants(t, trial, si, s)
		}

		for id := 1; id <= n.wid; id++ {
			mw, ok := n.waiters[id]
			if !ok {
				continue
			}
			rw, exists := s.waiters[id]
			if !exists {
				t.Fatalf("trial %d: waiter %d missing", trial, id)
			}
			if mw.terminal != rw.Terminal() {
				t.Fatalf("trial %d: waiter %d terminal real=%v model=%v", trial, id, rw.Terminal(), mw.terminal)
			}
			if mw.terminal {
				ro, rok := seen[id]
				if !rok || ro != mw.out {
					t.Fatalf("trial %d: waiter %d real=%v model=%v", trial, id, ro, mw.out)
				}
			}
		}
		if t.Failed() {
			return
		}
	}
}
