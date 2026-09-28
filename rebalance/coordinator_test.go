package rebalance

import (
	"errors"
	"fmt"
	"math/rand"
	"reflect"
	"sync"
	"testing"
)

func newTestCoordinator(t *testing.T, n int) *Coordinator {
	t.Helper()
	c, err := NewCoordinator(n, func(format string, args ...any) {
		t.Logf("[coordinator] "+format, args...)
	})
	if err != nil {
		t.Fatalf("NewCoordinator(%d): %v", n, err)
	}
	return c
}

func logState(t *testing.T, c *Coordinator, why string) {
	t.Helper()
	parts, members := c.Snapshot()
	t.Logf("state after %s: members=%v", why, members)
	for _, p := range parts {
		t.Logf("  partition %d: state=%s owner=%d", p.ID, p.State, p.Owner)
	}
}

func mustJoin(t *testing.T, c *Coordinator, ids ...int) {
	t.Helper()
	for _, id := range ids {
		t.Logf("input: Join(%d)", id)
		if err := c.Join(id); err != nil {
			t.Fatalf("Join(%d): %v", id, err)
		}
	}
}

// ownerOf 返回分区持有者，要求其为消费中。
func ownerOf(t *testing.T, c *Coordinator, id int) int {
	t.Helper()
	p, err := c.Partition(id)
	if err != nil {
		t.Fatalf("Partition(%d): %v", id, err)
	}
	if p.State != Consuming {
		t.Fatalf("partition %d: want consuming, got %s", id, p.State)
	}
	return p.Owner
}

// naiveTarget 是朴素目标：不小于 p 的最小成员，否则回绕到最小成员。
func naiveTarget(members []int, p int) int {
	best := -1
	for _, m := range members {
		if m >= p && (best == -1 || m < best) {
			best = m
		}
	}
	if best != -1 {
		return best
	}
	min := members[0]
	for _, m := range members {
		if m < min {
			min = m
		}
	}
	return min
}

// assertQuiescentMatchesNaive 校验静止时（无撤销中）与朴素目标完全一致。
func assertQuiescentMatchesNaive(t *testing.T, c *Coordinator) {
	t.Helper()
	parts, members := c.Snapshot()
	revoking := 0
	for _, p := range parts {
		if p.State == Revoking {
			revoking++
		}
	}
	if revoking > 0 {
		return
	}
	for _, p := range parts {
		if len(members) == 0 {
			if p.State != Unassigned {
				t.Fatalf("quiescent empty group: partition %d want unassigned, got %s owner=%d", p.ID, p.State, p.Owner)
			}
			continue
		}
		want := naiveTarget(members, p.ID)
		if p.State != Consuming || p.Owner != want {
			t.Fatalf("quiescent: partition %d want consuming owner=%d, got %s owner=%d",
				p.ID, want, p.State, p.Owner)
		}
	}
	t.Logf("quiescent check passed: members=%v, all partitions match naive target", members)
}

// TestTwoRoundProtocol 验证两轮协议：先撤销、确认后再分配，且未迁移分区不中断。
func TestTwoRoundProtocol(t *testing.T) {
	c := newTestCoordinator(t, 4)
	mustJoin(t, c, 2)
	logState(t, c, "join 2")
	for i := 0; i < 4; i++ {
		if got := ownerOf(t, c, i); got != 2 {
			t.Fatalf("partition %d: want owner 2, got %d", i, got)
		}
	}

	// 成员 3 加入：分区 3 的目标变为 3，进入撤销中；其余分区目标不变、保持消费中。
	mustJoin(t, c, 3)
	logState(t, c, "join 3")
	p3, _ := c.Partition(3)
	if p3.State != Revoking || p3.Owner != 2 {
		t.Fatalf("partition 3: want revoking owner=2, got %s owner=%d", p3.State, p3.Owner)
	}
	for i := 0; i < 3; i++ {
		p, _ := c.Partition(i)
		if p.State != Consuming || p.Owner != 2 {
			t.Fatalf("partition %d: must stay consuming owner=2 (no interruption), got %s owner=%d",
				i, p.State, p.Owner)
		}
	}

	// 第二轮尚未完成：成员 3 确认前，分区 3 不会分配。
	t.Log("input: ConfirmRevocation(2)")
	if err := c.ConfirmRevocation(2); err != nil {
		t.Fatalf("ConfirmRevocation(2): %v", err)
	}
	logState(t, c, "confirm 2")
	if got := ownerOf(t, c, 3); got != 3 {
		t.Fatalf("partition 3: want owner 3 after confirm, got %d", got)
	}
	assertQuiescentMatchesNaive(t, c)
}

// TestRevokingNotCancelledByTargetChange 已撤销的分区不因目标再次改变而取消。
func TestRevokingNotCancelledByTargetChange(t *testing.T) {
	c := newTestCoordinator(t, 2)
	mustJoin(t, c, 5)
	mustJoin(t, c, 1)
	// 分区 1 目标变为 1，进入撤销中（owner 5）。
	p1, _ := c.Partition(1)
	if p1.State != Revoking || p1.Owner != 5 {
		t.Fatalf("partition 1: want revoking owner=5, got %s owner=%d", p1.State, p1.Owner)
	}
	// 成员 1 离开，分区 1 目标回到 5，但撤销中状态不得取消。
	t.Log("input: Leave(1)")
	if err := c.Leave(1); err != nil {
		t.Fatalf("Leave(1): %v", err)
	}
	logState(t, c, "leave 1")
	p1, _ = c.Partition(1)
	if p1.State != Revoking || p1.Owner != 5 {
		t.Fatalf("partition 1: revoking must not be cancelled, got %s owner=%d", p1.State, p1.Owner)
	}
	// 确认后目标仍是 5，重新分配回 5。
	t.Log("input: ConfirmRevocation(5)")
	if err := c.ConfirmRevocation(5); err != nil {
		t.Fatalf("ConfirmRevocation(5): %v", err)
	}
	assertQuiescentMatchesNaive(t, c)
}

// TestWrapAround 验证环形回绕：无不小于分区号的成员时取最小成员。
func TestWrapAround(t *testing.T) {
	c := newTestCoordinator(t, 10)
	mustJoin(t, c, 3, 7)
	logState(t, c, "join 3,7")
	// 第二轮：确认撤销后，无主分区才分配给目标。
	t.Log("input: ConfirmRevocation(3)")
	if err := c.ConfirmRevocation(3); err != nil {
		t.Fatalf("ConfirmRevocation(3): %v", err)
	}
	logState(t, c, "confirm 3")
	want := map[int]int{
		0: 3, 1: 3, 2: 3, 3: 3, // 目标为不小于 p 的最小成员 3
		4: 7, 5: 7, 6: 7, 7: 7, // 目标为 7
		8: 3, 9: 3, // 无成员 >= p，环形回绕到最小成员 3
	}
	for p, owner := range want {
		if got := ownerOf(t, c, p); got != owner {
			t.Fatalf("partition %d: want owner %d, got %d", p, owner, got)
		}
	}
	assertQuiescentMatchesNaive(t, c)
}

// TestLeaveAndReassign 成员离开后其分区立即无主，并在无撤销中时分配给当前目标。
func TestLeaveAndReassign(t *testing.T) {
	c := newTestCoordinator(t, 4)
	mustJoin(t, c, 2, 4)
	logState(t, c, "join 2,4")
	t.Log("input: Leave(4)")
	if err := c.Leave(4); err != nil {
		t.Fatalf("Leave(4): %v", err)
	}
	logState(t, c, "leave 4")
	// 分区 3 在成员 4 加入时已进入撤销中，离开不会取消撤销，需确认后才重分配。
	p3, _ := c.Partition(3)
	if p3.State != Revoking || p3.Owner != 2 {
		t.Fatalf("partition 3: revoking must survive leave, got %s owner=%d", p3.State, p3.Owner)
	}
	t.Log("input: ConfirmRevocation(2)")
	if err := c.ConfirmRevocation(2); err != nil {
		t.Fatalf("ConfirmRevocation(2): %v", err)
	}
	logState(t, c, "confirm 2")
	for i := 0; i < 4; i++ {
		if got := ownerOf(t, c, i); got != 2 {
			t.Fatalf("partition %d: want owner 2 after leave, got %d", i, got)
		}
	}
	assertQuiescentMatchesNaive(t, c)
}

// TestEmptyGroupNoAssignment 成员为空时不分配，分区保持无主。
func TestEmptyGroupNoAssignment(t *testing.T) {
	c := newTestCoordinator(t, 3)
	mustJoin(t, c, 1)
	t.Log("input: Leave(1)")
	if err := c.Leave(1); err != nil {
		t.Fatalf("Leave(1): %v", err)
	}
	logState(t, c, "leave 1 (group empty)")
	parts, members := c.Snapshot()
	if len(members) != 0 {
		t.Fatalf("want empty members, got %v", members)
	}
	for _, p := range parts {
		if p.State != Unassigned || p.Owner != -1 {
			t.Fatalf("partition %d: want unassigned, got %s owner=%d", p.ID, p.State, p.Owner)
		}
	}
	assertQuiescentMatchesNaive(t, c)
}

// snapshotOf 捕获用于拒绝前后对比的状态。
func snapshotOf(c *Coordinator) ([]Partition, []int) {
	parts, members := c.Snapshot()
	return parts, members
}

// TestInvalidInputsRejected 非法输入整体拒绝，且拒绝后状态不变（失败不留痕）。
func TestInvalidInputsRejected(t *testing.T) {
	c := newTestCoordinator(t, 4)
	mustJoin(t, c, 2, 4)

	// 先排空待确认撤销，使后续“无待确认撤销”用例成立。
	if err := c.ConfirmRevocation(2); err != nil {
		t.Fatalf("drain ConfirmRevocation(2): %v", err)
	}

	type step struct {
		name string
		op   func() error
		want error
	}
	steps := []step{
		{"join non-positive id", func() error { return c.Join(0) }, ErrInvalidArgument},
		{"join negative id", func() error { return c.Join(-3) }, ErrInvalidArgument},
		{"duplicate join", func() error { return c.Join(2) }, ErrMemberExists},
		{"leave unknown member", func() error { return c.Leave(9) }, ErrMemberNotFound},
		{"leave invalid id", func() error { return c.Leave(-1) }, ErrInvalidArgument},
		{"confirm unknown member", func() error { return c.ConfirmRevocation(9) }, ErrMemberNotFound},
		{"confirm without pending revocation", func() error { return c.ConfirmRevocation(2) }, ErrNoPendingRevocation},
	}
	for _, s := range steps {
		beforeP, beforeM := snapshotOf(c)
		t.Logf("input: %s", s.name)
		err := s.op()
		if !errors.Is(err, s.want) {
			t.Fatalf("%s: want error %v, got %v", s.name, s.want, err)
		}
		afterP, afterM := snapshotOf(c)
		if !reflect.DeepEqual(beforeP, afterP) || !reflect.DeepEqual(beforeM, afterM) {
			t.Fatalf("%s: rejected op mutated state\nbefore: %v %v\nafter:  %v %v",
				s.name, beforeP, beforeM, afterP, afterM)
		}
		t.Logf("%s: rejected with %v, state unchanged", s.name, err)
	}

	// 错误类别互不相同、可区分。
	errs := []error{ErrInvalidArgument, ErrMemberExists, ErrMemberNotFound, ErrNoPendingRevocation}
	for i := range errs {
		for j := range errs {
			if i != j && errors.Is(errs[i], errs[j]) {
				t.Fatalf("error categories must be distinguishable: %v vs %v", errs[i], errs[j])
			}
		}
	}

	// 构造器与查询的非法参数。
	if _, err := NewCoordinator(0, nil); !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("NewCoordinator(0): want ErrInvalidArgument, got %v", err)
	}
	if _, err := NewCoordinator(-1, nil); !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("NewCoordinator(-1): want ErrInvalidArgument, got %v", err)
	}
	if _, err := c.Partition(4); !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("Partition(4): want ErrInvalidArgument, got %v", err)
	}
	if _, err := c.Partition(-1); !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("Partition(-1): want ErrInvalidArgument, got %v", err)
	}
}

// TestConcurrentDeterministic 并发执行成员变化与确认，校验：
// 任意时刻每个分区至多一个持有者，且最终静止时与朴素目标一致。
func TestConcurrentDeterministic(t *testing.T) {
	const (
		partitions = 16
		workers    = 8
		rounds     = 200
	)
	c := newTestCoordinator(t, partitions)
	rng := rand.New(rand.NewSource(42))

	var wg sync.WaitGroup
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func(seed int64) {
			defer wg.Done()
			r := rand.New(rand.NewSource(seed))
			for i := 0; i < rounds; i++ {
				id := 1 + r.Intn(12)
				switch r.Intn(3) {
				case 0:
					_ = c.Join(id)
				case 1:
					_ = c.Leave(id)
				case 2:
					_ = c.ConfirmRevocation(id)
				}
				// 不变量：每个分区至多一个持有者（无主时 owner 必须为 -1）。
				parts, _ := c.Snapshot()
				for _, p := range parts {
					if p.State == Unassigned && p.Owner != -1 {
						t.Errorf("partition %d unassigned but owner=%d", p.ID, p.Owner)
					}
					if p.State != Unassigned && p.Owner <= 0 {
						t.Errorf("partition %d %s but owner=%d", p.ID, p.State, p.Owner)
					}
				}
			}
		}(int64(w*1000) + rng.Int63n(1000))
	}
	wg.Wait()

	// 驱动到静止：确认所有待撤销，直到无撤销中分区。
	for {
		parts, members := c.Snapshot()
		progress := false
		for _, m := range members {
			for _, p := range parts {
				if p.State == Revoking && p.Owner == m {
					if err := c.ConfirmRevocation(m); err != nil {
						t.Fatalf("ConfirmRevocation(%d): %v", m, err)
					}
					progress = true
					break
				}
			}
		}
		if !progress {
			break
		}
	}
	logState(t, c, "concurrent drain")
	assertQuiescentMatchesNaive(t, c)
}

// TestDeterministicReplay 相同操作序列产生完全相同的结果（确定可复现）。
func TestDeterministicReplay(t *testing.T) {
	run := func() string {
		c, err := NewCoordinator(8, nil)
		if err != nil {
			t.Fatalf("NewCoordinator: %v", err)
		}
		r := rand.New(rand.NewSource(7))
		for i := 0; i < 100; i++ {
			id := 1 + r.Intn(6)
			switch r.Intn(3) {
			case 0:
				_ = c.Join(id)
			case 1:
				_ = c.Leave(id)
			case 2:
				_ = c.ConfirmRevocation(id)
			}
		}
		parts, members := c.Snapshot()
		return fmt.Sprintf("%v|%v", parts, members)
	}
	if a, b := run(), run(); a != b {
		t.Fatalf("same op sequence must be reproducible:\nrun1=%s\nrun2=%s", a, b)
	}
}
