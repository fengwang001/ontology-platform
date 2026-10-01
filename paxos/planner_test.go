package paxos

import (
	"errors"
	"fmt"
	"math/rand"
	"sort"
	"sync"
	"testing"
)

func entry(b int64, v string) AcceptedEntry {
	return AcceptedEntry{Ballot: b, Value: v}
}

func rep(chosen int, accepted map[int]AcceptedEntry) PromiseReport {
	return PromiseReport{Ballot: 5, Chosen: chosen, Accepted: accepted}
}

func logReports(t *testing.T, reports map[int]PromiseReport) {
	t.Helper()
	froms := make([]int, 0, len(reports))
	for from := range reports {
		froms = append(froms, from)
	}
	sort.Ints(froms)
	for _, from := range froms {
		r := reports[from]
		slots := make([]int, 0, len(r.Accepted))
		for s := range r.Accepted {
			slots = append(slots, s)
		}
		sort.Ints(slots)
		parts := ""
		for _, s := range slots {
			e := r.Accepted[s]
			parts += fmt.Sprintf(" %d->(b=%d,q=%q)", s, e.Ballot, e.Value)
		}
		t.Logf("AddPromise(from=%d, {pb=%d chosen=%d accepted:[%s ]})", from, r.Ballot, r.Chosen, parts)
	}
}

func logPlan(t *testing.T, plan *RecoveryPlan, err error, rationale string) {
	t.Helper()
	if err != nil {
		t.Logf("Plan() => error %v | 判定依据: %s", err, rationale)
		return
	}
	t.Logf("Plan() => start=%d nextFree=%d entries=%v | 判定依据: %s",
		plan.Start, plan.NextFree, plan.Entries, rationale)
}

// naivePlan is a straightforward per-slot reimplementation used as an oracle.
func naivePlan(n int, reports map[int]PromiseReport) (*RecoveryPlan, error) {
	if len(reports) < n/2+1 {
		return nil, ErrNoQuorum
	}
	start, maxSlot := 0, 0
	for _, r := range reports {
		if r.Chosen+1 > start {
			start = r.Chosen + 1
		}
		for s := range r.Accepted {
			if s > maxSlot {
				maxSlot = s
			}
		}
	}
	type cand struct {
		ballot int64
		value  string
	}
	var entries []PlanEntry
	for s := start; s <= maxSlot; s++ {
		var cands []cand
		var best int64
		for _, r := range reports {
			if e, ok := r.Accepted[s]; ok {
				cands = append(cands, cand{e.Ballot, e.Value})
				if e.Ballot > best {
					best = e.Ballot
				}
			}
		}
		if cands == nil {
			entries = append(entries, PlanEntry{Slot: s, Noop: true, SourceBallot: 0})
			continue
		}
		val := ""
		first := true
		for _, c := range cands {
			if c.ballot != best {
				continue
			}
			if first {
				val, first = c.value, false
			} else if c.value != val {
				return nil, ConflictError{Slot: s}
			}
		}
		entries = append(entries, PlanEntry{Slot: s, Value: val, SourceBallot: best})
	}
	nextFree := maxSlot
	if start-1 > nextFree {
		nextFree = start - 1
	}
	return &RecoveryPlan{Start: start, NextFree: nextFree + 1, Entries: entries}, nil
}

func plansEqual(a, b *RecoveryPlan) bool {
	if a.Start != b.Start || a.NextFree != b.NextFree || len(a.Entries) != len(b.Entries) {
		return false
	}
	for i := range a.Entries {
		if a.Entries[i] != b.Entries[i] {
			return false
		}
	}
	return true
}

func addAll(t *testing.T, p *Planner, reports map[int]PromiseReport) {
	t.Helper()
	froms := make([]int, 0, len(reports))
	for from := range reports {
		froms = append(froms, from)
	}
	sort.Ints(froms)
	for _, from := range froms {
		if err := p.AddPromise(from, reports[from]); err != nil {
			t.Fatalf("AddPromise(%d) unexpected error: %v", from, err)
		}
	}
}

func conflictSlot(err error) (int, bool) {
	var ce ConflictError
	if errors.As(err, &ce) {
		return ce.Slot, true
	}
	return 0, false
}

func TestHighestBallotBeatsCount(t *testing.T) {
	// 选票 4 的单个接受项胜过选票 3 的两个相同值接受项：值不按多数派计数。
	p, err := NewPlanner(5, 5)
	if err != nil {
		t.Fatal(err)
	}
	reports := map[int]PromiseReport{
		0: rep(0, map[int]AcceptedEntry{1: entry(4, "solo")}),
		1: rep(0, map[int]AcceptedEntry{1: entry(3, "dup")}),
		2: rep(0, map[int]AcceptedEntry{1: entry(3, "dup")}),
	}
	logReports(t, reports)
	addAll(t, p, reports)

	plan, err := p.Plan()
	if err != nil {
		t.Fatalf("Plan: %v", err)
	}
	logPlan(t, plan, nil, "槽1最高接受选票为4(仅1份)，高于选票3的2份，故沿用 solo")
	if plan.Start != 1 || plan.NextFree != 2 || len(plan.Entries) != 1 {
		t.Fatalf("unexpected plan: %+v", plan)
	}
	if e := plan.Entries[0]; e.Noop || e.Value != "solo" || e.SourceBallot != 4 {
		t.Fatalf("highest ballot must win regardless of count: %+v", e)
	}
	want, _ := naivePlan(5, reports)
	if !plansEqual(plan, want) {
		t.Fatalf("plan %+v != naive %+v", plan, want)
	}
}

func TestNoopHoleAndEmptyStringDistinction(t *testing.T) {
	// 仅槽位 2、4 有接受项 => 槽位 1、3 补 Noop；空串值与 Noop 可区分。
	p, _ := NewPlanner(5, 3)
	reports := map[int]PromiseReport{
		0: rep(0, map[int]AcceptedEntry{2: entry(1, "x"), 4: entry(2, "")}),
		1: rep(0, nil),
	}
	logReports(t, reports)
	addAll(t, p, reports)

	plan, err := p.Plan()
	if err != nil {
		t.Fatalf("Plan: %v", err)
	}
	logPlan(t, plan, nil, "start=1 maxSlot=4；槽1、3无接受项=>Noop(b=0)；槽4值为空串(Noop=false)，来源选票2")
	want := []PlanEntry{
		{Slot: 1, Noop: true, SourceBallot: 0},
		{Slot: 2, Value: "x", SourceBallot: 1},
		{Slot: 3, Noop: true, SourceBallot: 0},
		{Slot: 4, Value: "", SourceBallot: 2},
	}
	if len(plan.Entries) != len(want) {
		t.Fatalf("entries = %v, want %v", plan.Entries, want)
	}
	for i := range want {
		if plan.Entries[i] != want[i] {
			t.Fatalf("entry %d = %+v, want %+v", i, plan.Entries[i], want[i])
		}
	}
	if plan.Start != 1 || plan.NextFree != 5 {
		t.Fatalf("start/nextFree = %d/%d", plan.Start, plan.NextFree)
	}
	wantPlan, _ := naivePlan(3, reports)
	if !plansEqual(plan, wantPlan) {
		t.Fatalf("plan %+v != naive %+v", plan, wantPlan)
	}
}

func TestQuorumBoundary(t *testing.T) {
	// n=5 多数派=3：2 份时差一报 ErrNoQuorum，补第 3 份后成功。
	p, _ := NewPlanner(5, 5)
	for _, from := range []int{0, 1} {
		if err := p.AddPromise(from, rep(0, nil)); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := p.Plan(); !errors.Is(err, ErrNoQuorum) {
		t.Fatalf("2/5 promises: want ErrNoQuorum, got %v", err)
	}
	t.Logf("Plan() with 2/5 => ErrNoQuorum | 判定依据: 多数派=⌊5/2⌋+1=3，差一；Plan 失败不关闭")
	if err := p.AddPromise(2, rep(0, nil)); err != nil {
		t.Fatalf("planner must remain open after failed Plan: %v", err)
	}
	plan, err := p.Plan()
	if err != nil {
		t.Fatalf("3/5 promises should form quorum: %v", err)
	}
	logPlan(t, plan, nil, "恰好3/5构成多数派；空表 maxSlot=0、start=1，nextFree=1")
	if plan.Start != 1 || plan.NextFree != 1 || len(plan.Entries) != 0 {
		t.Fatalf("unexpected plan: %+v", plan)
	}
	// 成功后关闭：再收集与再规划都报已关闭。
	if err := p.AddPromise(3, rep(0, nil)); !errors.Is(err, ErrClosed) {
		t.Fatalf("AddPromise after success want ErrClosed, got %v", err)
	}
	if _, err := p.Plan(); !errors.Is(err, ErrClosed) {
		t.Fatalf("second Plan want ErrClosed, got %v", err)
	}
}

func TestRejectedCallsDoNotChangeState(t *testing.T) {
	p, _ := NewPlanner(5, 3)

	// 构造拒绝。
	if _, err := NewPlanner(0, 3); !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("b<=0: want ErrInvalidArgument, got %v", err)
	}
	if _, err := NewPlanner(5, -1); !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("n<=0: want ErrInvalidArgument, got %v", err)
	}

	// from 越界。
	if err := p.AddPromise(3, rep(0, nil)); !errors.Is(err, ErrAcceptorOutOfRange) {
		t.Fatalf("want ErrAcceptorOutOfRange, got %v", err)
	}
	if err := p.AddPromise(-1, rep(0, nil)); !errors.Is(err, ErrAcceptorOutOfRange) {
		t.Fatalf("want ErrAcceptorOutOfRange, got %v", err)
	}
	// pb 不等于 b。
	if err := p.AddPromise(0, PromiseReport{Ballot: 4, Chosen: 0}); !errors.Is(err, ErrWrongBallot) {
		t.Fatalf("want ErrWrongBallot, got %v", err)
	}
	// 槽位 <= chosen（含槽位 0）。
	bad1 := rep(1, map[int]AcceptedEntry{0: entry(1, "z"), 2: entry(0, "z")})
	if err := p.AddPromise(0, bad1); !errors.Is(err, ErrSlotNotAfterChosen) {
		t.Fatalf("slot 0 <= chosen: want ErrSlotNotAfterChosen, got %v", err)
	}
	bad2 := rep(1, map[int]AcceptedEntry{2: entry(0, "")})
	if err := p.AddPromise(0, bad2); !errors.Is(err, ErrInvalidBallot) {
		t.Fatalf("accepted ballot 0: want ErrInvalidBallot, got %v", err)
	}
	bad3 := rep(1, map[int]AcceptedEntry{2: entry(5, "")})
	if err := p.AddPromise(0, bad3); !errors.Is(err, ErrInvalidBallot) {
		t.Fatalf("accepted ballot >= b: want ErrInvalidBallot, got %v", err)
	}
	// 每项先查槽位后查选票：槽位与选票都非法时报槽位错误。
	bad4 := rep(1, map[int]AcceptedEntry{1: entry(0, "")})
	if err := p.AddPromise(0, bad4); !errors.Is(err, ErrSlotNotAfterChosen) {
		t.Fatalf("slot check precedes ballot check, got %v", err)
	}

	// 全部被拒后，接受者 0 仍应可成功提交。
	if err := p.AddPromise(0, rep(0, nil)); err != nil {
		t.Fatalf("rejected calls must not register acceptor 0: %v", err)
	}
	if err := p.AddPromise(0, rep(0, nil)); !errors.Is(err, ErrAlreadyPromised) {
		t.Fatalf("want ErrAlreadyPromised, got %v", err)
	}
	if err := p.AddPromise(1, rep(0, nil)); err != nil {
		t.Fatal(err)
	}
	// 2/3 即 n=3 的多数派（⌊3/2⌋+1=2），规划成功。
	plan, err := p.Plan()
	if err != nil {
		t.Fatalf("2/3 is quorum for n=3: %v", err)
	}
	logPlan(t, plan, nil, "拒绝操作均未改状态；接受者0、1构成 n=3 的多数派2")
}

func TestErrorPriorityClosedFirst(t *testing.T) {
	p, _ := NewPlanner(5, 3)
	if err := p.AddPromise(0, rep(0, nil)); err != nil {
		t.Fatal(err)
	}
	if err := p.AddPromise(1, rep(0, nil)); err != nil {
		t.Fatal(err)
	}
	if _, err := p.Plan(); err != nil {
		t.Fatal(err)
	}
	// 已关闭时 AddPromise 优先报关闭，即使其它字段也非法。
	if err := p.AddPromise(99, PromiseReport{Ballot: 1}); !errors.Is(err, ErrClosed) {
		t.Fatalf("closed takes priority over other errors, got %v", err)
	}
}

func TestNoReturnValueAliasing(t *testing.T) {
	p, _ := NewPlanner(5, 3)
	m := map[int]AcceptedEntry{1: entry(2, "orig")}
	reports := map[int]PromiseReport{
		0: rep(0, m),
		1: rep(0, nil),
	}
	addAll(t, p, reports)
	plan, err := p.Plan()
	if err != nil {
		t.Fatal(err)
	}
	// 调用方修改原 map 不得影响已返回的计划。
	m[1] = entry(2, "mutated")
	m[9] = entry(1, "x")
	if plan.Entries[0].Value != "orig" || len(plan.Entries) != 1 {
		t.Fatalf("returned plan aliases caller storage: %+v", plan.Entries)
	}
	plan.Entries[0] = PlanEntry{}
	t.Logf("别名检查: 修改入参与返回切片后计划仍为独立副本")
}

func TestRandomDifferentialAgainstNaive(t *testing.T) {
	// 随机生成合法报告集合，与逐槽位朴素计算对拍；同时打乱到达顺序。
	rng := rand.New(rand.NewSource(42))
	for iter := 0; iter < 400; iter++ {
		n := 1 + rng.Intn(7)
		b := int64(2 + rng.Intn(6))
		p, err := NewPlanner(b, n)
		if err != nil {
			t.Fatal(err)
		}
		reports := map[int]PromiseReport{}
		count := rng.Intn(n + 1)
		perm := rng.Perm(n)
		for _, from := range perm[:count] {
			chosen := rng.Intn(4)
			accepted := map[int]AcceptedEntry{}
			for k := rng.Intn(5); k > 0; k-- {
				slot := chosen + 1 + rng.Intn(5)
				accepted[slot] = entry(1+rng.Int63n(b-1), []string{"", "a", "b", "c"}[rng.Intn(4)])
			}
			reports[from] = PromiseReport{Ballot: b, Chosen: chosen, Accepted: accepted}
		}
		addAll(t, p, reports)

		got, gotErr := p.Plan()
		want, wantErr := naivePlan(n, reports)
		switch {
		case gotErr != nil || wantErr != nil:
			gs, gok := conflictSlot(gotErr)
			ws, wok := conflictSlot(wantErr)
			if gok != wok || gs != ws {
				if !(errors.Is(gotErr, ErrNoQuorum) && errors.Is(wantErr, ErrNoQuorum)) {
					t.Fatalf("iter %d n=%d b=%d reports=%v: err mismatch got=%v want=%v",
						iter, n, b, reports, gotErr, wantErr)
				}
			}
		default:
			if !plansEqual(got, want) {
				t.Fatalf("iter %d n=%d b=%d reports=%v: got=%+v want=%+v",
					iter, n, b, reports, got, want)
			}
		}
	}
	t.Logf("随机对拍 400 组（含到达顺序打乱）全部与朴素计算一致")
}

func TestArrivalOrderIndependence(t *testing.T) {
	// 同一组报告以不同顺序加入，Plan 结果必须一致。
	reports := map[int]PromiseReport{
		0: rep(0, map[int]AcceptedEntry{1: entry(3, "x"), 2: entry(1, "y")}),
		1: rep(1, map[int]AcceptedEntry{2: entry(4, "z"), 5: entry(2, "w")}),
		2: rep(2, map[int]AcceptedEntry{3: entry(2, ""), 5: entry(3, "w")}),
		3: rep(0, nil),
	}
	orders := [][]int{{0, 1, 2, 3}, {3, 2, 1, 0}, {2, 0, 3, 1}}
	var first *RecoveryPlan
	for oi, order := range orders {
		p, _ := NewPlanner(5, 5)
		for _, from := range order {
			if err := p.AddPromise(from, reports[from]); err != nil {
				t.Fatal(err)
			}
		}
		plan, err := p.Plan()
		if err != nil {
			t.Fatalf("order %d: %v", oi, err)
		}
		if oi == 0 {
			first = plan
			logPlan(t, plan, nil, "基准顺序结果；其余到达顺序须逐项相同")
		} else if !plansEqual(first, plan) {
			t.Fatalf("order %d = %+v, want %+v", oi, plan, first)
		}
	}
}

func TestConcurrentPlanOnlyOneSucceeds(t *testing.T) {
	// 并发 AddPromise + Plan：至多一个 Plan 成功，其余报 ErrClosed。
	p, _ := NewPlanner(5, 5)
	var wg sync.WaitGroup
	start := make(chan struct{})

	for from := 0; from < 5; from++ {
		wg.Add(1)
		go func(from int) {
			defer wg.Done()
			<-start
			_ = p.AddPromise(from, rep(0, map[int]AcceptedEntry{1: entry(int64(1+from%4+1), "v")}))
		}(from)
	}
	results := make(chan error, 16)
	for i := 0; i < 16; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			_, err := p.Plan()
			results <- err
		}()
	}
	close(start)
	wg.Wait()
	close(results)

	successes, closed, noQuorum := 0, 0, 0
	for err := range results {
		switch {
		case err == nil:
			successes++
		case errors.Is(err, ErrClosed):
			closed++
		case errors.Is(err, ErrNoQuorum):
			noQuorum++
		default:
			t.Fatalf("unexpected Plan error: %v", err)
		}
	}
	t.Logf("并发 Plan 结果: 成功=%d 已关闭=%d 不足多数派=%d", successes, closed, noQuorum)
	if successes > 1 {
		t.Fatalf("at most one concurrent Plan may succeed, got %d", successes)
	}
}

func TestChosenStartIgnoresOldSlots(t *testing.T) {
	// chosen 的最大值决定 start；低于 start 的已接受项被忽略。
	p, _ := NewPlanner(5, 3)
	reports := map[int]PromiseReport{
		0: rep(1, map[int]AcceptedEntry{2: entry(1, "old-a"), 3: entry(2, "keep-superseded")}),
		1: rep(3, map[int]AcceptedEntry{4: entry(3, "late")}),
	}
	logReports(t, reports)
	addAll(t, p, reports)

	plan, err := p.Plan()
	if err != nil {
		t.Fatalf("Plan: %v", err)
	}
	logPlan(t, plan, nil, "start=max(1,3)+1=4；接受者0的槽2、3<start 被忽略；恢复区间[4,4]，沿用 late")
	if plan.Start != 4 || plan.NextFree != 5 || len(plan.Entries) != 1 {
		t.Fatalf("unexpected plan: %+v", plan)
	}
	if e := plan.Entries[0]; e.Slot != 4 || e.Noop || e.Value != "late" || e.SourceBallot != 3 {
		t.Fatalf("entry = %+v", e)
	}
	wantPlan, _ := naivePlan(3, reports)
	if !plansEqual(plan, wantPlan) {
		t.Fatalf("plan %+v != naive %+v", plan, wantPlan)
	}
}

func TestMaxSlotBeforeStart(t *testing.T) {
	// maxSlot(0) < start(6)：无恢复槽位，nextFree == start。
	p, _ := NewPlanner(5, 3)
	reports := map[int]PromiseReport{
		0: rep(5, nil),
		1: rep(2, nil),
	}
	logReports(t, reports)
	addAll(t, p, reports)

	plan, err := p.Plan()
	if err != nil {
		t.Fatalf("Plan: %v", err)
	}
	logPlan(t, plan, nil, "maxSlot=0 < start=6；恢复区间为空；nextFree=max(0,5)+1=6==start")
	if plan.Start != 6 || plan.NextFree != 6 || len(plan.Entries) != 0 {
		t.Fatalf("unexpected plan: %+v", plan)
	}
	wantPlan0, _ := naivePlan(3, reports)
	if !plansEqual(plan, wantPlan0) {
		t.Fatalf("plan %+v != naive %+v", plan, wantPlan0)
	}
}

func TestSameBallotConflictAndAgreement(t *testing.T) {
	// 同一最大选票下相同值不冲突；不同值冲突且报最小冲突槽位。
	p, _ := NewPlanner(5, 5)
	reports := map[int]PromiseReport{
		0: rep(0, map[int]AcceptedEntry{1: entry(4, "ok"), 3: entry(4, "aaa")}),
		1: rep(0, map[int]AcceptedEntry{1: entry(4, "ok"), 3: entry(4, "bbb")}),
		2: rep(0, map[int]AcceptedEntry{2: entry(2, "low")}),
	}
	logReports(t, reports)
	addAll(t, p, reports)

	_, err := p.Plan()
	logPlan(t, nil, err, "槽1最大选票4两值相同不冲突；槽3最大选票4两值不同=>冲突，最小冲突槽=3")
	slot, ok := conflictSlot(err)
	if !ok || slot != 3 {
		t.Fatalf("want ConflictError at slot 3, got %v", err)
	}
	// Plan 失败不关闭规划器。
	if err := p.AddPromise(3, rep(0, nil)); err != nil {
		t.Fatalf("planner must stay open after failed Plan, got %v", err)
	}

	p2, _ := NewPlanner(5, 3)
	r2 := map[int]PromiseReport{
		0: rep(0, map[int]AcceptedEntry{1: entry(4, "ok")}),
		1: rep(0, map[int]AcceptedEntry{1: entry(4, "ok")}),
	}
	addAll(t, p2, r2)
	plan, err := p2.Plan()
	if err != nil {
		t.Fatalf("same value at same max ballot must not conflict: %v", err)
	}
	logPlan(t, plan, nil, "槽1最大选票4的两项值均为 ok，不冲突")
	if plan.Entries[0].Value != "ok" || plan.Entries[0].SourceBallot != 4 {
		t.Fatalf("entry = %+v", plan.Entries[0])
	}
}

func TestLowerBallotDifferencesDoNotConflict(t *testing.T) {
	// 较低选票处的值不同不冲突：最高选票值胜出。
	p, _ := NewPlanner(5, 3)
	reports := map[int]PromiseReport{
		0: rep(0, map[int]AcceptedEntry{1: entry(1, "a"), 2: entry(4, "win")}),
		1: rep(0, map[int]AcceptedEntry{1: entry(2, "b"), 2: entry(3, "lose")}),
	}
	logReports(t, reports)
	addAll(t, p, reports)

	plan, err := p.Plan()
	if err != nil {
		t.Fatalf("Plan: %v", err)
	}
	logPlan(t, plan, nil, "槽1最高选票2=>b（选票1的a较低，不参与冲突）；槽2最高选票4=>win")
	want := []PlanEntry{
		{Slot: 1, Value: "b", SourceBallot: 2},
		{Slot: 2, Value: "win", SourceBallot: 4},
	}
	for i := range want {
		if plan.Entries[i] != want[i] {
			t.Fatalf("entry %d = %+v, want %+v", i, plan.Entries[i], want[i])
		}
	}
	wantPlan, _ := naivePlan(3, reports)
	if !plansEqual(plan, wantPlan) {
		t.Fatalf("plan %+v != naive %+v", plan, wantPlan)
	}
}
