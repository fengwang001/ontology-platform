package rebalance

import (
	"errors"
	"log"
	"os"
	"reflect"
	"sync"
	"testing"
)

func testLogger(t *testing.T) *log.Logger {
	t.Helper()
	return log.New(os.Stdout, "[rebalance-test] ", log.Lmicroseconds)
}

func ownerOf(t *testing.T, a *Assignor, partition int) (State, int) {
	t.Helper()
	for _, v := range a.Snapshot() {
		if v.Partition == partition {
			return v.State, v.Owner
		}
	}
	t.Fatalf("partition %d not found", partition)
	return Unowned, -1
}

// 驱动至静止：反复让有待确认撤销的成员确认，直到无撤销中分区。
func settle(t *testing.T, a *Assignor) {
	t.Helper()
	for {
		progressed := false
		for _, m := range a.Members() {
			if err := a.Confirm(m); err == nil {
				progressed = true
			} else if !errors.Is(err, ErrNoPendingRevocation) {
				t.Fatalf("settle: unexpected confirm error: %v", err)
			}
		}
		if !progressed {
			return
		}
	}
}

func TestTwoRoundProtocol(t *testing.T) {
	a, err := NewAssignor([]int{0, 1, 2, 3, 4, 5}, testLogger(t))
	if err != nil {
		t.Fatal(err)
	}
	// 第一轮：首个成员加入，全部分区直接分配（无撤销中分区）。
	if err := a.Join(1); err != nil {
		t.Fatal(err)
	}
	for p := 0; p <= 5; p++ {
		if s, o := ownerOf(t, a, p); s != Consuming || o != 1 {
			t.Fatalf("partition %d: got %s(%d), want Consuming(1)", p, s, o)
		}
	}
	// 成员 2 加入：仅目标改变的分区 2 进入撤销中，其余保持消费中不中断。
	if err := a.Join(2); err != nil {
		t.Fatal(err)
	}
	if s, o := ownerOf(t, a, 2); s != Revoking || o != 1 {
		t.Fatalf("partition 2: got %s(%d), want Revoking(1)", s, o)
	}
	for _, p := range []int{0, 1, 3, 4, 5} {
		if s, o := ownerOf(t, a, p); s != Consuming || o != 1 {
			t.Fatalf("partition %d interrupted: got %s(%d), want Consuming(1)", p, s, o)
		}
	}
	// 第二轮：成员 1 确认后分区 2 变无主，全组无撤销中，立即分配给目标 2。
	if err := a.Confirm(1); err != nil {
		t.Fatal(err)
	}
	if s, o := ownerOf(t, a, 2); s != Consuming || o != 2 {
		t.Fatalf("partition 2: got %s(%d), want Consuming(2)", s, o)
	}
	for _, p := range []int{0, 1, 3, 4, 5} {
		if s, o := ownerOf(t, a, p); s != Consuming || o != 1 {
			t.Fatalf("partition %d: got %s(%d), want Consuming(1)", p, s, o)
		}
	}
}

func TestWrapAroundTarget(t *testing.T) {
	members := []int{3, 7}
	cases := map[int]int{0: 3, 3: 3, 4: 7, 7: 7, 8: 3, 10: 3}
	for p, want := range cases {
		if got := NaiveTarget(members, p); got != want {
			t.Fatalf("NaiveTarget(%v, %d) = %d, want %d", members, p, got, want)
		}
	}
	if got := NaiveTarget(nil, 5); got != -1 {
		t.Fatalf("NaiveTarget(nil, 5) = %d, want -1", got)
	}
	// 端到端：分区 10 环形回绕到最小成员 3。
	a, err := NewAssignor([]int{5, 10}, testLogger(t))
	if err != nil {
		t.Fatal(err)
	}
	for _, m := range []int{3, 7} {
		if err := a.Join(m); err != nil {
			t.Fatal(err)
		}
	}
	settle(t, a)
	if s, o := ownerOf(t, a, 10); s != Consuming || o != 3 {
		t.Fatalf("partition 10: got %s(%d), want Consuming(3)", s, o)
	}
	if s, o := ownerOf(t, a, 5); s != Consuming || o != 7 {
		t.Fatalf("partition 5: got %s(%d), want Consuming(7)", s, o)
	}
}

func TestRevocationNotCancelledByTargetChange(t *testing.T) {
	a, err := NewAssignor([]int{0, 2}, testLogger(t))
	if err != nil {
		t.Fatal(err)
	}
	if err := a.Join(1); err != nil {
		t.Fatal(err)
	}
	// 成员 2 加入：分区 2 目标变为 2，进入撤销中。
	if err := a.Join(2); err != nil {
		t.Fatal(err)
	}
	if s, _ := ownerOf(t, a, 2); s != Revoking {
		t.Fatalf("partition 2: got %s, want Revoking", s)
	}
	// 成员 2 离开：分区 2 目标变回 1，但已撤销中的分区不取消。
	if err := a.Leave(2); err != nil {
		t.Fatal(err)
	}
	if s, o := ownerOf(t, a, 2); s != Revoking || o != 1 {
		t.Fatalf("partition 2: got %s(%d), want Revoking(1)", s, o)
	}
	// 确认后全组无撤销中，分区 2 重新分给目标 1。
	if err := a.Confirm(1); err != nil {
		t.Fatal(err)
	}
	if s, o := ownerOf(t, a, 2); s != Consuming || o != 1 {
		t.Fatalf("partition 2: got %s(%d), want Consuming(1)", s, o)
	}
}

func TestLeaveMakesOrphanPartitionsUnowned(t *testing.T) {
	a, err := NewAssignor([]int{0, 1, 2}, testLogger(t))
	if err != nil {
		t.Fatal(err)
	}
	if err := a.Join(1); err != nil {
		t.Fatal(err)
	}
	if err := a.Join(2); err != nil {
		t.Fatal(err)
	}
	settle(t, a)
	// 成员 2 离开：其分区立即无主，且因组内无撤销中分区而立即重分配给成员 1。
	if err := a.Leave(2); err != nil {
		t.Fatal(err)
	}
	for p := 0; p <= 2; p++ {
		if s, o := ownerOf(t, a, p); s != Consuming || o != 1 {
			t.Fatalf("partition %d: got %s(%d), want Consuming(1)", p, s, o)
		}
	}
	// 全部离开：成员为空则不分配，分区保持无主。
	if err := a.Leave(1); err != nil {
		t.Fatal(err)
	}
	for p := 0; p <= 2; p++ {
		if s, o := ownerOf(t, a, p); s != Unowned || o != -1 {
			t.Fatalf("partition %d: got %s(%d), want Unowned(-1)", p, s, o)
		}
	}
}

func TestInvalidInputsRejectedWithoutSideEffects(t *testing.T) {
	if _, err := NewAssignor(nil, nil); !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("empty partitions: got %v, want ErrInvalidArgument", err)
	}
	if _, err := NewAssignor([]int{1, -1}, nil); !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("negative partition: got %v, want ErrInvalidArgument", err)
	}
	if _, err := NewAssignor([]int{1, 1}, nil); !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("duplicate partition: got %v, want ErrInvalidArgument", err)
	}

	a, err := NewAssignor([]int{0, 1, 2}, testLogger(t))
	if err != nil {
		t.Fatal(err)
	}
	if err := a.Join(1); err != nil {
		t.Fatal(err)
	}
	if err := a.Join(2); err != nil {
		t.Fatal(err)
	}
	before := a.Snapshot()
	beforeMembers := a.Members()

	rejections := []struct {
		name string
		op   func() error
		want error
	}{
		{"duplicate join", func() error { return a.Join(1) }, ErrDuplicateMember},
		{"negative join", func() error { return a.Join(-1) }, ErrInvalidArgument},
		{"leave missing member", func() error { return a.Leave(99) }, ErrMemberNotFound},
		{"negative leave", func() error { return a.Leave(-2) }, ErrInvalidArgument},
		{"confirm missing member", func() error { return a.Confirm(99) }, ErrMemberNotFound},
		{"confirm nothing pending", func() error { return a.Confirm(2) }, ErrNoPendingRevocation},
		{"negative confirm", func() error { return a.Confirm(-3) }, ErrInvalidArgument},
	}
	for _, tc := range rejections {
		if err := tc.op(); !errors.Is(err, tc.want) {
			t.Fatalf("%s: got %v, want %v", tc.name, err, tc.want)
		}
		if got := a.Snapshot(); !reflect.DeepEqual(got, before) {
			t.Fatalf("%s: partition state changed after rejection: %v", tc.name, got)
		}
		if got := a.Members(); !reflect.DeepEqual(got, beforeMembers) {
			t.Fatalf("%s: members changed after rejection: %v", tc.name, got)
		}
	}
	// 错误类别互不相同、可区分。
	errs := []error{ErrInvalidArgument, ErrDuplicateMember, ErrMemberNotFound, ErrNoPendingRevocation}
	for i, e1 := range errs {
		for j, e2 := range errs {
			if i != j && errors.Is(e1, e2) {
				t.Fatalf("errors %v and %v are not distinguishable", e1, e2)
			}
		}
	}
}

func TestConvergesToNaiveTarget(t *testing.T) {
	partitions := []int{0, 1, 2, 3, 4, 5, 6, 7}
	a, err := NewAssignor(partitions, testLogger(t))
	if err != nil {
		t.Fatal(err)
	}
	for _, m := range []int{2, 5, 9} {
		if err := a.Join(m); err != nil {
			t.Fatal(err)
		}
		settle(t, a)
	}
	if err := a.Leave(5); err != nil {
		t.Fatal(err)
	}
	settle(t, a)
	if err := a.Join(4); err != nil {
		t.Fatal(err)
	}
	settle(t, a)

	members := a.Members()
	for _, v := range a.Snapshot() {
		want := NaiveTarget(members, v.Partition)
		if v.State != Consuming || v.Owner != want {
			t.Fatalf("partition %d: got %s(%d), want Consuming(%d)", v.Partition, v.State, v.Owner, want)
		}
	}
}

func TestDeterministicReplay(t *testing.T) {
	run := func() []PartitionView {
		a, err := NewAssignor([]int{0, 1, 2, 3}, nil)
		if err != nil {
			t.Fatal(err)
		}
		for _, m := range []int{1, 3, 2} {
			if err := a.Join(m); err != nil {
				t.Fatal(err)
			}
			settle(t, a)
		}
		if err := a.Leave(3); err != nil {
			t.Fatal(err)
		}
		settle(t, a)
		return a.Snapshot()
	}
	first, second := run(), run()
	if !reflect.DeepEqual(first, second) {
		t.Fatalf("non-deterministic result:\n%v\n%v", first, second)
	}
}

func TestConcurrentMembershipAndConfirm(t *testing.T) {
	partitions := []int{0, 1, 2, 3, 4, 5, 6, 7, 8, 9}
	a, err := NewAssignor(partitions, testLogger(t))
	if err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	// 多执行体并发加入。
	for m := 0; m < 8; m++ {
		wg.Add(1)
		go func(m int) {
			defer wg.Done()
			if err := a.Join(m); err != nil {
				t.Errorf("join %d: %v", m, err)
			}
		}(m)
	}
	wg.Wait()
	// 多执行体并发确认直至静止。
	for round := 0; round < 16; round++ {
		for _, m := range a.Members() {
			wg.Add(1)
			go func(m int) {
				defer wg.Done()
				if err := a.Confirm(m); err != nil && !errors.Is(err, ErrNoPendingRevocation) {
					t.Errorf("confirm %d: %v", m, err)
				}
			}(m)
		}
		wg.Wait()
	}
	// 任意时刻每个分区至多一个持有者：终态校验每个分区恰好一个持有者且与朴素目标一致。
	members := a.Members()
	for _, v := range a.Snapshot() {
		want := NaiveTarget(members, v.Partition)
		if v.State != Consuming || v.Owner != want {
			t.Fatalf("partition %d: got %s(%d), want Consuming(%d)", v.Partition, v.State, v.Owner, want)
		}
	}
}
