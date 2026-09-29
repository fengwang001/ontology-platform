package replay

import (
	"errors"
	"fmt"
	"reflect"
	"sync"
	"testing"
)

// mustRegister 登记事务并在失败时终止测试，同时打印登记日志。
func mustRegister(t *testing.T, r *Replayer, id int64, deps ...int64) {
	t.Helper()
	if err := r.Register(id, deps); err != nil {
		t.Fatalf("登记 id=%d deps=%v 失败: %v", id, deps, err)
	}
	t.Logf("登记成功: id=%d deps=%v", id, deps)
}

// assertTopoValid 判定依据：回放序列中每个事务的所有依赖都先于它出现。
func assertTopoValid(t *testing.T, r *Replayer, seq []int64, depsOf map[int64][]int64) {
	t.Helper()
	pos := make(map[int64]int, len(seq))
	for i, id := range seq {
		pos[id] = i
	}
	for _, id := range seq {
		for _, d := range depsOf[id] {
			if pos[d] >= pos[id] {
				t.Fatalf("依赖先行被破坏: id=%d 出现在依赖 %d 之前, seq=%v", id, d, seq)
			}
		}
	}
	t.Logf("判定依据: 序列 %v 中每个事务的依赖均先行出现, 拓扑序成立", seq)
}

func TestStagingActivation(t *testing.T) {
	r := NewReplayer(0)

	mustRegister(t, r, 10, 20, 30)
	if !r.IsPending(10) {
		t.Fatal("依赖 20、30 未登记, id=10 应进入暂存集")
	}
	t.Logf("判定依据: 依赖未齐, id=10 暂存, 暂存集=%v", r.Pending())

	mustRegister(t, r, 20)
	if !r.IsPending(10) {
		t.Fatal("依赖 30 仍未登记, id=10 应继续暂存")
	}
	t.Logf("判定依据: 依赖 30 未登记, id=10 保持暂存, 暂存集=%v", r.Pending())

	mustRegister(t, r, 30)
	if r.IsPending(10) {
		t.Fatal("依赖已全部登记, id=10 应自动激活")
	}
	t.Logf("判定依据: 依赖 20、30 已齐, id=10 自动激活, 暂存集=%v", r.Pending())

	seq := r.Replay()
	t.Logf("回放序列: %v", seq)
	if !reflect.DeepEqual(seq, []int64{20, 30, 10}) {
		t.Fatalf("回放序列应为 [20 30 10], 实际 %v", seq)
	}
	assertTopoValid(t, r, seq, map[int64][]int64{10: {20, 30}})
}

func TestDependencyOrder(t *testing.T) {
	r := NewReplayer(0)
	depsOf := map[int64][]int64{
		1: {},
		2: {1},
		3: {2},
		4: {3},
	}
	// 逆依赖方向登记，验证回放仍按依赖先行。
	for _, id := range []int64{4, 3, 2, 1} {
		mustRegister(t, r, id, depsOf[id]...)
	}

	seq := r.Replay()
	t.Logf("回放序列: %v", seq)
	if !reflect.DeepEqual(seq, []int64{1, 2, 3, 4}) {
		t.Fatalf("回放序列应为 [1 2 3 4], 实际 %v", seq)
	}
	assertTopoValid(t, r, seq, depsOf)

	if got := r.Replay(); len(got) != 0 {
		t.Fatalf("全部回放后再次回放应为空, 实际 %v", got)
	}
	t.Logf("判定依据: 已回放集=%v, 再次回放无可用事务", r.Replayed())
}

func TestCycleDetection(t *testing.T) {
	r := NewReplayer(0)

	mustRegister(t, r, 1, 2) // 依赖 2 未登记, 暂存
	mustRegister(t, r, 3, 1) // 依赖 1 已登记, 激活
	t.Logf("暂存集=%v", r.Pending())

	err := r.Register(2, []int64{3})
	if !errors.Is(err, ErrCycle) {
		t.Fatalf("2->3->1->2 成环, 应拒绝并报 ErrCycle, 实际 %v", err)
	}
	t.Logf("判定依据: 登记 id=2 deps=[3] 被拒绝, 原因=%v", err)

	// 一次失败不得改变依赖图、暂存集与已回放集。
	if _, ok := r.txs[2]; ok {
		t.Fatal("成环登记被拒绝后, id=2 不应进入依赖图")
	}
	if !reflect.DeepEqual(r.Pending(), []int64{1}) {
		t.Fatalf("暂存集应保持 [1], 实际 %v", r.Pending())
	}
	t.Logf("判定依据: 失败后依赖图与暂存集不变, 暂存集=%v", r.Pending())

	// 自环以外的间接环: 4->5, 5->6, 再登记 6->4 成环。
	mustRegister(t, r, 4, 5)
	mustRegister(t, r, 5, 6)
	if err := r.Register(6, []int64{4}); !errors.Is(err, ErrCycle) {
		t.Fatalf("6->4->5->6 成环, 应拒绝并报 ErrCycle, 实际 %v", err)
	}
	t.Logf("判定依据: 登记 id=6 deps=[4] 被拒绝, 原因=ErrCycle")
}

func TestAscendingIDOrder(t *testing.T) {
	r := NewReplayer(0)
	// 多个互不依赖的事务同时可回放, 验证每次取标识最小者。
	for _, id := range []int64{9, 5, 7, 1, 3} {
		mustRegister(t, r, id)
	}

	seq := r.Replay()
	t.Logf("回放序列: %v", seq)
	if !reflect.DeepEqual(seq, []int64{1, 3, 5, 7, 9}) {
		t.Fatalf("可回放事务应按标识升序回放, 实际 %v", seq)
	}
	t.Logf("判定依据: 每轮选取可回放事务中标识最小者, 序列严格升序")
}

func TestRegisterValidation(t *testing.T) {
	cases := []struct {
		name string
		id   int64
		deps []int64
		want error
	}{
		{"空标识", 0, nil, ErrInvalidID},
		{"负标识", -3, nil, ErrInvalidID},
		{"依赖含空标识", 1, []int64{0}, ErrInvalidID},
		{"依赖含负标识", 1, []int64{-2}, ErrInvalidID},
		{"自依赖", 1, []int64{1}, ErrSelfDependency},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := NewReplayer(0)
			err := r.Register(tc.id, tc.deps)
			if !errors.Is(err, tc.want) {
				t.Fatalf("登记 id=%d deps=%v 应报 %v, 实际 %v", tc.id, tc.deps, tc.want, err)
			}
			t.Logf("判定依据: 登记 id=%d deps=%v 被拒绝, 原因=%v", tc.id, tc.deps, err)
			if len(r.txs) != 0 || len(r.staged) != 0 || len(r.replayed) != 0 {
				t.Fatal("非法登记被拒绝后, 依赖图、暂存集与已回放集必须为空")
			}
		})
	}

	t.Run("重复登记", func(t *testing.T) {
		r := NewReplayer(0)
		mustRegister(t, r, 1)
		if err := r.Register(1, nil); !errors.Is(err, ErrDuplicate) {
			t.Fatalf("重复登记应报 ErrDuplicate, 实际 %v", err)
		}
		t.Logf("判定依据: id=1 已登记, 再次登记被拒绝, 原因=ErrDuplicate")
	})

	t.Run("超出上限", func(t *testing.T) {
		r := NewReplayer(2)
		mustRegister(t, r, 1)
		mustRegister(t, r, 2)
		if err := r.Register(3, nil); !errors.Is(err, ErrCapacityExceeded) {
			t.Fatalf("超出上限应报 ErrCapacityExceeded, 实际 %v", err)
		}
		t.Logf("判定依据: 上限为 2, 登记 id=3 被拒绝, 原因=ErrCapacityExceeded")
	})
}

func TestFailedRegisterKeepsState(t *testing.T) {
	r := NewReplayer(0)
	mustRegister(t, r, 1)
	mustRegister(t, r, 2, 1)
	mustRegister(t, r, 3, 99) // 依赖未登记, 暂存
	mustRegister(t, r, 4, 5)  // 依赖未登记, 暂存
	seq := r.Replay()
	t.Logf("回放序列: %v, 已回放集=%v, 暂存集=%v", seq, r.Replayed(), r.Pending())

	pendingBefore := r.Pending()
	replayedBefore := r.Replayed()
	txCountBefore := len(r.txs)

	// 重复、非法标识、自依赖、成环均须整体拒绝。
	if err := r.Register(1, nil); !errors.Is(err, ErrDuplicate) {
		t.Fatalf("重复登记应报 ErrDuplicate, 实际 %v", err)
	}
	if err := r.Register(0, nil); !errors.Is(err, ErrInvalidID) {
		t.Fatalf("空标识应报 ErrInvalidID, 实际 %v", err)
	}
	if err := r.Register(6, []int64{6}); !errors.Is(err, ErrSelfDependency) {
		t.Fatalf("自依赖应报 ErrSelfDependency, 实际 %v", err)
	}
	if err := r.Register(5, []int64{4}); !errors.Is(err, ErrCycle) {
		t.Fatalf("5->4->5 成环应报 ErrCycle, 实际 %v", err)
	}
	t.Logf("判定依据: 重复/空标识/自依赖/成环四类登记均被拒绝")

	if len(r.txs) != txCountBefore {
		t.Fatalf("失败登记改变了依赖图: 事务数 %d -> %d", txCountBefore, len(r.txs))
	}
	if !reflect.DeepEqual(r.Pending(), pendingBefore) {
		t.Fatalf("失败登记改变了暂存集: %v -> %v", pendingBefore, r.Pending())
	}
	if !reflect.DeepEqual(r.Replayed(), replayedBefore) {
		t.Fatalf("失败登记改变了已回放集: %v -> %v", replayedBefore, r.Replayed())
	}
	t.Logf("判定依据: 失败登记后依赖图、暂存集=%v、已回放集=%v 均不变", r.Pending(), r.Replayed())
}

func TestDeterministicReplay(t *testing.T) {
	depsOf := map[int64][]int64{
		1: {},
		2: {1},
		3: {1},
		4: {2, 3},
		5: {},
	}
	build := func() *Replayer {
		r := NewReplayer(0)
		for _, id := range []int64{4, 2, 5, 3, 1} {
			mustRegister(t, r, id, depsOf[id]...)
		}
		return r
	}

	first := build().Replay()
	for i := 0; i < 10; i++ {
		got := build().Replay()
		if !reflect.DeepEqual(got, first) {
			t.Fatalf("第 %d 次回放序列 %v 与首次 %v 不一致", i+2, got, first)
		}
	}
	t.Logf("回放序列: %v (10 次重建回放逐次相同)", first)
	assertTopoValid(t, build(), first, depsOf)
}

func TestConcurrentRegisterAndQuery(t *testing.T) {
	r := NewReplayer(0)
	const workers = 8
	const perWorker = 50

	var wg sync.WaitGroup
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func(base int64) {
			defer wg.Done()
			for i := int64(1); i <= perWorker; i++ {
				id := base + i
				if err := r.Register(id, nil); err != nil {
					t.Errorf("并发登记 id=%d 失败: %v", id, err)
				}
				_ = r.Replayed()
				_ = r.Pending()
				_ = r.IsReplayed(id)
				_ = r.IsPending(id)
			}
		}(int64(w * perWorker))
	}
	wg.Wait()

	if got := len(r.txs); got != workers*perWorker {
		t.Fatalf("并发登记后应有 %d 个事务, 实际 %d", workers*perWorker, got)
	}
	seq := r.Replay()
	t.Logf("回放序列长度: %d, 前 5 项: %v", len(seq), seq[:5])
	if len(seq) != workers*perWorker {
		t.Fatalf("应回放全部 %d 个事务, 实际 %d", workers*perWorker, len(seq))
	}
	for i := 1; i < len(seq); i++ {
		if seq[i] <= seq[i-1] {
			t.Fatalf("无依赖并发登记应按标识升序回放, seq[%d]=%d <= seq[%d]=%d", i, seq[i], i-1, seq[i-1])
		}
	}
	t.Logf("判定依据: %d 个并发登记互不冲突, 回放序列严格升序", workers*perWorker)
}

func ExampleReplayer() {
	r := NewReplayer(0)
	_ = r.Register(2, []int64{1})
	_ = r.Register(1, nil)
	fmt.Println(r.Replay())
	// Output: [1 2]
}
