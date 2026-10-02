package deadlock

import (
	"reflect"
	"testing"
)

// unit 返回第 i 个分量为 1、长度为 n 的单位向量。
func unit(i, n int) []int64 {
	v := make([]int64, n)
	v[i] = 1
	return v
}

// TestBseqOvertakeOnRelease 后阻塞的小请求在 Release 后越过先阻塞的大请求：
// 授予不动点按 bseq 找第一个可满足者，而非严格队首阻塞。
func TestBseqOvertakeOnRelease(t *testing.T) {
	m := mustNew(t, 1, []int64{5}, []int64{1}, 3, 2)
	mustGrant(t, m, 0, [][]int64{{5}}, 0)
	mustBlock(t, m, 1, [][]int64{{3}}) // bseq=1，大请求
	mustBlock(t, m, 2, [][]int64{{1}}) // bseq=2，小请求
	// 只释放 1 个：进程 1 仍放不下，进程 2 越过它被授予。
	grants := mustRelease(t, m, 0, []int64{1})
	if !reflect.DeepEqual(grants, []Grant{{PID: 2, Alt: 0}}) {
		t.Fatalf("grants = %v, want [{2 0}]", grants)
	}
	// 再释放 3 个：进程 1 终于被满足。
	grants = mustRelease(t, m, 0, []int64{3})
	if !reflect.DeepEqual(grants, []Grant{{PID: 1, Alt: 0}}) {
		t.Fatalf("grants = %v, want [{1 0}]", grants)
	}
}

// TestGrantFixpointChain 一次 Release 触发授予连锁：按 bseq 顺序逐个授予，
// 每次授予后 avail 变化，更后的进程用新的 avail 判定。
func TestGrantFixpointChain(t *testing.T) {
	m := mustNew(t, 1, []int64{4}, []int64{1}, 4, 2)
	mustGrant(t, m, 0, [][]int64{{4}}, 0)
	mustBlock(t, m, 1, [][]int64{{1}}) // bseq=1
	mustBlock(t, m, 2, [][]int64{{1}}) // bseq=2
	mustBlock(t, m, 3, [][]int64{{2}}) // bseq=3
	// 释放 2 个：连锁授予进程 1、2 后 avail=0，进程 3 仍阻塞。
	grants := mustRelease(t, m, 0, []int64{2})
	want := []Grant{{PID: 1, Alt: 0}, {PID: 2, Alt: 0}}
	if !reflect.DeepEqual(grants, want) {
		t.Fatalf("grants = %v, want %v", grants, want)
	}
	// 进程 3 仍阻塞，但持有者均未阻塞（视为会结束），故不在死锁集合中。
	if got := m.Detect(); len(got) != 0 {
		t.Fatalf("Detect = %v, want empty", got)
	}
}

// TestUnblockedHolderPreventsDeadlock 未阻塞持有者被视为会结束，
// 等待它的阻塞进程不在死锁集合中。
func TestUnblockedHolderPreventsDeadlock(t *testing.T) {
	m := mustNew(t, 1, []int64{2}, []int64{1}, 2, 2)
	mustGrant(t, m, 0, [][]int64{{2}}, 0)
	mustBlock(t, m, 1, [][]int64{{1}})
	if got := m.Detect(); len(got) != 0 {
		t.Fatalf("Detect = %v, want empty (holder 0 is unblocked)", got)
	}
}

// TestWaitingOnDeadlockedOnly 阻塞进程只等死锁进程持有的资源时，
// 它自己也计入死锁集合；不持有资源的死锁成员不会被选为牺牲者。
func TestWaitingOnDeadlockedOnly(t *testing.T) {
	m := mustNew(t, 1, []int64{2}, []int64{1}, 3, 2)
	mustGrant(t, m, 0, [][]int64{{1}}, 0)
	mustGrant(t, m, 1, [][]int64{{1}}, 0)
	mustBlock(t, m, 0, [][]int64{{1}}) // bseq=1，持有 1
	mustBlock(t, m, 1, [][]int64{{1}}) // bseq=2，持有 1
	mustBlock(t, m, 2, [][]int64{{1}}) // bseq=3，持有 0
	if got := m.Detect(); !reflect.DeepEqual(got, []int{0, 1, 2}) {
		t.Fatalf("Detect = %v, want [0 1 2]", got)
	}
	steps := m.Resolve()
	// 进程 2 持有量为 0 不得被选；进程 0、1 代价并列取小编号 0。
	want := []ResolveStep{{
		Victim:     0,
		Cost:       1,
		Grants:     []Grant{{PID: 1, Alt: 0}},
		Terminated: false,
	}}
	if !reflect.DeepEqual(steps, want) {
		t.Fatalf("Resolve = %+v, want %+v", steps, want)
	}
	if got := m.Detect(); len(got) != 0 {
		t.Fatalf("Detect after Resolve = %v, want empty", got)
	}
}

// TestAltPlusAllocAgainstTotal 备选加已持有量恰等于 T 允许，大 1 永不可满足。
func TestAltPlusAllocAgainstTotal(t *testing.T) {
	m := mustNew(t, 1, []int64{3}, []int64{1}, 2, 2)
	mustGrant(t, m, 0, [][]int64{{2}}, 0)
	mustGrant(t, m, 1, [][]int64{{1}}, 0)
	// alloc[0]+alt = 2+1 = 3 == T：允许（avail 不足，阻塞而非拒绝）。
	mustBlock(t, m, 0, [][]int64{{1}})
	// alloc[1]+alt = 1+3 = 4 > T：唯一备选不可满足，整体拒绝。
	_, err := m.Request(1, [][]int64{{3}})
	wantErrCode(t, err, ErrUnsatisfiable)
	// 多备选：只要还有一个备选原则上可满足，请求即被接受（阻塞）。
	mustBlock(t, m, 1, [][]int64{{3}, {1}})
}

// TestVictimCostTie 牺牲者代价并列取编号小者。
func TestVictimCostTie(t *testing.T) {
	m := mustNew(t, 1, []int64{2}, []int64{1}, 2, 2)
	mustGrant(t, m, 0, [][]int64{{1}}, 0)
	mustGrant(t, m, 1, [][]int64{{1}}, 0)
	mustBlock(t, m, 0, [][]int64{{1}})
	mustBlock(t, m, 1, [][]int64{{1}})
	steps := m.Resolve()
	if len(steps) != 1 || steps[0].Victim != 0 || steps[0].Cost != 1 {
		t.Fatalf("Resolve = %+v, want single step with victim 0 cost 1", steps)
	}
}

// TestRollbackCountChangesVictim 回滚次数使代价翻倍而改变牺牲者：
// 先前便宜者因 rb 较大反而不再最小。
func TestRollbackCountChangesVictim(t *testing.T) {
	m := mustNew(t, 2, []int64{1, 1}, []int64{1, 1}, 2, 3)
	mustGrant(t, m, 0, [][]int64{{1, 0}}, 0)
	mustGrant(t, m, 1, [][]int64{{0, 1}}, 0)
	mustBlock(t, m, 0, [][]int64{{0, 1}})
	mustBlock(t, m, 1, [][]int64{{1, 0}})
	// 第一轮：代价 1 对 1，并列取小编号 0，rb[0]=1。
	steps := m.Resolve()
	if len(steps) != 1 || steps[0].Victim != 0 || steps[0].Cost != 1 || steps[0].Terminated {
		t.Fatalf("first Resolve = %+v, want victim 0 cost 1 not terminated", steps)
	}
	// 回滚后进程 0 存活、阻塞请求已取消、持有 0，可再次 Request。
	// 当前 avail=[0,0]、进程 1 持有 [1,1]（第一轮的授予连锁已授予其 [1,0]）。
	mustBlock(t, m, 0, [][]int64{{0, 1}}) // 若旧阻塞请求未取消，这里会报“已阻塞”
	if got := m.Detect(); len(got) != 0 {
		t.Fatalf("Detect = %v, want empty (holder 1 is unblocked)", got)
	}
	// 重建第二轮死锁：进程 1 释放 [0,1]，进程 0 获得 [0,1]。
	grants := mustRelease(t, m, 1, []int64{0, 1})
	if !reflect.DeepEqual(grants, []Grant{{PID: 0, Alt: 0}}) {
		t.Fatalf("grants = %v, want [{0 0}]", grants)
	}
	mustBlock(t, m, 0, [][]int64{{1, 0}})
	mustBlock(t, m, 1, [][]int64{{0, 1}})
	if got := m.Detect(); !reflect.DeepEqual(got, []int{0, 1}) {
		t.Fatalf("Detect = %v, want [0 1]", got)
	}
	// 第二轮：进程 0 代价 1*(1+1)=2，进程 1 代价 1*(1+0)=1，
	// 先前并列取小的进程 0 因 rb 较大不再最小，牺牲者变为 1。
	steps = m.Resolve()
	if len(steps) != 1 || steps[0].Victim != 1 || steps[0].Cost != 1 {
		t.Fatalf("second Resolve = %+v, want victim 1 cost 1", steps)
	}
}

// 让 p0、p1 在 R=2、T=[1,1] 上互相阻塞：p0 持 [1,0] 等 [0,1]，p1 持 [0,1] 等 [1,0]。
func buildPairDeadlock(t *testing.T, m *Manager) {
	t.Helper()
	mustGrant(t, m, 0, [][]int64{{1, 0}}, 0)
	mustGrant(t, m, 1, [][]int64{{0, 1}}, 0)
	mustBlock(t, m, 0, [][]int64{{0, 1}})
	mustBlock(t, m, 1, [][]int64{{1, 0}})
	if got := m.Detect(); !reflect.DeepEqual(got, []int{0, 1}) {
		t.Fatalf("Detect = %v, want [0 1]", got)
	}
}

// TestTerminationAtL rb 加一后恰等于 L 永久中止，小 1 不中止。
func TestTerminationAtL(t *testing.T) {
	m := mustNew(t, 2, []int64{1, 1}, []int64{1, 1}, 2, 2)
	buildPairDeadlock(t, m)
	// 第一轮：并列取小编号 0，rb[0]=1 < L=2，不中止。
	steps := m.Resolve()
	if len(steps) != 1 || steps[0].Victim != 0 || steps[0].Terminated {
		t.Fatalf("first Resolve = %+v, want victim 0 not terminated", steps)
	}
	// 进程 0 仍存活，可以再次 Request。
	mustBlock(t, m, 0, [][]int64{{0, 1}})
	// 重建死锁：进程 1 释放 [0,1] 授予进程 0，再互相阻塞。
	grants := mustRelease(t, m, 1, []int64{0, 1})
	if !reflect.DeepEqual(grants, []Grant{{PID: 0, Alt: 0}}) {
		t.Fatalf("grants = %v, want [{0 0}]", grants)
	}
	mustBlock(t, m, 0, [][]int64{{1, 0}})
	mustBlock(t, m, 1, [][]int64{{0, 1}})
	// 第二轮：进程 0 代价 1*(1+1)=2，进程 1 代价 1，牺牲者为 1，rb[1]=1 不中止。
	steps = m.Resolve()
	if len(steps) != 1 || steps[0].Victim != 1 || steps[0].Terminated {
		t.Fatalf("second Resolve = %+v, want victim 1 not terminated", steps)
	}
	// 第三轮：两者 rb 均为 1，代价并列取小编号 0，rb[0]=2 == L，永久中止。
	mustBlock(t, m, 1, [][]int64{{0, 1}})
	grants = mustRelease(t, m, 0, []int64{0, 1})
	if !reflect.DeepEqual(grants, []Grant{{PID: 1, Alt: 0}}) {
		t.Fatalf("grants = %v, want [{1 0}]", grants)
	}
	mustBlock(t, m, 1, [][]int64{{1, 0}})
	mustBlock(t, m, 0, [][]int64{{0, 1}})
	steps = m.Resolve()
	if len(steps) != 1 || steps[0].Victim != 0 || !steps[0].Terminated {
		t.Fatalf("third Resolve = %+v, want victim 0 terminated", steps)
	}
	// 永久中止后进程不存在。
	_, err := m.Request(0, [][]int64{{1, 0}})
	wantErrCode(t, err, ErrNoSuchProcess)
	_, err = m.Release(0, []int64{1, 0})
	wantErrCode(t, err, ErrNoSuchProcess)
}

// TestTerminationAtLOne L=1 时第一次回滚即永久中止，中止后授予连锁并再次检测为空。
func TestTerminationAtLOne(t *testing.T) {
	m := mustNew(t, 2, []int64{1, 1}, []int64{1, 1}, 2, 1)
	buildPairDeadlock(t, m)
	steps := m.Resolve()
	want := []ResolveStep{{
		Victim:     0,
		Cost:       1,
		Grants:     []Grant{{PID: 1, Alt: 0}},
		Terminated: true,
	}}
	if !reflect.DeepEqual(steps, want) {
		t.Fatalf("Resolve = %+v, want %+v", steps, want)
	}
	if got := m.Detect(); len(got) != 0 {
		t.Fatalf("Detect after Resolve = %v, want empty", got)
	}
	_, err := m.Request(0, [][]int64{{1, 0}})
	wantErrCode(t, err, ErrNoSuchProcess)
}

// TestMultiRoundResolve 两对独立死锁，Resolve 多轮各选一个牺牲者。
func TestMultiRoundResolve(t *testing.T) {
	m := mustNew(t, 2, []int64{2, 2}, []int64{1, 1}, 4, 2)
	mustGrant(t, m, 0, [][]int64{{1, 0}}, 0)
	mustGrant(t, m, 1, [][]int64{{1, 0}}, 0)
	mustBlock(t, m, 0, [][]int64{{1, 0}})
	mustBlock(t, m, 1, [][]int64{{1, 0}})
	mustGrant(t, m, 2, [][]int64{{0, 1}}, 0)
	mustGrant(t, m, 3, [][]int64{{0, 1}}, 0)
	mustBlock(t, m, 2, [][]int64{{0, 1}})
	mustBlock(t, m, 3, [][]int64{{0, 1}})
	if got := m.Detect(); !reflect.DeepEqual(got, []int{0, 1, 2, 3}) {
		t.Fatalf("Detect = %v, want [0 1 2 3]", got)
	}
	steps := m.Resolve()
	want := []ResolveStep{
		{Victim: 0, Cost: 1, Grants: []Grant{{PID: 1, Alt: 0}}, Terminated: false},
		{Victim: 2, Cost: 1, Grants: []Grant{{PID: 3, Alt: 0}}, Terminated: false},
	}
	if !reflect.DeepEqual(steps, want) {
		t.Fatalf("Resolve = %+v, want %+v", steps, want)
	}
	if got := m.Detect(); len(got) != 0 {
		t.Fatalf("Detect after Resolve = %v, want empty", got)
	}
}

// TestRejectedOpsKeepStateAndBseq 被拒绝的操作不改变状态，也不消耗 bseq。
func TestRejectedOpsKeepStateAndBseq(t *testing.T) {
	m := mustNew(t, 1, []int64{2}, []int64{1}, 3, 2)
	mustGrant(t, m, 0, [][]int64{{2}}, 0)
	mustBlock(t, m, 1, [][]int64{{1}}) // bseq=1
	ctr := m.bseqCtr
	// 各种被拒绝的 Request。
	if _, err := m.Request(2, [][]int64{}); err == nil {
		t.Fatal("want error for 0 alternatives")
	}
	if _, err := m.Request(2, [][]int64{{1}, {1}, {1}, {1}}); err == nil {
		t.Fatal("want error for 4 alternatives")
	}
	if _, err := m.Request(2, [][]int64{{1, 1}}); err == nil {
		t.Fatal("want error for wrong vector length")
	}
	if _, err := m.Request(2, [][]int64{{-1}}); err == nil {
		t.Fatal("want error for negative amount")
	}
	if _, err := m.Request(2, [][]int64{{0}}); err == nil {
		t.Fatal("want error for all-zero vector")
	}
	if _, err := m.Request(9, [][]int64{{1}}); err == nil {
		t.Fatal("want error for unknown process")
	}
	if _, err := m.Request(1, [][]int64{{1}}); err == nil {
		t.Fatal("want error for blocked process")
	}
	if _, err := m.Request(2, [][]int64{{3}}); err == nil {
		t.Fatal("want error for unsatisfiable request")
	}
	// 各种被拒绝的 Release。
	if _, err := m.Release(2, []int64{0}); err == nil {
		t.Fatal("want error for all-zero release")
	}
	if _, err := m.Release(9, []int64{1}); err == nil {
		t.Fatal("want error for unknown process")
	}
	if _, err := m.Release(1, []int64{1}); err == nil {
		t.Fatal("want error for blocked process")
	}
	if _, err := m.Release(2, []int64{1}); err == nil {
		t.Fatal("want error for over-release")
	}
	if m.bseqCtr != ctr {
		t.Fatalf("bseq counter changed by rejected ops: %d -> %d", ctr, m.bseqCtr)
	}
	// 状态未被改变：进程 2 阻塞成功且拿到下一个序号，释放 [2] 后按序授予。
	mustBlock(t, m, 2, [][]int64{{1}}) // bseq=2
	grants := mustRelease(t, m, 0, []int64{2})
	want := []Grant{{PID: 1, Alt: 0}, {PID: 2, Alt: 0}}
	if !reflect.DeepEqual(grants, want) {
		t.Fatalf("grants = %v, want %v", grants, want)
	}
}

// TestChecksBoundChain 全部进程串成一条等待链的最坏输入下，
// checks 恰好为 b(b+1)/2，证明归约不是枚举完成顺序。
func TestChecksBoundChain(t *testing.T) {
	const b = 7
	R := b + 1
	T := make([]int64, R)
	c := make([]int64, R)
	for i := range T {
		T[i], c[i] = 1, 1
	}
	m := mustNew(t, R, T, c, b+1, 2)
	// 进程 b（不阻塞）持有资源 b；进程 i 持有资源 i 并等待资源 i+1。
	mustGrant(t, m, b, [][]int64{unit(b, R)}, 0)
	for i := 0; i < b; i++ {
		mustGrant(t, m, i, [][]int64{unit(i, R)}, 0)
	}
	for i := 0; i < b; i++ {
		mustBlock(t, m, i, [][]int64{unit(i+1, R)})
	}
	// 归约只能按编号降序逐个完成：每趟扫描恰好完成一个进程。
	if got := m.Detect(); len(got) != 0 {
		t.Fatalf("Detect = %v, want empty (chain reduces fully)", got)
	}
	wantChecks := int64(b * (b + 1) / 2)
	if m.Checks() != wantChecks {
		t.Fatalf("checks = %d, want exactly %d", m.Checks(), wantChecks)
	}
}
