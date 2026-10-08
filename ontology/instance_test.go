package ontology

import (
	"reflect"
	"testing"
)

// mkReq 构造一个测试写请求。
func mkReq(id string, base uint64, maxRetries uint64) *WriteRequest {
	return &WriteRequest{
		ID:          RequestID(id),
		ObjectID:    "o",
		BaseVersion: base,
		Patch:       map[string]any{"owner": id},
		MaxRetries:  maxRetries,
	}
}

// 无竞争且版本匹配时应走快速路径直接提交。
func TestFastPathCommit(t *testing.T) {
	inst := newInstance()
	r := inst.attempt(mkReq("a", 0, 3))
	if r.Outcome != OutcomeCommitted || r.Version != 1 {
		t.Fatalf("快速路径结果 = %v, version = %d", r.Outcome, r.Version)
	}
	snap := inst.snapshot()
	if snap.Version != 1 || snap.Props["owner"] != "a" {
		t.Fatalf("提交后状态不正确: %+v", snap)
	}
	if err := ReplayDecisions(inst.log); err != nil {
		t.Fatalf("重放校验失败: %v", err)
	}
}

// 无竞争但版本过期时应返回写冲突，且不产生任何可观察状态变化。
func TestConflictThenCommit(t *testing.T) {
	inst := newInstance()
	inst.attempt(mkReq("setup", 0, 3)) // 提交，版本推进到 1

	before := inst.snapshot()
	stale := mkReq("w", 0, 3)
	r := inst.attempt(stale)
	if r.Outcome != OutcomeConflict {
		t.Fatalf("过期版本应返回冲突，实际为 %v", r.Outcome)
	}
	after := inst.snapshot()
	if !reflect.DeepEqual(before, after) {
		t.Fatalf("冲突尝试产生了可观察状态变化: before=%+v after=%+v", before, after)
	}
	if stale.Ticket.Failures != 1 {
		t.Fatalf("冲突后失败次数应为 1，实际为 %d", stale.Ticket.Failures)
	}

	// 重新基于最新版本重试应提交成功。
	stale.BaseVersion = 1
	r = inst.attempt(stale)
	if r.Outcome != OutcomeCommitted || r.Version != 2 {
		t.Fatalf("冲突后重试应提交，实际为 %v (version %d)", r.Outcome, r.Version)
	}
	if err := ReplayDecisions(inst.log); err != nil {
		t.Fatalf("重放校验失败: %v", err)
	}
}

// 持续竞争下，最后到达的请求被反复挤出，其优先级依据严格递增，
// 失败次数不超过同时在竞争者的最大数量，最终必然提交。
func TestVictimRepeatedlyPreemptedThenCommits(t *testing.T) {
	const n = 8 // 竞争者总数（含受害者）
	inst := newInstance()
	inst.attempt(mkReq("setup", 0, 3)) // 版本推进到 1

	// n 个请求均以过期版本到达：第一个冲突登记，其余登记即被挤出。
	reqs := make([]*WriteRequest, n)
	for i := 0; i < n; i++ {
		id := string(rune('a' + i))
		reqs[i] = mkReq(id, 0, uint64(n))
		r := inst.attempt(reqs[i])
		want := OutcomePreempted
		if i == 0 {
			want = OutcomeConflict
		}
		if r.Outcome != want {
			t.Fatalf("请求 %s 首次尝试结果 = %v，应为 %v", id, r.Outcome, want)
		}
	}
	victim := reqs[n-1]

	maxContenders := 0
	for _, d := range inst.log {
		if len(d.Contenders) > maxContenders {
			maxContenders = len(d.Contenders)
		}
	}
	if maxContenders != n {
		t.Fatalf("最大竞争者数 = %d，应为 %d", maxContenders, n)
	}

	// 前 n-1 个请求依次提交；每次提交后受害者重试均被挤出。
	prevScore := victim.Ticket.Score(inst.seq)
	for i := 0; i < n-1; i++ {
		reqs[i].BaseVersion = inst.snapshot().Version
		if r := inst.attempt(reqs[i]); r.Outcome != OutcomeCommitted {
			t.Fatalf("请求 %d 应提交，实际为 %v", i, r.Outcome)
		}
		victim.BaseVersion = inst.snapshot().Version
		r := inst.attempt(victim)
		if r.Outcome != OutcomePreempted {
			t.Fatalf("受害者在第 %d 轮应被挤出，实际为 %v", i, r.Outcome)
		}
		// 优先级依据必须严格提升，不得被重置。
		if r.Ticket.Failures != uint64(i+2) {
			t.Fatalf("受害者失败次数 = %d，应为 %d", r.Ticket.Failures, i+2)
		}
		if r.Score <= prevScore {
			t.Fatalf("受害者分数未严格提升: prev=%d now=%d", prevScore, r.Score)
		}
		prevScore = r.Score
	}
	if got := victim.Ticket.Failures; got != uint64(n) {
		t.Fatalf("受害者失败次数 = %d，应为 %d", got, n)
	}
	// 上界：失败次数不超过同时在竞争者的最大数量。
	if victim.Ticket.Failures > uint64(maxContenders) {
		t.Fatalf("失败次数 %d 超过竞争者上界 %d", victim.Ticket.Failures, maxContenders)
	}

	// 所有竞争者离开后，受害者成为最高优先级者并提交。
	victim.BaseVersion = inst.snapshot().Version
	if r := inst.attempt(victim); r.Outcome != OutcomeCommitted {
		t.Fatalf("受害者最终应提交，实际为 %v", r.Outcome)
	}
	if err := ReplayDecisions(inst.log); err != nil {
		t.Fatalf("重放校验失败: %v", err)
	}
}

// 老化反超：先到达但停滞不前的请求，会被失败次数不断增长的后到者反超。
// 这证明票据在重试间持续累积而不是每轮从零计算。
func TestAgingOvertakesStalledHead(t *testing.T) {
	inst := newInstance()
	inst.attempt(mkReq("setup", 0, 3)) // 版本推进到 1

	stalled := mkReq("stalled", 0, 10)
	if r := inst.attempt(stalled); r.Outcome != OutcomeConflict {
		t.Fatalf("stalled 首次应冲突，实际为 %v", r.Outcome)
	}
	active := mkReq("active", 0, 10)
	if r := inst.attempt(active); r.Outcome != OutcomePreempted {
		t.Fatalf("active 首次应被挤出，实际为 %v", r.Outcome)
	}

	// stalled 停滞不前（不再尝试）；active 不断重试，失败次数累积。
	// 当 active 的有效优先级（failures - arrivalSeq）超过 stalled 时发生反超。
	var r attemptResult
	for i := 0; i < 3; i++ {
		active.BaseVersion = 1
		r = inst.attempt(active)
	}
	if r.Outcome != OutcomeCommitted {
		t.Fatalf("active 积累足够失败次数后应反超提交，实际为 %v", r.Outcome)
	}
	if active.Ticket.Failures != 3 {
		t.Fatalf("active 提交时失败次数 = %d，应为 3", active.Ticket.Failures)
	}
	// stalled 之后仍可提交。
	stalled.BaseVersion = inst.snapshot().Version
	if r := inst.attempt(stalled); r.Outcome != OutcomeCommitted {
		t.Fatalf("stalled 最终应提交，实际为 %v", r.Outcome)
	}
	if err := ReplayDecisions(inst.log); err != nil {
		t.Fatalf("重放校验失败: %v", err)
	}
}
