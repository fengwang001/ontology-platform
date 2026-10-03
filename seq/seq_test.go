package seq

import (
	"reflect"
	"sync"
	"testing"
)

func mustNew(t *testing.T, K int, m int64) *Allocator {
	t.Helper()
	a, err := New(K, m)
	if err != nil {
		t.Fatalf("New(%d, %d): %v", K, m, err)
	}
	return a
}

func mustIssue(t *testing.T, a *Allocator, s int, want int64) {
	t.Helper()
	id, err := a.Issue(s)
	if err != nil {
		t.Fatalf("Issue(%d): %v", s, err)
	}
	if id != want {
		t.Fatalf("Issue(%d) = %d, want %d", s, id, want)
	}
}

func mustJoin(t *testing.T, a *Allocator, s int) {
	t.Helper()
	if err := a.Join(2, s); err != nil {
		t.Fatalf("Join(2, %d): %v", s, err)
	}
}

func mustLeave(t *testing.T, a *Allocator, s int) {
	t.Helper()
	if err := a.Leave(2, s); err != nil {
		t.Fatalf("Leave(2, %d): %v", s, err)
	}
}

func mustObserve(t *testing.T, a *Allocator, s int, id int64) {
	t.Helper()
	if err := a.Observe(s, id); err != nil {
		t.Fatalf("Observe(%d, %d): %v", s, id, err)
	}
}

func wantNext(t *testing.T, a *Allocator, s int, want int64) {
	t.Helper()
	if got := a.Next(s); got != want {
		t.Fatalf("Next(%d) = %d, want %d", s, got, want)
	}
}

func wantJ(t *testing.T, a *Allocator, want int64) {
	t.Helper()
	if got := a.J(); got != want {
		t.Fatalf("J = %d, want %d", got, want)
	}
}

// 规格例一：K=3、m=4 的完整走查，含活跃数 1→2→3→2→1 的往返。
func TestSpecExample1(t *testing.T) {
	a := mustNew(t, 3, 4)
	if got := a.Stride(); got != 1 {
		t.Fatalf("初始 Stride = %d, want 1", got)
	}
	mustIssue(t, a, 1, 1)
	mustIssue(t, a, 1, 2)
	mustIssue(t, a, 1, 3)
	wantNext(t, a, 1, 4)

	mustJoin(t, a, 2)
	if got := a.Stride(); got != 4 {
		t.Fatalf("Join(2) 后 Stride = %d, want 4", got)
	}
	wantNext(t, a, 1, 4)
	wantNext(t, a, 2, 5)
	wantJ(t, a, 4)

	mustIssue(t, a, 1, 4)
	mustIssue(t, a, 2, 5)
	mustIssue(t, a, 1, 8)
	mustIssue(t, a, 2, 9)
	wantNext(t, a, 1, 12)
	wantNext(t, a, 2, 13)

	mustJoin(t, a, 3)
	wantNext(t, a, 3, 14)
	wantJ(t, a, 17)
	mustIssue(t, a, 3, 14)
	wantNext(t, a, 3, 18)

	mustLeave(t, a, 3)
	if got := a.Stride(); got != 4 {
		t.Fatalf("Leave(3) 后 Stride = %d, want 4", got)
	}
	wantJ(t, a, 17)

	mustLeave(t, a, 1)
	if got := a.Stride(); got != 1 {
		t.Fatalf("Leave(1) 后 Stride = %d, want 1", got)
	}
	wantNext(t, a, 2, 18)
	wantJ(t, a, 22)
	mustIssue(t, a, 2, 18)
	mustIssue(t, a, 2, 19)
}

// 规格例一延伸：退出再重新加入，重入起点取全局高水位而非自身旧 next。
func TestLeaveAndRejoin(t *testing.T) {
	a := mustNew(t, 3, 4)
	mustIssue(t, a, 1, 1)
	mustIssue(t, a, 1, 2)
	mustIssue(t, a, 1, 3)
	mustJoin(t, a, 2)
	mustIssue(t, a, 1, 4)
	mustIssue(t, a, 2, 5)
	mustIssue(t, a, 1, 8)
	mustIssue(t, a, 2, 9)
	mustJoin(t, a, 3)
	mustIssue(t, a, 3, 14)
	mustLeave(t, a, 3)
	mustLeave(t, a, 1)
	mustIssue(t, a, 2, 18)
	mustIssue(t, a, 2, 19)
	wantNext(t, a, 2, 20)
	wantJ(t, a, 22)

	// 系统 1 重新加入：H=20（来自系统 2），t=2 对齐到 21，next_1 对齐到 20。
	mustJoin(t, a, 1)
	wantNext(t, a, 1, 20)
	wantNext(t, a, 2, 21)
	wantJ(t, a, 31)
	if got := a.Stride(); got != 4 {
		t.Fatalf("重入后 Stride = %d, want 4", got)
	}
	mustIssue(t, a, 1, 20)
	mustIssue(t, a, 2, 21)

	// 系统 2 再退出：只剩系统 1，H'=max(24,25,18)=25 含刚离开者。
	mustLeave(t, a, 2)
	wantNext(t, a, 1, 25)
	wantJ(t, a, 32)
	if got := a.Stride(); got != 1 {
		t.Fatalf("回到单系统后 Stride = %d, want 1", got)
	}
	mustIssue(t, a, 1, 25)
	mustIssue(t, a, 1, 26)
}

// Join 计算 next_s 用的是对齐 t 之前取定的 H。
// K=2、m=4：next_1=5 时 Join(2)，t 对齐到 8；若误用对齐后的 H=8，
// next_2 会是 9 而非 5。
func TestJoinUsesPreAlignHighWater(t *testing.T) {
	a := mustNew(t, 2, 4)
	mustIssue(t, a, 1, 1)
	mustIssue(t, a, 1, 2)
	mustIssue(t, a, 1, 3)
	mustIssue(t, a, 1, 4)
	wantNext(t, a, 1, 5)

	mustJoin(t, a, 2)
	wantNext(t, a, 1, 8) // alignUp(5, 0) = 8，增量 3
	wantNext(t, a, 2, 5) // alignUp(max(1, H=5), 1) = 5，增量 4
	wantJ(t, a, 7)
	mustIssue(t, a, 2, 5)
}

// Join 的 H 来自不活跃系统：系统 2 退出后 next_2=101 仍是全局最大，
// 系统 4 加入时必须从 101 起对齐，而不是只看活跃系统。
func TestJoinHighWaterFromInactive(t *testing.T) {
	a := mustNew(t, 4, 4)
	mustJoin(t, a, 2)
	mustJoin(t, a, 3)
	mustObserve(t, a, 2, 100) // next_2 = alignUp(101, 1) = 101
	wantNext(t, a, 2, 101)
	mustLeave(t, a, 2) // 剩 2 个活跃系统，不发生接续
	if a.Active(2) {
		t.Fatal("系统 2 应已退出")
	}

	mustJoin(t, a, 4)
	wantNext(t, a, 4, 103) // alignUp(max(1, H=101), 3) = 103
	mustIssue(t, a, 4, 103)
}

// 规格例四：Reserve 全成或全不成，最后一个编号恰等于 MaxID 时成功。
func TestReserveMaxIDBoundary(t *testing.T) {
	a := mustNew(t, 2, 2)
	a.setNext(1, MaxID-1)

	ids, err := a.Reserve(1, 2)
	if err != nil {
		t.Fatalf("Reserve(1, 2): %v", err)
	}
	if want := []int64{MaxID - 1, MaxID}; !reflect.DeepEqual(ids, want) {
		t.Fatalf("Reserve(1, 2) = %v, want %v", ids, want)
	}
	wantNext(t, a, 1, MaxID+1)

	if _, err := a.Issue(1); err != ErrExhausted {
		t.Fatalf("Issue(1) err = %v, want ErrExhausted", err)
	}
	if _, err := a.Reserve(1, 3); err != ErrExhausted {
		t.Fatalf("Reserve(1, 3) err = %v, want ErrExhausted", err)
	}
	wantNext(t, a, 1, MaxID+1) // 被拒后 next 不变

	// 全不成：整体拒绝且 next 不变。
	b := mustNew(t, 2, 2)
	b.setNext(1, MaxID-1)
	if _, err := b.Reserve(1, 3); err != ErrExhausted {
		t.Fatalf("Reserve(1, 3) err = %v, want ErrExhausted", err)
	}
	wantNext(t, b, 1, MaxID-1)
}

// 规格例三：Observe 在 id 小于、恰等、跨类时的取值。
func TestObserve(t *testing.T) {
	a := mustNew(t, 3, 4)
	mustIssue(t, a, 1, 1)
	mustIssue(t, a, 1, 2)
	mustIssue(t, a, 1, 3)
	mustJoin(t, a, 2)
	mustIssue(t, a, 1, 4)
	mustIssue(t, a, 2, 5)
	mustIssue(t, a, 1, 8)
	mustIssue(t, a, 2, 9)
	wantNext(t, a, 2, 13)
	wantJ(t, a, 4)

	mustObserve(t, a, 2, 12) // id < next，无变化
	wantNext(t, a, 2, 13)
	wantJ(t, a, 4)

	mustObserve(t, a, 2, 13) // id 恰等 next，对齐到 17
	wantNext(t, a, 2, 17)
	wantJ(t, a, 8)

	mustObserve(t, a, 2, 20) // 跨类：alignUp(21, 1) = 21
	wantNext(t, a, 2, 21)
	wantJ(t, a, 12)
	mustIssue(t, a, 2, 21)

	// 步长 1 时 Observe 直接置为 id+1。
	b := mustNew(t, 2, 2)
	mustObserve(t, b, 1, 5)
	wantNext(t, b, 1, 6)
	wantJ(t, b, 5)
	mustObserve(t, b, 1, 3) // id < next，无变化
	wantNext(t, b, 1, 6)
	wantJ(t, b, 5)
}

// 构造参数校验。
func TestNewParamValidation(t *testing.T) {
	for _, tc := range []struct {
		K int
		m int64
	}{
		{1, 1}, {9, 16}, {0, 4}, // K 越界
		{3, 2},  // m < K
		{2, 17}, // m > 16
	} {
		if _, err := New(tc.K, tc.m); err != ErrParam {
			t.Errorf("New(%d, %d) err = %v, want ErrParam", tc.K, tc.m, err)
		}
	}
	if _, err := New(2, 2); err != nil {
		t.Errorf("New(2, 2): %v", err)
	}
	if _, err := New(8, 16); err != nil {
		t.Errorf("New(8, 16): %v", err)
	}
}

// 拒绝次序与被拒不改状态：每个被拒操作前后状态完全一致。
func TestRejectionsKeepState(t *testing.T) {
	a := mustNew(t, 3, 4)
	mustIssue(t, a, 1, 1)
	mustJoin(t, a, 2)

	snapshot := func() (int64, []int64, []bool) {
		next := make([]int64, 3)
		active := make([]bool, 3)
		for s := 1; s <= 3; s++ {
			next[s-1] = a.Next(s)
			active[s-1] = a.Active(s)
		}
		return a.J(), next, active
	}
	check := func(name string, err, want error, j int64, next []int64, active []bool) {
		t.Helper()
		if err != want {
			t.Errorf("%s: err = %v, want %v", name, err, want)
		}
		j2, next2, active2 := snapshot()
		if j2 != j || !reflect.DeepEqual(next2, next) || !reflect.DeepEqual(active2, active) {
			t.Errorf("%s: 被拒后状态被改变", name)
		}
	}

	j, next, active := snapshot()

	var err error
	_, err = a.Issue(0)
	check("Issue s=0", err, ErrParam, j, next, active)
	_, err = a.Issue(4)
	check("Issue s=K+1", err, ErrParam, j, next, active)
	_, err = a.Issue(3)
	check("Issue 不活跃", err, ErrInactive, j, next, active)
	_, err = a.Reserve(1, 0)
	check("Reserve n=0", err, ErrParam, j, next, active)
	_, err = a.Reserve(1, 1001)
	check("Reserve n=1001", err, ErrParam, j, next, active)
	_, err = a.Reserve(0, 1)
	check("Reserve s=0", err, ErrParam, j, next, active)
	_, err = a.Reserve(3, 1)
	check("Reserve 不活跃", err, ErrInactive, j, next, active)
	err = a.Observe(1, 0)
	check("Observe id=0", err, ErrParam, j, next, active)
	err = a.Observe(1, MaxID+1)
	check("Observe id=MaxID+1", err, ErrParam, j, next, active)
	err = a.Observe(0, 1)
	check("Observe s=0", err, ErrParam, j, next, active)
	err = a.Observe(3, 10)
	check("Observe 不活跃", err, ErrInactive, j, next, active)
	err = a.Join(2, 0)
	check("Join s=0", err, ErrParam, j, next, active)
	err = a.Join(2, 4)
	check("Join s=K+1", err, ErrParam, j, next, active)
	err = a.Join(0, 3)
	check("Join role=0", err, ErrParam, j, next, active)
	err = a.Join(3, 3)
	check("Join role=3", err, ErrParam, j, next, active)
	err = a.Join(1, 3)
	check("Join role=1", err, ErrPermission, j, next, active)
	err = a.Join(1, 1)
	check("Join 权限优先于状态", err, ErrPermission, j, next, active)
	err = a.Join(2, 1)
	check("Join 已活跃", err, ErrState, j, next, active)
	err = a.Leave(2, 0)
	check("Leave s=0", err, ErrParam, j, next, active)
	err = a.Leave(1, 1)
	check("Leave role=1", err, ErrPermission, j, next, active)
	err = a.Leave(2, 3)
	check("Leave 不活跃", err, ErrState, j, next, active)

	// ErrLast：只剩一个活跃系统时。
	mustLeave(t, a, 2)
	j, next, active = snapshot()
	err = a.Leave(2, 1)
	check("Leave 最后活跃者", err, ErrLast, j, next, active)
	err = a.Leave(1, 1)
	check("Leave 权限优先于 ErrLast", err, ErrPermission, j, next, active)

	// ErrExhausted 排在状态类之后：活跃但编号耗尽。
	a.setNext(1, MaxID+1)
	j, next, active = snapshot()
	_, err = a.Issue(1)
	check("Issue 耗尽", err, ErrExhausted, j, next, active)
	_, err = a.Reserve(1, 2)
	check("Reserve 耗尽", err, ErrExhausted, j, next, active)
}

// 非导出计数器：Issue 与 Reserve 触碰的系统数恰为 1，与 K 无关。
func TestIssueReserveTouchSingleSystem(t *testing.T) {
	for K := 2; K <= 8; K++ {
		a := mustNew(t, K, int64(K))
		if _, err := a.Issue(1); err != nil {
			t.Fatalf("K=%d Issue: %v", K, err)
		}
		if a.touched != 1 {
			t.Errorf("K=%d Issue 触碰系统数 = %d, want 1", K, a.touched)
		}
		if _, err := a.Reserve(1, 1000); err != nil {
			t.Fatalf("K=%d Reserve: %v", K, err)
		}
		if a.touched != 1 {
			t.Errorf("K=%d Reserve 触碰系统数 = %d, want 1", K, a.touched)
		}
	}
}

// 并发签发：结果等价于某个串行顺序，编号全局唯一且不超 MaxID。
func TestConcurrentIssue(t *testing.T) {
	a := mustNew(t, 4, 4)
	mustJoin(t, a, 2)
	mustJoin(t, a, 3)
	mustJoin(t, a, 4)

	const workers = 8
	const perWorker = 500
	type issued struct {
		s  int
		id int64
	}
	ch := make(chan issued, workers*perWorker)
	var wg sync.WaitGroup
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			for i := 0; i < perWorker; i++ {
				s := w%4 + 1
				id, err := a.Issue(s)
				if err != nil {
					t.Errorf("Issue(%d): %v", s, err)
					return
				}
				ch <- issued{s, id}
			}
		}(w)
	}
	wg.Wait()
	close(ch)

	seen := make(map[int64]bool, workers*perWorker)
	count := 0
	for it := range ch {
		count++
		if it.id < 1 || it.id > MaxID {
			t.Fatalf("编号 %d 超出 [1, MaxID]", it.id)
		}
		if seen[it.id] {
			t.Fatalf("编号 %d 被重复签发", it.id)
		}
		seen[it.id] = true
		if it.id%4 != int64(it.s-1) {
			t.Fatalf("系统 %d 签发的 %d 不满足模 4 余 %d", it.s, it.id, it.s-1)
		}
	}
	if count != workers*perWorker {
		t.Fatalf("签发总数 = %d, want %d", count, workers*perWorker)
	}
}

// 相同操作序列重放得到相同编号与 J。
func TestReplayDeterminism(t *testing.T) {
	run := func() ([]int64, int64) {
		a := mustNew(t, 3, 4)
		var ids []int64
		for i := 0; i < 3; i++ {
			id, _ := a.Issue(1)
			ids = append(ids, id)
		}
		_ = a.Join(2, 2)
		_ = a.Join(2, 3)
		_ = a.Observe(1, 40)
		for _, s := range []int{1, 2, 3, 1, 2, 3} {
			id, _ := a.Issue(s)
			ids = append(ids, id)
		}
		_ = a.Leave(2, 3)
		_ = a.Leave(2, 2)
		for i := 0; i < 3; i++ {
			id, _ := a.Issue(1)
			ids = append(ids, id)
		}
		return ids, a.J()
	}
	ids1, j1 := run()
	ids2, j2 := run()
	if !reflect.DeepEqual(ids1, ids2) || j1 != j2 {
		t.Fatalf("重放不一致：ids %v vs %v, J %d vs %d", ids1, ids2, j1, j2)
	}
}
