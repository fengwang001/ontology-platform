package ontology

import (
	"errors"
	"testing"
)

const testLink = "ownedBy"

func newTestStore(t *testing.T, max int) (*Store, *Submitter) {
	t.Helper()
	store := NewStore()
	store.AddObject("o")
	store.AddCardinality("o", Cardinality{LinkType: testLink, Direction: Outgoing, Max: max})
	return store, NewSubmitter(store, RetryPolicy{MaxAttempts: 4})
}

func addChange(other string) Change {
	return Change{ObjectID: "o", BaseVersion: 0, Ops: []LinkOp{
		{LinkType: testLink, Direction: Outgoing, OtherID: other, Add: true},
	}}
}

// 基数约束恰好用尽：第一个请求占走唯一名额，第二个请求立即业务拒绝。
func TestCardinalityExactlyFilled(t *testing.T) {
	store, submitter := newTestStore(t, 1)

	res, err := submitter.Submit(addChange("a"))
	if err != nil || !res.Committed {
		t.Fatalf("first add must commit: %v", err)
	}

	// 调用方拿到新基线后再尝试第二个关联。
	ch := addChange("b")
	ch.BaseVersion = res.Version
	res2, err := submitter.Submit(ch)
	if !errors.Is(err, ErrCardinality) {
		t.Fatalf("want cardinality rejection, got %v", err)
	}
	if res2.Committed || res2.Reason != ReasonCardinality {
		t.Fatalf("want business rejection, got %+v", res2)
	}
	if len(res2.Attempts) != 1 {
		t.Fatalf("cardinality rejection must terminate immediately, attempts=%d", len(res2.Attempts))
	}
	v := res2.Attempts[0].Violations
	if len(v) != 1 || v[0].Current != 1 || v[0].Projected != 2 || v[0].Max != 1 {
		t.Fatalf("bad violation basis: %+v", v)
	}

	final := store.Snapshot("o")
	if final.Counts[keyOf(testLink, Outgoing)] != 1 || !final.Links[keyOf(testLink, Outgoing)]["a"] {
		t.Fatalf("state changed by rejected request: %+v", final.Links)
	}
}

// 恰好空出一个名额：首次尝试时名额被占（版本冲突），重试前他人删除关联
// 空出唯一名额，重试必须基于最新读取成功，且以本次结果为准。
func TestRetrySeesFreedSlotAndReusesNothing(t *testing.T) {
	store, submitter := newTestStore(t, 1)

	// 预置：a 已占用唯一名额，实例版本为 1。
	if _, err := submitter.Submit(addChange("a")); err != nil {
		t.Fatal(err)
	}

	// 受害者基线 1：第一次尝试读到“已满”前，b 请求在冲突窗口先抢到版本 2；
	// 但 b 的添加因基数满会被业务拒绝，无法推进版本。为让版本推进到 2，
	// 用一个“先占满另一对端再由他人腾出”的脚本：
	// attempt1 窗口：他人将 a 替换为 x（先删 a 再加 x，一次提交，版本 2）→
	// 受害者在锁内复核发现版本冲突（名额仍满，且本次不做基数判定）；
	// attempt2 窗口：他人删除 x（版本 3，名额空出）→ 受害者基于最新读取成功。
	hooked := submitter.WithHook(func(change Change, attempt int, read *Snapshot) {
		switch attempt {
		case 1:
			replace := Change{ObjectID: "o", BaseVersion: read.Version, Ops: []LinkOp{
				{LinkType: testLink, Direction: Outgoing, OtherID: "a", Add: false},
				{LinkType: testLink, Direction: Outgoing, OtherID: "x", Add: true},
			}}
			if _, err := NewSubmitter(store, RetryPolicy{MaxAttempts: 1}).Submit(replace); err != nil {
				t.Fatalf("setup replace: %v", err)
			}
		case 2:
			remove := Change{ObjectID: "o", BaseVersion: read.Version, Ops: []LinkOp{
				{LinkType: testLink, Direction: Outgoing, OtherID: "x", Add: false},
			}}
			if _, err := NewSubmitter(store, RetryPolicy{MaxAttempts: 1}).Submit(remove); err != nil {
				t.Fatalf("setup remove: %v", err)
			}
		}
	})

	victim := addChange("v")
	victim.BaseVersion = 1
	res, err := hooked.Submit(victim)
	if err != nil || !res.Committed {
		t.Fatalf("retry must succeed after slot frees: %v", err)
	}
	// attempt1：读到满(a)，窗口内替换为 x（版本2）→锁内复核冲突；
	// attempt2：读到满(x)，窗口内删除 x（版本3）→锁内以新版本读到空，
	// 但因版本冲突优先，本次即便基数已满足也不提交；
	// attempt3：重新读到空且版本匹配→提交。
	if len(res.Attempts) != 3 {
		t.Fatalf("want 3 attempts, got %d", len(res.Attempts))
	}
	for i, d := range res.Attempts[:2] {
		if d.Reason != ReasonVersionConflict {
			t.Fatalf("attempt %d must be version conflict, got %v", i+1, d.Reason)
		}
	}
	// 关键证据：第 1 次尝试记录的快照为“满（a 或 x）”，第 2 次为“空”，
	// 判定使用的是第 2 次重新读取的结果，前一次计数未被复用。
	if res.Attempts[0].Read.Counts[keyOf(testLink, Outgoing)] != 1 {
		t.Fatalf("attempt1 read must show count 1, got %+v", res.Attempts[0].Read.Counts)
	}
	if res.Attempts[1].Read.Counts[keyOf(testLink, Outgoing)] != 0 {
		t.Fatalf("attempt2 in-lock read must show count 0 after x removed, got %+v", res.Attempts[1].Read.Counts)
	}
	if res.Attempts[2].Read.Counts[keyOf(testLink, Outgoing)] != 0 {
		t.Fatalf("attempt3 read must show count 0, got %+v", res.Attempts[2].Read.Counts)
	}
	// 第 2 次尝试已读到“基数满足”，但版本冲突优先，绝不能拿着
	// 这次读结果直接提交；真正提交基于第 3 次独立的重新读取。

	final := store.Snapshot("o")
	if final.Counts[keyOf(testLink, Outgoing)] != 1 || !final.Links[keyOf(testLink, Outgoing)]["v"] {
		t.Fatalf("final state must contain only v: %+v", final.Links)
	}
}

// 重试过程中基数状态反复变化：满→空→满。受害者在第 2 次窗口看到空，
// 但第 2 次尝试在进入锁前又被他人占满（版本冲突）；第 3 次窗口再次
// 空出名额，受害者成功。要求每一次的读集合都独立且完整记录。
func TestRetryCardinalityOscillates(t *testing.T) {
	store, submitter := newTestStore(t, 1)
	if _, err := submitter.Submit(addChange("a")); err != nil {
		t.Fatal(err)
	}

	attemptReads := []int{}
	hooked := submitter.WithHook(func(change Change, attempt int, read *Snapshot) {
		attemptReads = append(attemptReads, read.Counts[keyOf(testLink, Outgoing)])
		switch attempt {
		case 1:
			// 满→满（替换对端），制造版本冲突。
			mustCommit(t, store, read.Version, false, "a", "x")
		case 2:
			// 空：删除 x；随后立刻由另一个提交重新占满（z），
			// 受害者本次仍将版本冲突。
			mustCommit(t, store, read.Version, true, "x", "")
			fresh := store.Snapshot("o")
			mustCommit(t, store, fresh.Version, false, "", "z")
		case 3:
			// 再次空出名额。
			mustCommit(t, store, read.Version, true, "z", "")
		}
	})

	victim := addChange("v")
	victim.BaseVersion = 1
	res, err := hooked.Submit(victim)
	if err != nil || !res.Committed {
		t.Fatalf("want success on 3rd attempt, got %v", err)
	}
	// attempt1: 读到满(a)，窗口 a→x（满）→冲突；
	// attempt2: 读到满(x)，窗口删 x 再加 z（满）→冲突；
	// attempt3: 读到满(z)，窗口删 z（空）→冲突；
	// attempt4: 读到空→提交。
	if len(res.Attempts) != 4 {
		t.Fatalf("want 4 attempts, got %d", len(res.Attempts))
	}
	for i, d := range res.Attempts[:3] {
		if d.Reason != ReasonVersionConflict {
			t.Fatalf("attempt %d must be conflict, got %v", i+1, d.Reason)
		}
	}
	// hook 在每次尝试改动前看到的计数：满、满、满、空——反复变化
	// 体现在尝试之间（每次的读都是独立重新读取，且与上一次不同内容）。
	want := []int{1, 1, 1, 0}
	if len(attemptReads) != 4 {
		t.Fatalf("hook reads %v", attemptReads)
	}
	for i := range want {
		if attemptReads[i] != want[i] {
			t.Fatalf("hook read %d = %d want %d (full trace %v)", i, attemptReads[i], want[i], attemptReads)
		}
	}

	final := store.Snapshot("o")
	if !final.Links[keyOf(testLink, Outgoing)]["v"] || final.Counts[keyOf(testLink, Outgoing)] != 1 {
		t.Fatalf("final must be only v: %+v", final.Links)
	}
}

// 重试预算恰好耗尽：预算为 3，前 3 次尝试全部因版本被推进而失败，
// 必须返回 ErrRetriesExhausted（而不是基数拒绝），且实例最终状态
// 与完全没有发生过这次请求不可区分（除被他人合法推进的部分外）。
func TestRetryBudgetExactlyExhausted(t *testing.T) {
	store, _ := newTestStore(t, 100)

	bumped := 0
	hooked := NewSubmitter(store, RetryPolicy{MaxAttempts: 3}).WithHook(
		func(change Change, attempt int, read *Snapshot) {
			// 每次窗口都由他人提交一个无关变更推进版本，且始终保有名额，
			// 从而排除“基数不满足”这一解释，唯一终止原因只能是耗尽。
			otherID := "w"
			ch := Change{ObjectID: "o", BaseVersion: read.Version, Ops: []LinkOp{
				{LinkType: testLink, Direction: Outgoing, OtherID: otherID, Add: bumped%2 == 0},
			}}
			if bumped%2 == 1 {
				ch.Ops[0].Add = false
			}
			if _, err := NewSubmitter(store, RetryPolicy{MaxAttempts: 1}).Submit(ch); err != nil {
				t.Fatalf("bumper: %v", err)
			}
			bumped++
		})

	res, err := hooked.Submit(addChange("v"))
	if !errors.Is(err, ErrRetriesExhausted) {
		t.Fatalf("want retries exhausted, got %v", err)
	}
	if res.Reason != ReasonRetriesExhausted || len(res.Attempts) != 3 {
		t.Fatalf("want 3 conflict attempts then exhaustion, got %+v", res.Attempts)
	}
	for i, d := range res.Attempts {
		if d.Reason != ReasonVersionConflict {
			t.Fatalf("attempt %d must be conflict, got %v", i+1, d.Reason)
		}
	}

	// 受害者的关联从未写入。
	final := store.Snapshot("o")
	if final.Links[keyOf(testLink, Outgoing)]["v"] {
		t.Fatalf("exhausted request must leave no link of its own")
	}
	// 逻辑时钟推进次数恰好等于他人的 3 次提交，受害者的 3 次失败尝试
	// 没有推进任何时钟/版本。
	if got := store.Clock(); got != uint64(bumped) {
		t.Fatalf("clock=%d, only %d competing commits may be observable", got, bumped)
	}
}

// 单次尝试策略下基线落后：直接版本冲突，不允许伪装成耗尽。
func TestSingleAttemptStaleIsVersionConflict(t *testing.T) {
	store, submitter := newTestStore(t, 100)
	if _, err := submitter.Submit(addChange("a")); err != nil {
		t.Fatal(err)
	}
	stale := addChange("b") // BaseVersion 仍为 0，当前已是 1
	res, err := NewSubmitter(store, RetryPolicy{MaxAttempts: 1}).Submit(stale)
	if !errors.Is(err, ErrVersionConflict) {
		t.Fatalf("want version conflict, got %v", err)
	}
	var vc *VersionConflictError
	if !errors.As(err, &vc) || vc.LatestVersion != 1 || vc.BaseVersion != 0 {
		t.Fatalf("bad conflict detail: %v", err)
	}
	if res.Attempts[0].Read.Version != 1 {
		t.Fatal("conflict decision must record latest read")
	}
}

// 失败的中间尝试不得改变任何外部可观察状态：版本、关联、时钟。
func TestFailedAttemptsAreInvisible(t *testing.T) {
	store, submitter := newTestStore(t, 1)
	if _, err := submitter.Submit(addChange("a")); err != nil {
		t.Fatal(err)
	}

	ch := addChange("b")
	ch.BaseVersion = 1 // 已满，且版本匹配 → 直接基数拒绝
	before := store.Snapshot("o")
	clockBefore := store.Clock()
	res, err := submitter.Submit(ch)
	if !errors.Is(err, ErrCardinality) {
		t.Fatalf("want cardinality, got %v", err)
	}
	after := store.Snapshot("o")
	if after.Version != before.Version || store.Clock() != clockBefore {
		t.Fatal("failed attempt advanced version or clock")
	}
	if len(after.Links) != len(before.Links) {
		t.Fatal("failed attempt changed links")
	}
	_ = res
}

func mustCommit(t *testing.T, store *Store, base int64, remove bool, del, add string) {
	t.Helper()
	var ops []LinkOp
	if del != "" {
		ops = append(ops, LinkOp{LinkType: testLink, Direction: Outgoing, OtherID: del, Add: false})
	}
	if add != "" {
		ops = append(ops, LinkOp{LinkType: testLink, Direction: Outgoing, OtherID: add, Add: true})
	}
	_, err := NewSubmitter(store, RetryPolicy{MaxAttempts: 1}).Submit(
		Change{ObjectID: "o", BaseVersion: base, Ops: ops})
	if err != nil {
		t.Fatalf("helper commit failed: %v", err)
	}
}
