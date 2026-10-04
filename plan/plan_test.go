package plan_test

import (
	"errors"
	"fmt"
	"math/rand/v2"
	"strings"
	"sync"
	"testing"

	"ontology/plan"
)

func newExampleScheduler(t *testing.T) *plan.Scheduler {
	t.Helper()
	s, err := plan.New(30, 0, "A")
	if err != nil {
		t.Fatal(err)
	}
	must(t, s.SetChangeover("A", "B", 20))
	must(t, s.SetChangeover("B", "A", 15))
	return s
}

func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}

func wo(id, fam string, dur, due, ready int64) plan.WorkOrder {
	return plan.WorkOrder{ID: id, Family: fam, Duration: dur, Due: due, Ready: ready}
}

func checkResult(t *testing.T, got plan.Result, start, end int64, late bool) {
	t.Helper()
	if got.Start != start || got.End != end || got.Late != late {
		t.Fatalf("got start=%d end=%d late=%v, want %d/%d/%v", got.Start, got.End, got.Late, start, end, late)
	}
}

func ids(rs []plan.Result) string {
	parts := make([]string, len(rs))
	for i, r := range rs {
		parts[i] = r.ID
	}
	return strings.Join(parts, ",")
}

// TestSpecExample 复现题面给定的完整例子。
func TestSpecExample(t *testing.T) {
	s := newExampleScheduler(t)

	r1, err := s.Insert(wo("W1", "A", 50, 100, 0), false, 0)
	must(t, err)
	checkResult(t, r1, 30, 80, false)

	r2, err := s.Insert(wo("W2", "B", 40, 200, 0), false, 0)
	must(t, err)
	checkResult(t, r2, 100, 140, false)

	r3, err := s.Insert(wo("W3", "A", 30, 150, 0), true, 0)
	must(t, err)
	checkResult(t, r3, 80, 110, false)
	if seq := ids(s.List()); seq != "W1,W3,W2" {
		t.Fatalf("seq=%s", seq)
	}
	if g, _ := s.Get("W2"); g.Start != 130 || g.End != 170 {
		t.Fatalf("W2=%+v, want 130..170", g)
	}

	if _, err = s.Insert(wo("W4", "B", 40, 150, 0), true, 0); !errors.Is(err, plan.ErrDueConflict) {
		t.Fatalf("W4 err=%v, want ErrDueConflict", err)
	}

	if _, err = s.Insert(wo("W5", "A", 35, 120, 0), true, 0); !errors.Is(err, plan.ErrDueConflict) {
		t.Fatalf("W5(35) err=%v, want ErrDueConflict", err)
	}

	r5, err := s.Insert(wo("W5", "A", 30, 120, 0), true, 0)
	must(t, err)
	checkResult(t, r5, 80, 110, false)
	if seq := ids(s.List()); seq != "W1,W5,W3,W2" {
		t.Fatalf("seq=%s", seq)
	}
	g3, _ := s.Get("W3")
	checkResult(t, g3, 110, 140, false)
	g2, _ := s.Get("W2")
	checkResult(t, g2, 160, 200, false)
}

// TestFreezeBoundary 复现冻结边界例子：start==now+F 不冻结，小 1 即冻结。
func TestFreezeBoundary(t *testing.T) {
	s := newExampleScheduler(t)
	_, _ = s.Insert(wo("W1", "A", 50, 100, 0), false, 0)
	_, _ = s.Insert(wo("W3", "A", 30, 150, 0), false, 0)
	_, _ = s.Insert(wo("W2", "B", 40, 200, 0), false, 0)

	_, err := s.Insert(wo("W9", "A", 1, 9_999, 0), false, 50)
	must(t, err)
	g1, _ := s.Get("W1")
	g3, _ := s.Get("W3")
	if !g1.Frozen || g3.Frozen {
		t.Fatalf("frozen flags: W1=%v W3=%v, want true/false", g1.Frozen, g3.Frozen)
	}
	if g3.Start != 80 {
		t.Fatalf("W3 start=%d, want 80 (boundary equals start, not frozen)", g3.Start)
	}

	must(t, s.Remove("W9", 51))
	g3, _ = s.Get("W3")
	if !g3.Frozen {
		t.Fatal("W3 should be frozen at now=51")
	}
	if err := s.Remove("W3", 51); !errors.Is(err, plan.ErrFrozen) {
		t.Fatalf("remove frozen err=%v, want ErrFrozen", err)
	}
	if _, err := s.Insert(wo("W6", "A", 1, 120, 0), true, 51); err != nil {
		t.Fatalf("insert after frozen prefix: %v", err)
	}
	if seq := ids(s.List()); seq != "W1,W3,W6,W2" {
		t.Fatalf("seq=%s, frozen W3 must not move", seq)
	}
}

// TestEarliestFixed earliest 固定在接受时刻，不随时钟浮动。
func TestEarliestFixed(t *testing.T) {
	s, err := plan.New(30, 0, "A")
	must(t, err)
	r, err := s.Insert(wo("W1", "A", 10, 1000, 0), false, 100)
	must(t, err)
	checkResult(t, r, 130, 140, false)
	r2, err := s.Insert(wo("W2", "A", 10, 1000, 0), false, 200)
	must(t, err)
	checkResult(t, r2, 230, 240, false)
}

// TestAlreadyLateNotConflict 插入前已延期的工单变得更晚不算新增冲突。
func TestAlreadyLateNotConflict(t *testing.T) {
	s, err := plan.New(0, 0, "A")
	must(t, err)
	_, _ = s.Insert(wo("L", "A", 100, 50, 0), false, 0)
	_, err = s.Insert(wo("N", "A", 10, 200, 0), true, 0)
	must(t, err)
	if g, _ := s.Get("L"); g.Start != 0 || g.End != 100 || !g.Late {
		t.Fatalf("L=%+v, want 0..100 late (F=0: N inserts after L by due order? no: N due 200 after L)", g)
	}
	if n, _ := s.Get("N"); n.Start != 100 || n.End != 110 {
		t.Fatalf("N=%+v, want 100..110", n)
	}
}

// TestEndEqualsDue end 恰等 due 算按期。
func TestEndEqualsDue(t *testing.T) {
	s, err := plan.New(0, 10, "A")
	must(t, err)
	r, err := s.Insert(wo("W", "A", 40, 50, 0), true, 0)
	must(t, err)
	checkResult(t, r, 10, 50, false)
}

// TestStrictFailureRollsBack 严格插入失败后计划逐字段不变，且不占编号、不推进时钟。
func TestStrictFailureRollsBack(t *testing.T) {
	s := newExampleScheduler(t)
	_, _ = s.Insert(wo("W1", "A", 50, 100, 0), false, 0)
	_, _ = s.Insert(wo("W2", "B", 40, 200, 0), false, 0)
	before := s.List()

	_, err := s.Insert(wo("W4", "B", 40, 120, 0), true, 0)
	if !errors.Is(err, plan.ErrDueConflict) {
		t.Fatalf("err=%v", err)
	}
	after := s.List()
	if len(after) != len(before) {
		t.Fatalf("len changed: %d vs %d", len(after), len(before))
	}
	for i := range before {
		if before[i] != after[i] {
			t.Fatalf("field changed at %d:\nbefore=%+v\nafter =%+v", i, before[i], after[i])
		}
	}
	if _, err := s.Insert(wo("W4", "B", 5, 500, 0), false, 0); err != nil {
		t.Fatalf("rejected op must not consume id/clock: %v", err)
	}
}

// TestRejectionOrder 拒绝次序：参数非法 > 时钟回退 > 重复/不存在 > 已冻结 > 交期冲突。
func TestRejectionOrder(t *testing.T) {
	s := newExampleScheduler(t)
	_, _ = s.Insert(wo("W1", "A", 50, 100, 0), false, 10)

	if _, err := s.Insert(plan.WorkOrder{ID: "X", Family: "A", Duration: 0, Due: 0, Ready: 0}, true, 0); !errors.Is(err, plan.ErrInvalidArgument) {
		t.Fatalf("invalid+rollback: %v, want ErrInvalidArgument", err)
	}
	if _, err := s.Insert(wo("W1", "A", 1, 1, 0), true, 5); !errors.Is(err, plan.ErrClockRollback) {
		t.Fatalf("rollback+duplicate: %v, want ErrClockRollback", err)
	}
	if _, err := s.Insert(wo("W1", "A", 1, 1, 0), true, 10); !errors.Is(err, plan.ErrDuplicate) {
		t.Fatalf("duplicate+conflict: %v, want ErrDuplicate", err)
	}
	_, _ = s.Insert(wo("W2", "A", 1, 500, 0), false, 100)
	if err := s.Remove("W1", 100); !errors.Is(err, plan.ErrFrozen) {
		t.Fatalf("frozen remove: %v, want ErrFrozen", err)
	}
	if err := s.Remove("ghost", 100); !errors.Is(err, plan.ErrNotFound) {
		t.Fatalf("missing remove: %v, want ErrNotFound", err)
	}
	if _, err := s.Get("ghost"); !errors.Is(err, plan.ErrNotFound) {
		t.Fatalf("get missing: %v, want ErrNotFound", err)
	}
	if _, err := s.Get(""); !errors.Is(err, plan.ErrInvalidArgument) {
		t.Fatalf("get invalid id: %v, want ErrInvalidArgument", err)
	}
	if err := s.Remove("ghost", 99); !errors.Is(err, plan.ErrClockRollback) {
		t.Fatalf("rollback vs missing: %v, want ErrClockRollback", err)
	}
	if err := s.SetChangeover("A", "B", 99); !errors.Is(err, plan.ErrInvalidState) {
		t.Fatalf("setchangeover state: %v, want ErrInvalidState", err)
	}
}

// TestRemoveRecalculates 移除后按新前驱换型重算，且受各自 earliest 限制。
func TestRemoveRecalculates(t *testing.T) {
	s := newExampleScheduler(t)
	_, _ = s.Insert(wo("W1", "A", 50, 500, 0), false, 0)
	_, _ = s.Insert(wo("W2", "B", 10, 500, 0), false, 0)
	_, _ = s.Insert(wo("W3", "A", 10, 500, 0), false, 0)
	must(t, s.Remove("W2", 0))
	if seq := ids(s.List()); seq != "W1,W3" {
		t.Fatalf("seq=%s", seq)
	}
	g3, _ := s.Get("W3")
	if g3.Start != 80 || g3.End != 90 {
		t.Fatalf("W3=%+v, want 80..90", g3)
	}
}

// TestRecalcIndependentOfPrefix 100 张与 10000 张前缀两档：
// 尾部操作重算数与前缀长度无关。
func TestRecalcIndependentOfPrefix(t *testing.T) {
	measure := func(prefix int) (insertN, middleN int) {
		s, err := plan.New(0, 0, "A")
		if err != nil {
			t.Fatal(err)
		}
		for i := 0; i < prefix; i++ {
			id := fmt.Sprintf("P%05d", i)
			if _, err := s.Insert(wo(id, "A", 1, 1_000_000_000, 0), false, 0); err != nil {
				t.Fatal(err)
			}
		}
		if _, err := s.Insert(wo("TAIL", "A", 1, 1_000_000_000, 0), false, 0); err != nil {
			t.Fatal(err)
		}
		insertN = s.RecalcCount()
		mid := fmt.Sprintf("P%05d", prefix/2)
		if err := s.Remove(mid, 0); err != nil {
			t.Fatal(err)
		}
		middleN = s.RecalcCount()
		return insertN, middleN
	}
	i100, m100 := measure(100)
	i10k, m10k := measure(10_000)
	if i100 != 1 || i10k != 1 {
		t.Fatalf("tail insert recalc: 100-prefix=%d 10000-prefix=%d, both want 1", i100, i10k)
	}
	// 移除点之后有 (prefix/2 - 1 个 P + TAIL) 张，重算即该数量；与移除点之前无关。
	if m100 != 50 || m10k != 5000 {
		t.Fatalf("mid remove recalc: 100-prefix=%d(want 50) 10000-prefix=%d(want 5000)", m100, m10k)
	}
	if m100 >= m10k {
		t.Fatalf("remove recalc should scale with suffix only: %d vs %d", m100, m10k)
	}
}

// TestConcurrentSafe 并发调用结果等价于某串行顺序（-race 下验证）。
func TestConcurrentSafe(t *testing.T) {
	s, err := plan.New(0, 0, "A")
	must(t, err)
	var wg sync.WaitGroup
	for g := 0; g < 8; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			for k := 0; k < 50; k++ {
				id := fmt.Sprintf("G%dK%d", g, k)
				_, _ = s.Insert(wo(id, "A", 1, int64(10_000+k), 0), false, 0)
				_ = s.List()
			}
		}(g)
	}
	wg.Wait()
	if got := len(s.List()); got != 400 {
		t.Fatalf("accepted=%d, want 400", got)
	}
}

// ---- 朴素参考模型：每次接受操作后都从头排序、从头递推全部时刻 ----

type refJob struct {
	id, fam      string
	dur, due     int64
	ready, early int64
	ord          uint64
	start, end   int64
}

type refModel struct {
	f, t0, nowMax int64
	f0            string
	co            map[[2]string]int64
	jobs          []refJob
	frozen        int
	ordSeq        uint64
}

func newRef(f, t0 int64, f0 string) *refModel {
	return &refModel{f: f, t0: t0, f0: f0, co: map[[2]string]int64{}}
}

func (m *refModel) recompute() {
	var prevEnd int64 = m.t0
	prevFam := m.f0
	for i := range m.jobs {
		job := &m.jobs[i]
		start := prevEnd + m.co[[2]string{prevFam, job.fam}]
		if job.early > start {
			start = job.early
		}
		job.start = start
		job.end = start + job.dur
		prevEnd = job.end
		prevFam = job.fam
	}
}

func (m *refModel) freeze(now int64) {
	limit := now + m.f
	for m.frozen < len(m.jobs) && m.jobs[m.frozen].start < limit {
		m.frozen++
	}
}

func (m *refModel) get(id string) (refJob, int, bool) {
	for i := range m.jobs {
		if m.jobs[i].id == id {
			return m.jobs[i], i, true
		}
	}
	return refJob{}, -1, false
}

func validRefID(id string) bool { return len(id) >= 1 && len(id) <= 32 }

func (m *refModel) setCO(a, b string, minutes int64) error {
	if !validRefID(a) || !validRefID(b) || minutes < 0 || minutes > 100_000 || (a == b && minutes != 0) {
		return plan.ErrInvalidArgument
	}
	if len(m.jobs) > 0 {
		return plan.ErrInvalidState
	}
	m.co[[2]string{a, b}] = minutes
	return nil
}

func (m *refModel) insert(w plan.WorkOrder, strict bool, now int64) (plan.Result, error) {
	if !validRefID(w.ID) || !validRefID(w.Family) || w.Duration < 1 || w.Duration > 100_000 ||
		w.Due < 0 || w.Due > 1_000_000_000 || w.Ready < 0 || w.Ready > 1_000_000_000 || now < 0 || now > 1_000_000_000 {
		return plan.Result{}, plan.ErrInvalidArgument
	}
	if now < m.nowMax {
		return plan.Result{}, plan.ErrClockRollback
	}
	if _, _, ok := m.get(w.ID); ok {
		return plan.Result{}, plan.ErrDuplicate
	}
	savedJobs := append([]refJob(nil), m.jobs...)
	savedFrozen := m.frozen
	m.freeze(now)
	early := w.Ready
	if cand := now + m.f; cand > early {
		early = cand
	}
	newJob := refJob{id: w.ID, fam: w.Family, dur: w.Duration, due: w.Due, ready: w.Ready, early: early, ord: m.ordSeq}
	m.ordSeq++
	pos := m.frozen
	for pos < len(m.jobs) {
		cur := m.jobs[pos]
		if cur.due > w.Due || (cur.due == w.Due && cur.ord > newJob.ord) {
			break
		}
		pos++
	}
	m.jobs = append(m.jobs, refJob{})
	copy(m.jobs[pos+1:], m.jobs[pos:])
	m.jobs[pos] = newJob
	m.recompute()
	conflict := m.jobs[pos].end > w.Due
	if strict && !conflict {
		for i := pos + 1; i < len(m.jobs); i++ {
			after := m.jobs[i]
			if before, _, ok := savedGet(savedJobs, after.id); ok && before.end <= before.due && after.end > after.due {
				conflict = true
				break
			}
		}
	}
	if strict && conflict {
		m.jobs = savedJobs
		m.frozen = savedFrozen
		return plan.Result{}, plan.ErrDueConflict
	}
	m.nowMax = now
	j, _, _ := m.get(w.ID)
	return plan.Result{ID: w.ID, Start: j.start, End: j.end, Late: j.end > j.due, Frozen: j.start < now+m.f}, nil
}

func savedGet(jobs []refJob, id string) (refJob, int, bool) {
	for i := range jobs {
		if jobs[i].id == id {
			return jobs[i], i, true
		}
	}
	return refJob{}, -1, false
}

func (m *refModel) remove(id string, now int64) error {
	if !validRefID(id) || now < 0 || now > 1_000_000_000 {
		return plan.ErrInvalidArgument
	}
	if now < m.nowMax {
		return plan.ErrClockRollback
	}
	savedJobs := append([]refJob(nil), m.jobs...)
	savedFrozen := m.frozen
	m.freeze(now)
	_, pos, ok := m.get(id)
	if !ok {
		m.jobs = savedJobs
		m.frozen = savedFrozen
		return plan.ErrNotFound
	}
	if pos < m.frozen {
		m.jobs = savedJobs
		m.frozen = savedFrozen
		return plan.ErrFrozen
	}
	m.jobs = append(m.jobs[:pos], m.jobs[pos+1:]...)
	m.recompute()
	m.nowMax = now
	return nil
}

func (m *refModel) list() []plan.Result {
	out := make([]plan.Result, len(m.jobs))
	for i, job := range m.jobs {
		out[i] = plan.Result{ID: job.id, Start: job.start, End: job.end, Late: job.end > job.due, Frozen: i < m.frozen}
	}
	return out
}

func errCode(err error) string {
	switch {
	case errors.Is(err, plan.ErrInvalidArgument):
		return "INVALID"
	case errors.Is(err, plan.ErrClockRollback):
		return "ROLLBACK"
	case errors.Is(err, plan.ErrDuplicate):
		return "DUP"
	case errors.Is(err, plan.ErrNotFound):
		return "NOTFOUND"
	case errors.Is(err, plan.ErrFrozen):
		return "FROZEN"
	case errors.Is(err, plan.ErrDueConflict):
		return "CONFLICT"
	case errors.Is(err, plan.ErrInvalidState):
		return "STATE"
	case err == nil:
		return "OK"
	default:
		return "OTHER:" + err.Error()
	}
}

type refOp struct {
	kind    string
	w       plan.WorkOrder
	strict  bool
	now     int64
	id      string
	a, b    string
	minutes int64
}

// TestRandomDifferential 与每次从头朴素重排的参考模型对照 1500 组随机操作序列。
func TestRandomDifferential(t *testing.T) {
	const cases = 1500
	fams := []string{"A", "B", "C"}
	for tc := 0; tc < cases; tc++ {
		rng := rand.New(rand.NewPCG(uint64(tc+1), uint64(cases-tc)))
		F := int64(rng.IntN(41))
		T0 := int64(rng.IntN(10))
		s, err := plan.New(F, T0, "A")
		if err != nil {
			t.Fatal(err)
		}
		ref := newRef(F, T0, "A")
		var logBuf strings.Builder
		fmt.Fprintf(&logBuf, "case %d F=%d T0=%d\n", tc, F, T0)

		nOps := 6 + rng.IntN(20)
		now := int64(0)
		var liveIDs []string
		for opn := 0; opn < nOps; opn++ {
			op := refOp{now: now, kind: "insert"}
			roll := rng.IntN(100)
			switch {
			case roll < 12 && len(liveIDs) > 0:
				op.kind = "remove"
				op.id = liveIDs[rng.IntN(len(liveIDs))]
			case roll < 20:
				op.kind = "setCO"
				op.a = fams[rng.IntN(3)]
				op.b = fams[rng.IntN(3)]
				if op.a == op.b {
					op.minutes = 0
				} else {
					op.minutes = int64(rng.IntN(6)) * 5
				}
			default:
				op.kind = "insert"
				id := fmt.Sprintf("W%d-%d", tc, opn)
				op.w = plan.WorkOrder{
					ID:       id,
					Family:   fams[rng.IntN(3)],
					Duration: int64(1 + rng.IntN(40)),
					Due:      int64(rng.IntN(220)),
					Ready:    int64(rng.IntN(60)),
				}
				op.strict = rng.IntN(2) == 0
				op.now = now
				liveIDs = append(liveIDs, id)
			}
			if op.kind != "setCO" {
				jump := int64(rng.IntN(30))
				if rng.IntN(8) == 0 {
					jump = -int64(rng.IntN(6)) // 偶发时钟回退
				}
				op.now = now + jump
				if op.now < 0 {
					op.now = 0
				}
			}

			var gotR plan.Result
			var gotErr error
			var refR plan.Result
			var refErr error
			switch op.kind {
			case "insert":
				gotR, gotErr = s.Insert(op.w, op.strict, op.now)
				refR, refErr = ref.insert(op.w, op.strict, op.now)
				fmt.Fprintf(&logBuf, " op%d insert %s fam=%s dur=%d due=%d ready=%d strict=%v now=%d",
					opn, op.w.ID, op.w.Family, op.w.Duration, op.w.Due, op.w.Ready, op.strict, op.now)
			case "remove":
				gotErr = s.Remove(op.id, op.now)
				refErr = ref.remove(op.id, op.now)
				fmt.Fprintf(&logBuf, " op%d remove %s now=%d", opn, op.id, op.now)
			case "setCO":
				gotErr = s.SetChangeover(op.a, op.b, op.minutes)
				refErr = ref.setCO(op.a, op.b, op.minutes)
				fmt.Fprintf(&logBuf, " op%d setCO %s->%s=%d", opn, op.a, op.b, op.minutes)
			}

			reason := "error classes match"
			if errCode(gotErr) != errCode(refErr) {
				reason = fmt.Sprintf("ERROR MISMATCH impl=%s ref=%s", errCode(gotErr), errCode(refErr))
			} else if gotErr == nil && op.kind == "insert" {
				if gotR != refR {
					reason = fmt.Sprintf("RESULT MISMATCH impl=%+v ref=%+v", gotR, refR)
				} else {
					reason = fmt.Sprintf("accepted start=%d end=%d late=%v frozen=%v",
						gotR.Start, gotR.End, gotR.Late, gotR.Frozen)
				}
			}
			fmt.Fprintf(&logBuf, " => impl=%s ref=%s | %s\n", errCode(gotErr), errCode(refErr), reason)

			if errCode(gotErr) != errCode(refErr) {
				t.Fatalf("case %d op %s:\n%s", tc, op.kind, logBuf.String())
			}
			if gotErr == nil && op.kind == "insert" && gotR != refR {
				t.Fatalf("case %d insert result:\n%s", tc, logBuf.String())
			}
			gotList, refList := s.List(), ref.list()
			if len(gotList) != len(refList) {
				t.Fatalf("case %d list len %d vs %d:\n%s", tc, len(gotList), len(refList), logBuf.String())
			}
			for i := range gotList {
				if gotList[i] != refList[i] {
					t.Fatalf("case %d list[%d] mismatch\nimpl=%+v\nref =%+v\n%s",
						tc, i, gotList[i], refList[i], logBuf.String())
				}
			}
			if op.kind != "setCO" && gotErr == nil {
				now = op.now
			}
		}
		t.Logf("case %d: %d ops replayed, final %d jobs, fields identical to naive model\n%s",
			tc, nOps, len(s.List()), logBuf.String())
	}
}
