package idpool

import (
	"errors"
	"sync"
	"sync/atomic"
	"testing"
)

func newPoolOrFail(t *testing.T, n int, q, qmax, r int64) *Pool {
	t.Helper()
	p, err := New(n, q, qmax, r)
	t.Logf("输入: New(N=%d, Q=%d, Qmax=%d, R=%d) -> 输出: pool!=%v, err=%v; 判定: 构造应成功", n, q, qmax, r, p == nil, err)
	if err != nil {
		t.Fatalf("New 返回意外错误: %v", err)
	}
	return p
}

func mustAllocate(t *testing.T, p *Pool, now, wantID int64, reason string) {
	t.Helper()
	id, fail, err := p.AllocateDetailed(now)
	t.Logf("输入: Allocate(now=%d) -> 输出: id=%d, failure=%+v, err=%v; 判定: %s", now, id, fail, err, reason)
	if err != nil {
		t.Fatalf("Allocate(%d) 意外失败: %v", now, err)
	}
	if int64(id) != wantID {
		t.Fatalf("Allocate(%d) = %d, 期望 %d", now, id, wantID)
	}
}

func failAllocate(t *testing.T, p *Pool, now, wantReady int64, wantReadyID int, wantErr error, reason string) {
	t.Helper()
	id, fail, err := p.AllocateDetailed(now)
	t.Logf("输入: Allocate(now=%d) -> 输出: id=%d, failure=%+v, err=%v; 判定: %s", now, id, fail, err, reason)
	if !errors.Is(err, wantErr) {
		t.Fatalf("Allocate(%d) err=%v, 期望 %v", now, err, wantErr)
	}
	if fail.EarliestReady != wantReady || fail.EarliestID != wantReadyID {
		t.Fatalf("Allocate(%d) failure=%+v, 期望 ready=%d/id=%d", now, fail, wantReady, wantReadyID)
	}
}

func mustRelease(t *testing.T, p *Pool, id int, now int64, reason string) {
	t.Helper()
	err := p.Release(id, now)
	t.Logf("输入: Release(id=%d, now=%d) -> 输出: err=%v; 判定: %s", id, now, err, reason)
	if err != nil {
		t.Fatalf("Release(%d,%d) 意外失败: %v", id, now, err)
	}
}

func rejectRelease(t *testing.T, p *Pool, id int, now int64, wantErr error, reason string) {
	t.Helper()
	err := p.Release(id, now)
	t.Logf("输入: Release(id=%d, now=%d) -> 输出: err=%v; 判定: %s", id, now, err, reason)
	if !errors.Is(err, wantErr) {
		t.Fatalf("Release(%d,%d) err=%v, 期望 %v", id, now, err, wantErr)
	}
}

// TestInvalidConfig 覆盖四类构造参数非法情形。
func TestInvalidConfig(t *testing.T) {
	cases := []struct {
		name       string
		n          int
		q, qmax, r int64
	}{
		{"N_not_positive", 0, 10, 100, 50},
		{"Q_not_positive", 3, 0, 100, 50},
		{"Qmax_less_than_Q", 3, 20, 10, 50},
		{"R_not_positive", 3, 10, 100, 0},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			p, err := New(c.n, c.q, c.qmax, c.r)
			t.Logf("输入: New(n=%d,q=%d,qmax=%d,r=%d) -> 输出: p!=%v err=%v; 判定: 必须拒绝 ErrInvalidConfig", c.n, c.q, c.qmax, c.r, p == nil, err)
			if !errors.Is(err, ErrInvalidConfig) {
				t.Fatalf("err=%v, 期望 ErrInvalidConfig", err)
			}
		})
	}
}

// TestMinFreeAndReadyBoundary 验证最小空闲分配、恰在解除时刻可分配、并列取小。
func TestMinFreeAndReadyBoundary(t *testing.T) {
	p := newPoolOrFail(t, 3, 10, 100, 50)
	mustAllocate(t, p, 0, 1, "首次分配最小编号 1")
	mustAllocate(t, p, 1, 2, "下一个最小空闲为 2")
	mustAllocate(t, p, 2, 3, "下一个最小空闲为 3")
	mustRelease(t, p, 2, 5, "编号2存活5<50短命: 2Q=20, readyAt=25")

	failAllocate(t, p, 24, 25, 2, ErrNoFree, "时刻24未到25, 绝不提前借出; 报告 2@25")
	mustAllocate(t, p, 25, 2, "恰在解除时刻25即可分配编号2")

	mustRelease(t, p, 1, 30, "编号1短命(30ms): 20, readyAt=50")
	mustRelease(t, p, 3, 30, "编号3短命(28ms): 20, readyAt=50")
	mustRelease(t, p, 2, 30, "编号2短命(5ms): 上次20->40, readyAt=70")
	failAllocate(t, p, 30, 50, 1, ErrNoFree, "1与3同于50解除, 并列取小报告1")
	mustAllocate(t, p, 50, 1, "时刻50编号1、3空闲, 取最小1")
	mustAllocate(t, p, 50, 3, "仍在时刻50, 下一空闲为3")
	failAllocate(t, p, 69, 70, 2, ErrNoFree, "编号2于70解除")
	mustAllocate(t, p, 70, 2, "时刻70编号2空闲")
}

// TestDoublingCapAndReset 验证短命逐次翻倍、封顶 Qmax、正常寿命重置后再次短命从 Q 翻倍。
func TestDoublingCapAndReset(t *testing.T) {
	p := newPoolOrFail(t, 1, 10, 25, 50)

	// 周期1: alloc@100, 存活5 -> q=min(2*10,25)=20, ready=125
	mustAllocate(t, p, 100, 1, "第1次短命周期开始")
	mustRelease(t, p, 1, 105, "存活5<50: q=min(2*10,25)=20")
	failAllocate(t, p, 105, 125, 1, ErrNoFree, "最早解除=125")
	failAllocate(t, p, 124, 125, 1, ErrNoFree, "124 仍不可借, 间隔恰为20")

	// 周期2: alloc@125, 存活5 -> q=min(2*20,25)=25(封顶), ready=155
	mustAllocate(t, p, 125, 1, "恰在125解除时刻分配, 两次分配间隔=20")
	mustRelease(t, p, 1, 130, "存活5: q=min(2*20,25)=25 封顶Qmax")
	failAllocate(t, p, 154, 155, 1, ErrNoFree, "154 仍隔离, 间隔=25")

	// 周期3: alloc@155, 存活5 -> q=min(2*25,25)=25(持续封顶), ready=185
	mustAllocate(t, p, 155, 1, "155 分配")
	mustRelease(t, p, 1, 160, "存活5: 仍封顶25")
	failAllocate(t, p, 184, 185, 1, ErrNoFree, "184 仍隔离")

	// 正常寿命(存活恰等于 R=50)后隔离期重置为 Q。
	mustAllocate(t, p, 185, 1, "185 分配, 开始正常寿命周期")
	mustRelease(t, p, 1, 235, "存活恰等于 R=50, 正常寿命 -> 隔离重置 Q=10, ready=245")
	failAllocate(t, p, 244, 245, 1, ErrNoFree, "重置后隔离 Q=10")
	mustAllocate(t, p, 245, 1, "245 重新分配")

	// 再次短命：从 Q 开始翻倍（而非沿用 25）。
	mustRelease(t, p, 1, 250, "再次短命(存活5): 从Q翻倍 10->20, ready=270")
	failAllocate(t, p, 269, 270, 1, ErrNoFree, "270 前不可借")
	mustAllocate(t, p, 270, 1, "270 可分配, 证明再次短命从 Q=10 翻倍而非沿用25")
}

// TestLifeExactlyR 锁定「存活时间恰等于 R」按正常寿命处理。
func TestLifeExactlyR(t *testing.T) {
	p := newPoolOrFail(t, 1, 10, 100, 50)
	mustAllocate(t, p, 0, 1, "0 时刻分配")
	mustRelease(t, p, 1, 50, "存活恰为 R=50, 正常寿命隔离 Q=10, ready=60")
	failAllocate(t, p, 59, 60, 1, ErrNoFree, "59 仍隔离")
	mustAllocate(t, p, 60, 1, "60 可分配")
	mustRelease(t, p, 1, 65, "本次存活5<50短命: 上次隔离为Q(已重置)->20, ready=85")
	failAllocate(t, p, 84, 85, 1, ErrNoFree, "85 解除")
	mustAllocate(t, p, 85, 1, "85 可分配")
}

// TestExhausted 池耗尽：全部使用中时 Exhausted=true 且无最早解除。
func TestExhausted(t *testing.T) {
	p := newPoolOrFail(t, 2, 10, 100, 50)
	mustAllocate(t, p, 0, 1, "占用1")
	mustAllocate(t, p, 0, 2, "占用2, 池全部使用中")
	id, fail, err := p.AllocateDetailed(0)
	t.Logf("输入: Allocate(0) -> id=%d failure=%+v err=%v; 判定: ErrExhausted 且 Exhausted=true 且无最早解除", id, fail, err)
	if !errors.Is(err, ErrExhausted) || !fail.Exhausted || fail.EarliestID != 0 {
		t.Fatalf("耗尽报告错误: id=%d fail=%+v err=%v", id, fail, err)
	}
}

// TestRejectionPriorityAndNoMutation 释放错误优先级与「被拒操作不改状态/隔离记录」。
func TestRejectionPriorityAndNoMutation(t *testing.T) {
	// 独立序列：时钟回拨优先于其余一切（包括越界）。
	rp := newPoolOrFail(t, 1, 10, 100, 50)
	mustAllocate(t, rp, 100, 1, "100 分配1(回拨优先级序列)")
	rejectRelease(t, rp, 999, 90, ErrClockRollback, "越界编号+更早时刻: 仍只报时钟回拨(回拨优先)")
	rejectRelease(t, rp, 1, 90, ErrClockRollback, "使用中编号+更早时刻: 仍报时钟回拨")
	snap, err := rp.Query(100)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("输入: Query(100) -> inUse=%v; 判定: 回拨被拒后编号1仍使用中, 状态未改变", snap.InUse)
	if len(snap.InUse) != 1 || snap.InUse[0] != 1 {
		t.Fatalf("回拨拒绝污染了状态: %+v", snap)
	}

	// 主序列：时刻合法时按「越界、空闲、隔离中」只报第一个。
	p := newPoolOrFail(t, 2, 10, 100, 50)
	mustAllocate(t, p, 100, 1, "100 分配编号1")
	mustRelease(t, p, 1, 110, "短命释放1, 隔离至130")

	rejectRelease(t, p, 99, 110, ErrOutOfRange, "时刻合法(110): 先报越界")
	rejectRelease(t, p, 2, 110, ErrFreeID, "编号2空闲, 报 ErrFreeID")
	rejectRelease(t, p, 1, 110, ErrQuarantinedID, "编号1隔离中, 报 ErrQuarantinedID")
	rejectRelease(t, p, 0, 110, ErrOutOfRange, "编号0越界(而非空闲)")
	rejectRelease(t, p, 3, 110, ErrOutOfRange, "编号3越界(而非空闲)")

	if _, _, err := p.AllocateDetailed(109); !errors.Is(err, ErrClockRollback) {
		t.Fatalf("分配回拨 err=%v", err)
	}
	t.Logf("输入: Allocate(109) -> 输出: err=%v; 判定: 时钟回拨拒绝", ErrClockRollback)

	_, err = p.Query(109)
	t.Logf("输入: Query(109) -> 输出: err=%v; 判定: 查询时钟回拨拒绝", err)
	if !errors.Is(err, ErrClockRollback) {
		t.Fatalf("查询回拨 err=%v", err)
	}

	mustAllocate(t, p, 121, 2, "121 时编号2空闲可分配, 说明之前拒绝未污染状态")
	failAllocate(t, p, 121, 130, 1, ErrNoFree, "编号1仍130解除, 隔离记录未受污染")

	snap2, qerr := p.Query(130)
	if qerr != nil {
		t.Fatal(qerr)
	}
	t.Logf("输入: Query(130) -> 输出: inUse=%v quarantined=%v free=%v; 判定: 1转空闲,2使用中, 三类之和=N",
		snap2.InUse, snap2.Quarantined, snap2.Free)
	if len(snap2.Free) != 1 || snap2.Free[0] != 1 || len(snap2.InUse) != 1 || snap2.InUse[0] != 2 {
		t.Fatalf("130 快照不符: %+v", snap2)
	}
	if len(snap2.InUse)+len(snap2.Quarantined)+len(snap2.Free) != 2 {
		t.Fatalf("三类编号数之和必须恒为 N")
	}
}

// TestDeterministicReplay 相同操作序列重放得到完全相同的编号序列。
func TestDeterministicReplay(t *testing.T) {
	type op struct {
		kind byte
		id   int
		now  int64
	}
	ops := []op{
		{'a', 0, 1}, {'a', 0, 2}, {'a', 0, 3},
		{'r', 1, 5}, {'r', 2, 6},
		{'a', 0, 24}, {'a', 0, 25}, {'a', 0, 26},
		{'r', 2, 30}, {'r', 3, 40},
		{'a', 0, 50}, {'a', 0, 50},
	}
	run := func() []int {
		p, err := New(3, 10, 100, 50)
		if err != nil {
			t.Fatal(err)
		}
		var seq []int
		for _, o := range ops {
			if o.kind == 'a' {
				id, err := p.Allocate(o.now)
				if err == nil {
					seq = append(seq, id)
				} else {
					seq = append(seq, -1)
				}
			} else if err := p.Release(o.id, o.now); err != nil {
				t.Fatalf("重放意外错误: %v", err)
			}
		}
		return seq
	}
	first := run()
	for k := 0; k < 3; k++ {
		again := run()
		t.Logf("输入: 重放第%d遍 -> 输出: %v; 判定: 与首遍 %v 完全一致", k+2, again, first)
		if len(again) != len(first) {
			t.Fatalf("长度不一致: %v vs %v", again, first)
		}
		for i := range first {
			if again[i] != first[i] {
				t.Fatalf("第%d遍位置%d: %d != 首遍 %d (%v vs %v)", k+2, i, again[i], first[i], again, first)
			}
		}
	}
}

// TestConcurrentSerialEquivalence 并发分配/释放/查询：状态计数恒为 N，且每个编号不被双重分配。
func TestConcurrentSerialEquivalence(t *testing.T) {
	const n = 8
	p := newPoolOrFail(t, n, 2, 16, 5)

	var clock atomic.Int64
	var wg sync.WaitGroup
	owner := make([]atomic.Int64, n+1) // owner[id] 为当前持有者序号，0 为空闲/隔离

	next := func() int64 {
		for {
			cur := clock.Load()
			next := cur + 1
			if clock.CompareAndSwap(cur, next) {
				return next
			}
		}
	}

	const workers = 8
	const iterations = 300
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func(worker int) {
			defer wg.Done()
			tag := int64(worker + 1)
			releaseOwned := func(id int) {
				t.Helper()
				for {
					err := p.Release(id, next())
					if err == nil {
						owner[id].Store(0)
						return
					}
					if !errors.Is(err, ErrClockRollback) {
						t.Errorf("并发释放 id=%d 意外错误: %v", id, err)
						owner[id].Store(0)
						return
					}
				}
			}
			for i := 0; i < iterations; i++ {
				now := next()
				switch i % 4 {
				case 0, 1:
					id, err := p.Allocate(now)
					if err != nil {
						if !errors.Is(err, ErrNoFree) && !errors.Is(err, ErrExhausted) && !errors.Is(err, ErrClockRollback) {
							t.Errorf("并发分配意外错误: %v", err)
						}
						continue
					}
					if !owner[id].CompareAndSwap(0, tag) {
						t.Errorf("编号%d 被双重分配(owner=%d)", id, owner[id].Load())
						continue
					}
					releaseOwned(id)
				case 2:
					if _, err := p.Query(now); err != nil && !errors.Is(err, ErrClockRollback) {
						t.Errorf("并发查询意外错误: %v", err)
					}
				case 3:
					id, _, aerr := p.AllocateDetailed(now)
					if aerr != nil && !errors.Is(aerr, ErrNoFree) && !errors.Is(aerr, ErrExhausted) && !errors.Is(aerr, ErrClockRollback) {
						t.Errorf("并发详细分配意外错误: %v", aerr)
					}
					if aerr == nil {
						if !owner[id].CompareAndSwap(0, tag) {
							t.Errorf("编号%d 被双重分配(owner=%d)", id, owner[id].Load())
						} else {
							releaseOwned(id)
						}
					}
				}
			}
		}(w)
	}
	wg.Wait()

	final := next()
	snap, err := p.Query(final + 1000)
	if err != nil {
		t.Fatal(err)
	}
	total := len(snap.InUse) + len(snap.Quarantined) + len(snap.Free)
	t.Logf("输入: 并发 %d 协程 x %d 轮后 Query(%d) -> inUse=%d quarantined=%d free=%d; 判定: 总数=%d 恒为 N=%d",
		workers, iterations, final+1000, len(snap.InUse), len(snap.Quarantined), len(snap.Free), total, n)
	if total != n {
		t.Fatalf("三类编号数之和 %d != N %d", total, n)
	}

	// 推进足够久后所有编号必须全部空闲。
	snap, _ = p.Query(final + 1_000_000)
	t.Logf("输入: Query(%d) -> free=%v; 判定: 隔离期过后全部编号空闲", final+1_000_000, snap.Free)
	if len(snap.Free) != n {
		t.Fatalf("远期快照 free=%d, 期望 %d", len(snap.Free), n)
	}
}
