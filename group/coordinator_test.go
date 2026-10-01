package group

import (
	"errors"
	"reflect"
	"testing"
)

const testTimeoutMs = 1000

// setupStable 构造一个已知成员为 {A, B}、处于稳定状态的组。
// 路径：A 加入（立即完成，gen=1，领导者 A）-> B 加入进入准备中
// -> A 再次加入使全员到齐（gen=2，领导者 B）-> B 提交分配表进入稳定。
func setupStable(t *testing.T) *Coordinator {
	t.Helper()
	c := New(testTimeoutMs)
	mustJoin(t, c, "A", 0)
	mustJoin(t, c, "B", 100)
	mustJoin(t, c, "A", 200)
	if _, err := c.Sync("B", 2, Assignment{"A": {"p0"}, "B": {"p1"}}); err != nil {
		t.Fatalf("leader submit: %v", err)
	}
	assertState(t, c, StateStable, 2, "B", []string{"A", "B"})
	return c
}

func mustJoin(t *testing.T, c *Coordinator, member string, now int64) {
	t.Helper()
	if err := c.Join(member, now); err != nil {
		t.Fatalf("Join(%q, %d): %v", member, now, err)
	}
}

func assertState(t *testing.T, c *Coordinator, state State, gen int, leader string, members []string) {
	t.Helper()
	if got := c.State(); got != state {
		t.Fatalf("state = %v, want %v", got, state)
	}
	if got := c.Generation(); got != gen {
		t.Fatalf("generation = %d, want %d", got, gen)
	}
	if got := c.Leader(); got != leader {
		t.Fatalf("leader = %q, want %q", got, leader)
	}
	got := c.Members()
	if len(got) == 0 && len(members) == 0 {
		return
	}
	if !reflect.DeepEqual(got, members) {
		t.Fatalf("members = %v, want %v", got, members)
	}
}

func TestJoinFromEmptyCompletesImmediately(t *testing.T) {
	c := New(testTimeoutMs)
	mustJoin(t, c, "A", 0)
	// 唯一成员加入后全员到齐，同一次调用内立即完成。
	assertState(t, c, StateAwaitingAssignment, 1, "A", []string{"A"})
}

func TestTimeoutExactlyTCompletes(t *testing.T) {
	c := setupStable(t)
	// 新成员 C 加入，组回到准备中，开始时刻为 300，代数不变。
	mustJoin(t, c, "C", 300)
	assertState(t, c, StatePreparing, 2, "B", []string{"A", "B", "C"})

	// 开始时刻 + T - 1 仍未超时。
	if err := c.Tick(300 + testTimeoutMs - 1); err != nil {
		t.Fatalf("Tick before deadline: %v", err)
	}
	assertState(t, c, StatePreparing, 2, "B", []string{"A", "B", "C"})

	// now 恰等于开始时刻 + T 时超时完成：未加入的 A、B 被移除，
	// 代数加一，领导者为本轮唯一加入者 C。
	if err := c.Tick(300 + testTimeoutMs); err != nil {
		t.Fatalf("Tick at deadline: %v", err)
	}
	assertState(t, c, StateAwaitingAssignment, 3, "C", []string{"C"})
}

func TestLastMemberJoinCompletes(t *testing.T) {
	c := setupStable(t)
	// B 再次加入使组进入准备中；A 是最后一个未加入者。
	mustJoin(t, c, "B", 300)
	assertState(t, c, StatePreparing, 2, "B", []string{"A", "B"})
	// A 加入后全员到齐，在触发它的这一次调用内立即完成。
	mustJoin(t, c, "A", 400)
	assertState(t, c, StateAwaitingAssignment, 3, "B", []string{"A", "B"})
}

func TestLeaveMakesRemainingComplete(t *testing.T) {
	c := setupStable(t)
	// A 加入进入准备中；B 尚未加入。
	mustJoin(t, c, "A", 300)
	assertState(t, c, StatePreparing, 2, "B", []string{"A", "B"})
	// B 离开后剩余已知成员 {A} 都已加入，立即完成。
	if err := c.Leave("B", 400); err != nil {
		t.Fatalf("Leave: %v", err)
	}
	assertState(t, c, StateAwaitingAssignment, 3, "A", []string{"A"})
}

func TestDuplicateJoinKeepsOrder(t *testing.T) {
	c := setupStable(t)
	mustJoin(t, c, "C", 300) // 进入准备中，已知 {A, B, C}
	mustJoin(t, c, "A", 310)
	mustJoin(t, c, "A", 320) // 重复加入，幂等，不改变次序
	mustJoin(t, c, "A", 330)
	if got, want := c.JoinOrder(), []string{"C", "A"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("joinOrder = %v, want %v", got, want)
	}
	assertState(t, c, StatePreparing, 2, "B", []string{"A", "B", "C"})
	// B 加入后全员到齐完成，领导者为本轮加入序最早者 C。
	mustJoin(t, c, "B", 340)
	assertState(t, c, StateAwaitingAssignment, 3, "C", []string{"A", "B", "C"})
}

func TestLeaderIsEarliestJoinerNotPreviousLeader(t *testing.T) {
	c := setupStable(t) // 上一代领导者为 B
	// 新一轮中 A 先于 B 加入，领导者应为 A 而非上一代领导者 B。
	mustJoin(t, c, "A", 300)
	mustJoin(t, c, "B", 400)
	assertState(t, c, StateAwaitingAssignment, 3, "A", []string{"A", "B"})
}

func TestNewJoinInAwaitingReturnsToPreparing(t *testing.T) {
	c := New(testTimeoutMs)
	mustJoin(t, c, "A", 0)
	assertState(t, c, StateAwaitingAssignment, 1, "A", []string{"A"})

	// 等待分配状态下新成员加入：回到准备中，代数未变。
	mustJoin(t, c, "B", 100)
	assertState(t, c, StatePreparing, 1, "A", []string{"A", "B"})

	// 直到再次完成，代数才递增。
	mustJoin(t, c, "A", 200)
	assertState(t, c, StateAwaitingAssignment, 2, "B", []string{"A", "B"})
}

func TestTimeoutPurgeWhenNoJoins(t *testing.T) {
	c := setupStable(t)
	// A 离开，组进入准备中且本轮无人加入。
	if err := c.Leave("A", 300); err != nil {
		t.Fatalf("Leave: %v", err)
	}
	assertState(t, c, StatePreparing, 2, "B", []string{"B"})
	// 超时后没有任何成员已加入本轮：成员全部移除、进入空状态、代数不变。
	if err := c.Tick(300 + testTimeoutMs); err != nil {
		t.Fatalf("Tick: %v", err)
	}
	assertState(t, c, StateEmpty, 2, "", nil)
}

func TestLeavePurgeWhenNoJoinsRemain(t *testing.T) {
	c := setupStable(t)
	mustJoin(t, c, "A", 300) // 准备中，本轮仅 A 加入
	// 唯一已加入者离开：本轮无任何加入，成员全部移除、进入空状态、代数不变。
	if err := c.Leave("A", 400); err != nil {
		t.Fatalf("Leave: %v", err)
	}
	assertState(t, c, StateEmpty, 2, "", nil)
}

func TestLeaveLastMemberEmptiesGroup(t *testing.T) {
	c := New(testTimeoutMs)
	mustJoin(t, c, "A", 0)
	if err := c.Leave("A", 100); err != nil {
		t.Fatalf("Leave: %v", err)
	}
	assertState(t, c, StateEmpty, 1, "", nil)
}

func TestLeaveUnknownMemberIsNoop(t *testing.T) {
	c := setupStable(t)
	if err := c.Leave("ghost", 300); err != nil {
		t.Fatalf("Leave unknown: %v", err)
	}
	assertState(t, c, StateStable, 2, "B", []string{"A", "B"})
}

func TestStableSyncReturnsOwnAssignment(t *testing.T) {
	c := setupStable(t)
	got, err := c.Sync("A", 2, nil)
	if err != nil {
		t.Fatalf("Sync: %v", err)
	}
	if want := []string{"p0"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("assignment = %v, want %v", got, want)
	}
	// 稳定状态下携带非空分配表的同步同样只返回自己的分配。
	got, err = c.Sync("A", 2, Assignment{"A": {"x"}, "B": {"y"}})
	if err != nil {
		t.Fatalf("Sync with table in stable: %v", err)
	}
	if want := []string{"p0"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("assignment = %v, want %v", got, want)
	}
	assertState(t, c, StateStable, 2, "B", []string{"A", "B"})
}

func TestHeartbeatChecksInOrder(t *testing.T) {
	c := setupStable(t)
	mustJoin(t, c, "C", 300) // 进入准备中，gen 仍为 2

	// 未知成员优先于代数不等与准备中。
	if err := c.Heartbeat("ghost", 999); !errors.Is(err, ErrUnknownMember) {
		t.Fatalf("got %v, want ErrUnknownMember", err)
	}
	// 代数不等优先于准备中。
	if err := c.Heartbeat("A", 999); !errors.Is(err, ErrStaleGeneration) {
		t.Fatalf("got %v, want ErrStaleGeneration", err)
	}
	// 准备中。
	if err := c.Heartbeat("A", 2); !errors.Is(err, ErrRejoinNeeded) {
		t.Fatalf("got %v, want ErrRejoinNeeded", err)
	}
	// 被拒绝的心跳不改变状态。
	assertState(t, c, StatePreparing, 2, "B", []string{"A", "B", "C"})
}

func TestHeartbeatOK(t *testing.T) {
	c := setupStable(t)
	if err := c.Heartbeat("A", 2); err != nil {
		t.Fatalf("Heartbeat: %v", err)
	}
}

func TestSyncRejections(t *testing.T) {
	c := setupStable(t)
	mustJoin(t, c, "C", 300) // 准备中，gen=2，已知 {A, B, C}

	cases := []struct {
		name       string
		member     string
		gen        int
		assignment Assignment
		want       error
	}{
		{"unknown member", "ghost", 2, nil, ErrUnknownMember},
		{"stale generation", "A", 1, nil, ErrStaleGeneration},
		{"preparing needs rejoin", "A", 2, nil, ErrRejoinNeeded},
	}
	for _, tc := range cases {
		if _, err := c.Sync(tc.member, tc.gen, tc.assignment); !errors.Is(err, tc.want) {
			t.Fatalf("%s: got %v, want %v", tc.name, err, tc.want)
		}
	}
	// 被拒绝的同步不改变状态、代数、加入序。
	assertState(t, c, StatePreparing, 2, "B", []string{"A", "B", "C"})
	if got, want := c.JoinOrder(), []string{"C"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("joinOrder = %v, want %v", got, want)
	}
}

func TestSyncAwaitingAssignment(t *testing.T) {
	c := New(testTimeoutMs)
	mustJoin(t, c, "A", 0)
	mustJoin(t, c, "B", 100)
	mustJoin(t, c, "A", 200) // gen=2，领导者 B，等待分配
	assertState(t, c, StateAwaitingAssignment, 2, "B", []string{"A", "B"})

	// 非领导者仅查询：尚未就绪。
	if _, err := c.Sync("A", 2, nil); !errors.Is(err, ErrNotReady) {
		t.Fatalf("non-leader query: got %v, want ErrNotReady", err)
	}
	// 领导者仅查询：同样尚未就绪。
	if _, err := c.Sync("B", 2, nil); !errors.Is(err, ErrNotReady) {
		t.Fatalf("leader query: got %v, want ErrNotReady", err)
	}
	// 非领导者提交非空分配表：非领导者提交。
	bad := Assignment{"A": {"p0"}, "B": {"p1"}}
	if _, err := c.Sync("A", 2, bad); !errors.Is(err, ErrNotLeader) {
		t.Fatalf("non-leader submit: got %v, want ErrNotLeader", err)
	}
	// 领导者提交成员集不符的分配表。
	if _, err := c.Sync("B", 2, Assignment{"A": {"p0"}}); !errors.Is(err, ErrAssignmentMismatch) {
		t.Fatalf("missing member: got %v, want ErrAssignmentMismatch", err)
	}
	if _, err := c.Sync("B", 2, Assignment{"A": nil, "B": nil, "C": nil}); !errors.Is(err, ErrAssignmentMismatch) {
		t.Fatalf("extra member: got %v, want ErrAssignmentMismatch", err)
	}
	// 全部被拒绝后状态不变。
	assertState(t, c, StateAwaitingAssignment, 2, "B", []string{"A", "B"})

	// 领导者提交成员集恰等的分配表：进入稳定。
	if _, err := c.Sync("B", 2, bad); err != nil {
		t.Fatalf("leader submit: %v", err)
	}
	assertState(t, c, StateStable, 2, "B", []string{"A", "B"})
}

func TestClockBackwards(t *testing.T) {
	c := setupStable(t)
	mustJoin(t, c, "C", 300) // 准备中，开始时刻 300

	for _, op := range []func() error{
		func() error { return c.Join("D", 299) },
		func() error { return c.Leave("A", 299) },
		func() error { return c.Tick(299) },
	} {
		if err := op(); !errors.Is(err, ErrClockBackwards) {
			t.Fatalf("got %v, want ErrClockBackwards", err)
		}
	}
	// 被拒绝的操作不改变状态、代数、加入序与开始时刻：
	// 开始时刻仍为 300，恰在 300+T 超时完成可验证。
	assertState(t, c, StatePreparing, 2, "B", []string{"A", "B", "C"})
	if got, want := c.JoinOrder(), []string{"C"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("joinOrder = %v, want %v", got, want)
	}
	if err := c.Tick(300 + testTimeoutMs); err != nil {
		t.Fatalf("Tick at deadline: %v", err)
	}
	assertState(t, c, StateAwaitingAssignment, 3, "C", []string{"C"})
}

func TestEqualClockIsAllowed(t *testing.T) {
	c := New(testTimeoutMs)
	mustJoin(t, c, "A", 100)
	// now 等于此前值不算倒退。
	mustJoin(t, c, "B", 100)
	assertState(t, c, StatePreparing, 1, "A", []string{"A", "B"})
}
