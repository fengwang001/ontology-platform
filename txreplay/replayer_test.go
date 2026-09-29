package txreplay

import (
	"errors"
	"reflect"
	"sort"
	"sync"
	"testing"
)

// logRegister 打印一次登记及其判定依据。
func logRegister(t *testing.T, id int, deps []int, err error) {
	t.Helper()
	reason := "accepted"
	if err != nil {
		reason = "rejected: " + err.Error()
	}
	t.Logf("登记事务 %d, 依赖 %v => %s", id, deps, reason)
}

// logState 打印暂存/就绪/已回放三个集合。
func logState(t *testing.T, r *Replayer, tag string) {
	t.Helper()
	t.Logf("[%s] 暂存 pending=%v, 就绪 ready=%v, 已回放 replayed=%v",
		tag, r.Pending(), r.Ready(), r.Replayed())
}

func mustRegister(t *testing.T, r *Replayer, id int, deps ...int) {
	t.Helper()
	if err := r.Register(id, deps...); err != nil {
		t.Fatalf("登记事务 %d (依赖 %v) 失败: %v", id, deps, err)
	}
	logRegister(t, id, deps, nil)
}

func TestStagingThenAutoActivation(t *testing.T) {
	r := New()

	err := r.Register(3, 1, 2)
	logRegister(t, 3, []int{1, 2}, err)
	if err != nil {
		t.Fatalf("事务 3 依赖未登记时应暂存，实际错误: %v", err)
	}
	if got, want := r.Pending(), []int{3}; !reflect.DeepEqual(got, want) {
		t.Fatalf("暂存集 = %v, 期望 %v（依据：依赖 1、2 均未登记）", got, want)
	}

	err = r.Register(1)
	logRegister(t, 1, nil, err)
	// 仅依赖 1 就绪，2 仍缺失，事务 3 继续暂存。
	if got, want := r.Pending(), []int{3}; !reflect.DeepEqual(got, want) {
		t.Fatalf("依赖部分就绪时暂存集 = %v, 期望 %v", got, want)
	}

	err = r.Register(2, 1)
	logRegister(t, 2, []int{1}, err)
	logState(t, r, "回放前：1 无依赖可回放，2 等待 1，3 等待 1、2")

	seq := r.Replay()
	t.Logf("回放序列 = %v（判定依据：每步选可回放事务中标识最小者）", seq)
	if want := []int{1, 2, 3}; !reflect.DeepEqual(seq, want) {
		t.Fatalf("回放序列 = %v, 期望 %v", seq, want)
	}
	if got := r.Pending(); len(got) != 0 {
		t.Fatalf("回放后暂存集应为空，实际 %v", got)
	}
}

func TestDependencyFirst(t *testing.T) {
	// 依赖：4->2, 3->2, 2->1；即便乱序登记，依赖也必须先行回放。
	regs := []struct {
		id   int
		deps []int
	}{
		{4, []int{2}},
		{2, []int{1}},
		{3, []int{2}},
		{1, nil},
	}
	r := New()
	for _, reg := range regs {
		mustRegister(t, r, reg.id, reg.deps...)
	}

	seq := r.Replay()
	t.Logf("回放序列 = %v（判定依据：依赖必须先于后继出现）", seq)
	deps := map[int][]int{1: nil, 2: {1}, 3: {2}, 4: {2}}
	if !IsTopologicalOrder(seq, deps) {
		t.Fatalf("序列 %v 不满足依赖先行（拓扑序校验失败）", seq)
	}
	if want := []int{1, 2, 3, 4}; !reflect.DeepEqual(seq, want) {
		t.Fatalf("序列 = %v, 期望标识升序的 %v", seq, want)
	}
}

func TestAscendingIDTieBreak(t *testing.T) {
	// 三个互不依赖的事务同时就绪，必须按标识升序回放。
	r := New()
	for _, id := range []int{30, 10, 20} {
		mustRegister(t, r, id)
	}
	seq := r.Replay()
	t.Logf("回放序列 = %v（判定依据：无依赖关系，取最小标识）", seq)
	if want := []int{10, 20, 30}; !reflect.DeepEqual(seq, want) {
		t.Fatalf("序列 = %v, 期望 %v", seq, want)
	}
}

func TestCycleDetection(t *testing.T) {
	r := New()
	// 先构造 1->2->3，再登记 3 依赖 1 即闭环（1 等 2，2 等 3，3 将等 1）。
	mustRegister(t, r, 1, 2)
	mustRegister(t, r, 2, 3)

	err := r.Register(3, 1)
	logRegister(t, 3, []int{1}, err)
	if !errors.Is(err, ErrCycleDetected) {
		t.Fatalf("成环登记应返回 ErrCycleDetected，实际 %v", err)
	}

	// 环被整体拒绝：3 未进入任何集合，1、2 仍按原状暂存。
	if got := r.Pending(); !reflect.DeepEqual(got, []int{1, 2}) {
		t.Fatalf("拒绝成环后暂存集 = %v, 期望 [1 2]（依据：失败不改变图）", got)
	}
	if seq := r.Replay(); len(seq) != 0 {
		t.Fatalf("成环部分无可回放事务，实际回放 %v", seq)
	}

	// 解开环：登记 3 不带依赖后，3 先回放，随后 1、2 依次回放。
	mustRegister(t, r, 3)
	seq := r.Replay()
	t.Logf("补登记后回放序列 = %v", seq)
	if want := []int{3, 2, 1}; !reflect.DeepEqual(seq, want) {
		t.Fatalf("序列 = %v, 期望 %v（3 先就绪；随后仅 2 的依赖齐备，再 1）", seq, want)
	}
}

func TestRejectionReasonsAreDistinguishable(t *testing.T) {
	r := New()

	cases := []struct {
		name string
		id   int
		deps []int
		want error
	}{
		{"空标识", 0, nil, ErrInvalidID},
		{"负标识", -7, nil, ErrInvalidID},
		{"非法依赖标识", 5, []int{0}, ErrInvalidID},
	}
	for _, tc := range cases {
		err := r.Register(tc.id, tc.deps...)
		logRegister(t, tc.id, tc.deps, err)
		if !errors.Is(err, tc.want) {
			t.Fatalf("%s: 错误 = %v, 期望包裹 %v", tc.name, err, tc.want)
		}
	}

	// 自依赖。
	err := r.Register(9, 9)
	logRegister(t, 9, []int{9}, err)
	if !errors.Is(err, ErrSelfDependency) {
		t.Fatalf("自依赖应返回 ErrSelfDependency，实际 %v", err)
	}

	// 重复登记。
	mustRegister(t, r, 8)
	err = r.Register(8)
	logRegister(t, 8, nil, err)
	if !errors.Is(err, ErrDuplicateRegistration) {
		t.Fatalf("重复登记应返回 ErrDuplicateRegistration，实际 %v", err)
	}

	// 超限。
	small := New(WithMaxTransactions(2))
	mustRegister(t, small, 1)
	mustRegister(t, small, 2)
	err = small.Register(3)
	logRegister(t, 3, nil, err)
	if !errors.Is(err, ErrTooManyTransactions) {
		t.Fatalf("超限应返回 ErrTooManyTransactions，实际 %v", err)
	}
	if seq := small.Replay(); !reflect.DeepEqual(seq, []int{1, 2}) {
		t.Fatalf("超限拒绝不应改变已有状态，回放 = %v", seq)
	}
}

func TestRejectionIsAtomic(t *testing.T) {
	r := New()
	mustRegister(t, r, 1)
	r.Replay()

	beforePending := r.Pending()
	beforeReady := r.Ready()
	beforeReplayed := r.Replayed()

	// 非法登记（负依赖）必须整体拒绝，状态快照不变。
	err := r.Register(5, 1, -2)
	if !errors.Is(err, ErrInvalidID) {
		t.Fatalf("期望 ErrInvalidID，实际 %v", err)
	}
	if got := r.Pending(); !reflect.DeepEqual(got, beforePending) {
		t.Fatalf("失败后暂存集改变: %v -> %v", beforePending, got)
	}
	if got := r.Ready(); !reflect.DeepEqual(got, beforeReady) {
		t.Fatalf("失败后就绪集改变: %v -> %v", beforeReady, got)
	}
	if got := r.Replayed(); !reflect.DeepEqual(got, beforeReplayed) {
		t.Fatalf("失败后已回放集改变: %v -> %v", beforeReplayed, got)
	}

	// 成环登记同样整体拒绝：4 依赖 5（暂存），再让 5 依赖 4 即成环。
	mustRegister(t, r, 4, 5)
	snapPending := r.Pending()
	snapReady := r.Ready()
	err = r.Register(5, 4)
	logRegister(t, 5, []int{4}, err)
	if !errors.Is(err, ErrCycleDetected) {
		t.Fatalf("期望 ErrCycleDetected，实际 %v", err)
	}
	if !reflect.DeepEqual(r.Pending(), snapPending) || !reflect.DeepEqual(r.Ready(), snapReady) {
		t.Fatalf("拒绝成环后状态发生变化")
	}

	// 重复登记整体拒绝。
	err = r.Register(4)
	logRegister(t, 4, nil, err)
	if !errors.Is(err, ErrDuplicateRegistration) {
		t.Fatalf("期望 ErrDuplicateRegistration，实际 %v", err)
	}
	if !reflect.DeepEqual(r.Pending(), snapPending) || !reflect.DeepEqual(r.Ready(), snapReady) {
		t.Fatalf("拒绝重复登记后状态发生变化")
	}
}

func TestDeterministicReplayAcrossRuns(t *testing.T) {
	regs := []struct {
		id   int
		deps []int
	}{
		{6, []int{4}},
		{5, []int{2, 4}},
		{4, []int{1, 3}},
		{3, nil},
		{2, []int{1}},
		{1, nil},
	}
	depMap := make(map[int][]int, len(regs))
	for _, reg := range regs {
		depMap[reg.id] = reg.deps
	}

	var first []int
	for run := 0; run < 20; run++ {
		r := New()
		for _, reg := range regs {
			if err := r.Register(reg.id, reg.deps...); err != nil {
				t.Fatalf("run %d 登记 %d 失败: %v", run, reg.id, err)
			}
		}
		seq := r.Replay()
		if run == 0 {
			first = seq
			t.Logf("登记集合与依赖: %v", depMap)
			t.Logf("回放序列 = %v", seq)
		} else if !reflect.DeepEqual(seq, first) {
			t.Fatalf("run %d 序列 %v 与首次 %v 不一致", run, seq, first)
		}
	}
	if !IsTopologicalOrder(first, depMap) {
		t.Fatalf("序列 %v 未通过拓扑序核对", first)
	}
	t.Logf("判定依据：20 次独立回放序列均为 %v，且拓扑序核对通过", first)
	// 1 回放后 2 立即就绪，与无依赖的 3 同为可回放，取最小标识 2，再 3、4、5、6。
	if want := []int{1, 2, 3, 4, 5, 6}; !reflect.DeepEqual(first, want) {
		t.Fatalf("序列 = %v, 期望 %v", first, want)
	}
}

func TestReplayIsResumable(t *testing.T) {
	r := New()
	mustRegister(t, r, 1)
	mustRegister(t, r, 2, 1)

	first := r.Replay()
	t.Logf("第一次回放 = %v", first)
	if !reflect.DeepEqual(first, []int{1, 2}) {
		t.Fatalf("第一次回放 = %v", first)
	}
	if second := r.Replay(); len(second) != 0 {
		t.Fatalf("无新事务时回放应为空，实际 %v", second)
	}

	// 回放后再登记后继事务，可继续回放。
	mustRegister(t, r, 3, 2)
	logState(t, r, "登记后继事务 3 后")
	third := r.Replay()
	t.Logf("后续回放 = %v", third)
	if !reflect.DeepEqual(third, []int{3}) {
		t.Fatalf("后续回放 = %v, 期望 [3]", third)
	}
	if want := []int{1, 2, 3}; !reflect.DeepEqual(r.Replayed(), want) {
		t.Fatalf("累计已回放 = %v, 期望 %v", r.Replayed(), want)
	}
}

func TestConcurrentRegisterAndQuery(t *testing.T) {
	const n = 200
	r := New(WithMaxTransactions(n + 1))

	var wg sync.WaitGroup
	// 事务 id (2..n) 依赖 id-1，与事务 1 一起并发登记，互不冲突、无死锁。
	for id := 2; id <= n; id++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			if err := r.Register(id, id-1); err != nil {
				t.Errorf("并发登记 %d 失败: %v", id, err)
			}
		}(id)
	}
	wg.Add(1)
	go func() {
		defer wg.Done()
		if err := r.Register(1); err != nil {
			t.Errorf("并发登记 1 失败: %v", err)
		}
	}()

	// 与登记并发地反复查询状态，必须始终得到有序、自洽的快照。
	stop := make(chan struct{})
	go func() {
		for {
			select {
			case <-stop:
				return
			default:
				pending := r.Pending()
				ready := r.Ready()
				replayed := r.Replayed()
				if !sort.IntsAreSorted(pending) ||
					!sort.IntsAreSorted(ready) ||
					!sort.IntsAreSorted(replayed) {
					t.Errorf("查询返回了无序快照: pending=%v ready=%v replayed=%v",
						pending, ready, replayed)
					return
				}
			}
		}
	}()

	wg.Wait()
	close(stop)

	// 全部登记完成后，链式依赖必须按 1..n 完整回放，不多不少。
	seq := r.Replay()
	if len(seq) != n {
		t.Fatalf("回放数量 = %d, 期望 %d, 序列尾部=%v", len(seq), n, seq[max(0, len(seq)-5):])
	}
	for i, id := range seq {
		if id != i+1 {
			t.Fatalf("位置 %d 的事务 = %d, 期望 %d（依据：id 依赖 id-1）", i, id, i+1)
		}
	}
	t.Logf("并发登记 %d 个事务后回放序列长度 = %d，且严格满足 1..%d 依赖链", n, len(seq), n)
}

func TestIsTopologicalOrderHelper(t *testing.T) {
	deps := map[int][]int{1: nil, 2: {1}, 3: {1, 2}}
	if !IsTopologicalOrder([]int{1, 2, 3}, deps) {
		t.Fatal("合法拓扑序 [1 2 3] 被误判为非法")
	}
	if IsTopologicalOrder([]int{2, 1, 3}, deps) {
		t.Fatal("非法序列 [2 1 3] 被误判为合法（2 先于其依赖 1）")
	}
	if IsTopologicalOrder([]int{1, 1, 2}, deps) {
		t.Fatal("重复元素的序列被误判为合法")
	}
	t.Logf("拓扑序助手判定：[1 2 3] 合法，[2 1 3] 非法，依据为依赖位置必须更靠前")
}
