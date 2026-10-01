package coordinator

import (
	"errors"
	"slices"
	"testing"
)

const testTimeoutMs = 1000

func requireState(t *testing.T, c *Coordinator, want State, wantGen int) {
	t.Helper()
	if got := c.State(); got != want {
		t.Fatalf("状态 = %v, 期望 %v", got, want)
	}
	if got := c.Generation(); got != wantGen {
		t.Fatalf("代数 = %d, 期望 %d", got, wantGen)
	}
}

// TestTimeoutExactlyTCompletes 验证推进时刻恰等于开始时刻加 T 时完成本轮。
func TestTimeoutExactlyTCompletes(t *testing.T) {
	c := New(testTimeoutMs)
	if err := c.Join("A", 0); err != nil {
		t.Fatal(err)
	}
	requireState(t, c, StateAwaitingSync, 1) // 单成员立即完成第 1 代

	if err := c.Join("B", 100); err != nil {
		t.Fatal(err)
	}
	requireState(t, c, StatePreparing, 1)

	if err := c.Tick(100 + testTimeoutMs - 1); err != nil {
		t.Fatal(err)
	}
	requireState(t, c, StatePreparing, 1)

	// now == start + T，恰等于超时边界，必须完成。
	if err := c.Tick(100 + testTimeoutMs); err != nil {
		t.Fatal(err)
	}
	requireState(t, c, StateAwaitingSync, 2)
	if got := c.Leader(); got != "B" {
		t.Fatalf("领导者 = %q, 期望 B（本轮唯一加入者）", got)
	}
	if got := c.Members(); !slices.Equal(got, []string{"B"}) {
		t.Fatalf("成员 = %v, 期望 [B]（A 未在本轮加入被移除）", got)
	}
}

// TestLastMemberJoinCompletes 验证最后一个已知成员加入时在该次调用内立即完成。
func TestLastMemberJoinCompletes(t *testing.T) {
	c := New(testTimeoutMs)
	if err := c.Join("A", 0); err != nil {
		t.Fatal(err)
	}
	if err := c.Join("B", 10); err != nil {
		t.Fatal(err)
	}
	requireState(t, c, StatePreparing, 1)

	// A 是最后一个未加入的已知成员，本次调用内立即完成。
	if err := c.Join("A", 20); err != nil {
		t.Fatal(err)
	}
	requireState(t, c, StateAwaitingSync, 2)
	if got := c.Leader(); got != "B" {
		t.Fatalf("领导者 = %q, 期望 B（本轮加入序最早）", got)
	}
}

// TestLeaveCompletesRemaining 验证准备中离开使剩余已知成员齐备时立即完成。
func TestLeaveCompletesRemaining(t *testing.T) {
	c := New(testTimeoutMs)
	if err := c.Join("A", 0); err != nil {
		t.Fatal(err)
	}
	if err := c.Join("B", 10); err != nil {
		t.Fatal(err)
	}
	if err := c.Join("C", 20); err != nil {
		t.Fatal(err)
	}
	requireState(t, c, StatePreparing, 1) // 已知 {A,B,C}，本轮已加入 {B,C}

	// A 离开后剩余已知成员 {B,C} 都已在本轮加入，立即完成。
	if err := c.Leave("A", 30); err != nil {
		t.Fatal(err)
	}
	requireState(t, c, StateAwaitingSync, 2)
	if got := c.Leader(); got != "B" {
		t.Fatalf("领导者 = %q, 期望 B", got)
	}
	if got := c.Members(); !slices.Equal(got, []string{"B", "C"}) {
		t.Fatalf("成员 = %v, 期望 [B C]", got)
	}
}

// TestDuplicateJoinKeepsOrder 验证重复加入幂等且不改变本轮加入序。
func TestDuplicateJoinKeepsOrder(t *testing.T) {
	c := New(testTimeoutMs)
	if err := c.Join("A", 0); err != nil {
		t.Fatal(err)
	}
	if err := c.Join("B", 10); err != nil {
		t.Fatal(err)
	}
	if err := c.Join("C", 20); err != nil {
		t.Fatal(err)
	}
	requireState(t, c, StatePreparing, 1)

	// B 重复加入：幂等，不改变次序，也不触发完成。
	if err := c.Join("B", 30); err != nil {
		t.Fatal(err)
	}
	requireState(t, c, StatePreparing, 1)
	if got := c.JoinOrder(); !slices.Equal(got, []string{"B", "C"}) {
		t.Fatalf("加入序 = %v, 期望 [B C]", got)
	}

	if err := c.Join("A", 40); err != nil {
		t.Fatal(err)
	}
	requireState(t, c, StateAwaitingSync, 2)
	if got := c.JoinOrder(); !slices.Equal(got, []string{"B", "C", "A"}) {
		t.Fatalf("加入序 = %v, 期望 [B C A]", got)
	}
	if got := c.Leader(); got != "B" {
		t.Fatalf("领导者 = %q, 期望 B", got)
	}
}

// TestLeaderIsEarliestJoinerNotPreviousLeader 验证领导者取本轮加入序
// 最早者，而非上一代领导者。
func TestLeaderIsEarliestJoinerNotPreviousLeader(t *testing.T) {
	c := New(testTimeoutMs)
	if err := c.Join("A", 0); err != nil {
		t.Fatal(err)
	}
	requireState(t, c, StateAwaitingSync, 1)
	if got := c.Leader(); got != "A" {
		t.Fatalf("第 1 代领导者 = %q, 期望 A", got)
	}

	// 新一轮中 B 最先加入，A 随后；领导者应为 B 而非上一代领导者 A。
	if err := c.Join("B", 10); err != nil {
		t.Fatal(err)
	}
	if err := c.Join("A", 20); err != nil {
		t.Fatal(err)
	}
	requireState(t, c, StateAwaitingSync, 2)
	if got := c.Leader(); got != "B" {
		t.Fatalf("第 2 代领导者 = %q, 期望 B（本轮加入序最早者）", got)
	}
}

// TestNewMemberJoinInAwaitingSyncReopensRound 验证等待分配状态下新成员
// 加入回到准备中，且代数不变，直到再次完成。
func TestNewMemberJoinInAwaitingSyncReopensRound(t *testing.T) {
	c := New(testTimeoutMs)
	if err := c.Join("A", 0); err != nil {
		t.Fatal(err)
	}
	requireState(t, c, StateAwaitingSync, 1)

	if err := c.Join("B", 100); err != nil {
		t.Fatal(err)
	}
	requireState(t, c, StatePreparing, 1) // 回到准备中，代数未变

	// 未完成前代数保持为 1。
	if err := c.Tick(100 + testTimeoutMs - 1); err != nil {
		t.Fatal(err)
	}
	requireState(t, c, StatePreparing, 1)

	// 超时完成后代数才递增到 2。
	if err := c.Tick(100 + testTimeoutMs); err != nil {
		t.Fatal(err)
	}
	requireState(t, c, StateAwaitingSync, 2)
}

// stableGroup 构造一个包含 A、B 且已提交分配进入稳定状态的组，
// 返回协调者与分配表。第 2 代领导者为 B。
func stableGroup(t *testing.T) (*Coordinator, Assignment) {
	t.Helper()
	c := New(testTimeoutMs)
	for i, m := range []string{"A", "B", "A"} {
		if err := c.Join(m, int64(i*10)); err != nil {
			t.Fatal(err)
		}
	}
	requireState(t, c, StateAwaitingSync, 2)
	assignment := Assignment{"A": {"p0"}, "B": {"p1"}}
	if _, err := c.Sync("B", 2, assignment); err != nil {
		t.Fatalf("领导者提交分配失败: %v", err)
	}
	requireState(t, c, StateStable, 2)
	return c, assignment
}

// TestSyncFlow 验证等待分配与稳定状态下的同步行为。
func TestSyncFlow(t *testing.T) {
	c := New(testTimeoutMs)
	for i, m := range []string{"A", "B", "A"} {
		if err := c.Join(m, int64(i*10)); err != nil {
			t.Fatal(err)
		}
	}
	requireState(t, c, StateAwaitingSync, 2) // 领导者为 B

	// 非领导者仅查询：尚未就绪。
	if _, err := c.Sync("A", 2, nil); !errors.Is(err, ErrNotReady) {
		t.Fatalf("非领导者查询 err = %v, 期望 ErrNotReady", err)
	}
	// 领导者仅查询（空分配表）：同样尚未就绪。
	if _, err := c.Sync("B", 2, nil); !errors.Is(err, ErrNotReady) {
		t.Fatalf("领导者查询 err = %v, 期望 ErrNotReady", err)
	}
	// 非领导者提交非空分配表：非领导者提交。
	if _, err := c.Sync("A", 2, Assignment{"A": {"p0"}, "B": {"p1"}}); !errors.Is(err, ErrNotLeader) {
		t.Fatalf("非领导者提交 err = %v, 期望 ErrNotLeader", err)
	}
	// 领导者提交成员集不符的分配表。
	if _, err := c.Sync("B", 2, Assignment{"B": {"p0"}}); !errors.Is(err, ErrAssignmentMismatch) {
		t.Fatalf("成员集不符 err = %v, 期望 ErrAssignmentMismatch", err)
	}
	if _, err := c.Sync("B", 2, Assignment{"A": {"p0"}, "B": {"p1"}, "C": {"p2"}}); !errors.Is(err, ErrAssignmentMismatch) {
		t.Fatalf("成员集超集 err = %v, 期望 ErrAssignmentMismatch", err)
	}
	// 领导者提交合法分配表：进入稳定并返回自己的分配。
	own, err := c.Sync("B", 2, Assignment{"A": {"p0"}, "B": {"p1"}})
	if err != nil {
		t.Fatalf("领导者提交失败: %v", err)
	}
	if !slices.Equal(own, []string{"p1"}) {
		t.Fatalf("领导者自身分配 = %v, 期望 [p1]", own)
	}
	requireState(t, c, StateStable, 2)

	// 稳定状态下成员同步返回自己的分配。
	if own, err := c.Sync("A", 2, nil); err != nil || !slices.Equal(own, []string{"p0"}) {
		t.Fatalf("稳定状态同步 = %v, %v, 期望 [p0], nil", own, err)
	}
}

// TestSyncCheckOrder 验证同步按未知成员、代数过期的顺序检查。
func TestSyncCheckOrder(t *testing.T) {
	c, _ := stableGroup(t)

	if _, err := c.Sync("X", 999, nil); !errors.Is(err, ErrUnknownMember) {
		t.Fatalf("未知成员 err = %v, 期望 ErrUnknownMember", err)
	}
	if _, err := c.Sync("A", 1, nil); !errors.Is(err, ErrStaleGeneration) {
		t.Fatalf("代数过期 err = %v, 期望 ErrStaleGeneration", err)
	}

	// 准备中：已知成员且代数正确时报需重新加入。
	if err := c.Leave("A", 100); err != nil {
		t.Fatal(err)
	}
	requireState(t, c, StatePreparing, 2)
	if _, err := c.Sync("B", 2, nil); !errors.Is(err, ErrRejoinNeeded) {
		t.Fatalf("准备中同步 err = %v, 期望 ErrRejoinNeeded", err)
	}
	// 准备中但代数不等：优先报代数过期。
	if _, err := c.Sync("B", 1, nil); !errors.Is(err, ErrStaleGeneration) {
		t.Fatalf("准备中代数不等 err = %v, 期望 ErrStaleGeneration", err)
	}
}

// TestHeartbeatCheckOrder 验证心跳按未知成员、代数不等、准备中的顺序
// 检查，只报第一个。
func TestHeartbeatCheckOrder(t *testing.T) {
	c, _ := stableGroup(t)

	if err := c.Heartbeat("X", 999); !errors.Is(err, ErrUnknownMember) {
		t.Fatalf("未知成员 err = %v, 期望 ErrUnknownMember", err)
	}
	if err := c.Heartbeat("A", 1); !errors.Is(err, ErrStaleGeneration) {
		t.Fatalf("代数不等 err = %v, 期望 ErrStaleGeneration", err)
	}
	if err := c.Heartbeat("A", 2); err != nil {
		t.Fatalf("稳定状态心跳 err = %v, 期望 nil", err)
	}

	// 进入准备中：代数正确时报需重新加入；代数不等时优先报代数。
	if err := c.Leave("A", 100); err != nil {
		t.Fatal(err)
	}
	if err := c.Heartbeat("B", 2); !errors.Is(err, ErrRejoinNeeded) {
		t.Fatalf("准备中心跳 err = %v, 期望 ErrRejoinNeeded", err)
	}
	if err := c.Heartbeat("B", 1); !errors.Is(err, ErrStaleGeneration) {
		t.Fatalf("准备中代数不等 err = %v, 期望 ErrStaleGeneration", err)
	}
}

// TestClockBackwards 验证时钟倒退被拒绝且不改变状态。
func TestClockBackwards(t *testing.T) {
	c := New(testTimeoutMs)
	if err := c.Join("A", 100); err != nil {
		t.Fatal(err)
	}
	if err := c.Join("B", 200); err != nil {
		t.Fatal(err)
	}
	requireState(t, c, StatePreparing, 1)

	if err := c.Tick(199); !errors.Is(err, ErrClockBackwards) {
		t.Fatalf("Tick 时钟倒退 err = %v, 期望 ErrClockBackwards", err)
	}
	if err := c.Join("C", 150); !errors.Is(err, ErrClockBackwards) {
		t.Fatalf("Join 时钟倒退 err = %v, 期望 ErrClockBackwards", err)
	}
	if err := c.Leave("A", 0); !errors.Is(err, ErrClockBackwards) {
		t.Fatalf("Leave 时钟倒退 err = %v, 期望 ErrClockBackwards", err)
	}
	// 被拒绝的调用不改变状态、代数与加入序。
	requireState(t, c, StatePreparing, 1)
	if got := c.JoinOrder(); !slices.Equal(got, []string{"B"}) {
		t.Fatalf("加入序 = %v, 期望 [B]", got)
	}
	// 等于高水位的时刻仍可接受。
	if err := c.Tick(200); err != nil {
		t.Fatalf("等于高水位的 Tick 应被接受: %v", err)
	}
}

// TestRejectedOpsDoNotChangeState 验证被拒绝的操作不改变状态、代数、
// 加入序与开始时刻。
func TestRejectedOpsDoNotChangeState(t *testing.T) {
	c := New(testTimeoutMs)
	if err := c.Join("A", 0); err != nil {
		t.Fatal(err)
	}
	if err := c.Join("B", 100); err != nil {
		t.Fatal(err)
	}
	requireState(t, c, StatePreparing, 1)
	beforeOrder := c.JoinOrder()
	beforeMembers := c.Members()

	rejections := []error{
		func() error { return c.Tick(50) }(),        // 时钟倒退
		func() error { return c.Leave("X", 200) }(), // 未知成员
	}
	for i, err := range rejections {
		if err == nil {
			t.Fatalf("第 %d 个操作应被拒绝", i)
		}
	}
	requireState(t, c, StatePreparing, 1)
	if got := c.JoinOrder(); !slices.Equal(got, beforeOrder) {
		t.Fatalf("加入序 = %v, 期望 %v", got, beforeOrder)
	}
	if got := c.Members(); !slices.Equal(got, beforeMembers) {
		t.Fatalf("成员 = %v, 期望 %v", got, beforeMembers)
	}
	// 开始时刻不变：在 100+T 时超时完成，证明 startMs 仍为 100。
	if err := c.Tick(100 + testTimeoutMs); err != nil {
		t.Fatal(err)
	}
	requireState(t, c, StateAwaitingSync, 2)
}

// TestLeaveWipesGroupWhenNobodyJoined 验证准备中离开后没有任何成员
// 已加入本轮时，成员全部移除、组进入空状态且代数不变。
func TestLeaveWipesGroupWhenNobodyJoined(t *testing.T) {
	c, _ := stableGroup(t) // 稳定，成员 {A,B}，第 2 代

	// A 离开：稳定 -> 准备中，本轮尚无人加入。
	if err := c.Leave("A", 100); err != nil {
		t.Fatal(err)
	}
	requireState(t, c, StatePreparing, 2)

	// B 再离开：成员清空，进入空状态，代数不变。
	if err := c.Leave("B", 200); err != nil {
		t.Fatal(err)
	}
	requireState(t, c, StateEmpty, 2)
	if got := c.Members(); len(got) != 0 {
		t.Fatalf("成员 = %v, 期望为空", got)
	}
}

// TestLeaveInPreparingWipesWhenJoinOrderEmpty 验证准备中（本轮无人加入）
// 发生离开且仍有剩余已知成员时，成员全部移除、进入空状态、代数不变。
func TestLeaveInPreparingWipesWhenJoinOrderEmpty(t *testing.T) {
	c := New(testTimeoutMs)
	for i, m := range []string{"A", "B", "C", "B", "A"} {
		if err := c.Join(m, int64(i*10)); err != nil {
			t.Fatal(err)
		}
	}
	requireState(t, c, StateAwaitingSync, 2) // 成员 {A,B,C}

	// C 离开：等待分配 -> 准备中，本轮无人加入。
	if err := c.Leave("C", 100); err != nil {
		t.Fatal(err)
	}
	requireState(t, c, StatePreparing, 2)

	// B 离开：本轮仍无人加入，剩余成员全部移除，进入空状态。
	if err := c.Leave("B", 200); err != nil {
		t.Fatal(err)
	}
	requireState(t, c, StateEmpty, 2)
	if got := c.Members(); len(got) != 0 {
		t.Fatalf("成员 = %v, 期望为空", got)
	}
}

// TestTimeoutWipesGroupWhenNobodyJoined 验证准备中超时且没有任何成员
// 已加入本轮时，成员全部移除、组进入空状态且代数不变。
func TestTimeoutWipesGroupWhenNobodyJoined(t *testing.T) {
	c, _ := stableGroup(t) // 稳定，成员 {A,B}，第 2 代

	if err := c.Leave("A", 100); err != nil {
		t.Fatal(err)
	}
	requireState(t, c, StatePreparing, 2) // 本轮无人加入

	if err := c.Tick(100 + testTimeoutMs); err != nil {
		t.Fatal(err)
	}
	requireState(t, c, StateEmpty, 2)
	if got := c.Members(); len(got) != 0 {
		t.Fatalf("成员 = %v, 期望为空", got)
	}
}

// TestLeaveUnknownMember 验证未知成员离开报未知成员。
func TestLeaveUnknownMember(t *testing.T) {
	c := New(testTimeoutMs)
	if err := c.Leave("X", 0); !errors.Is(err, ErrUnknownMember) {
		t.Fatalf("err = %v, 期望 ErrUnknownMember", err)
	}
}
