package scan

import (
	"errors"
	"sync"
	"testing"
)

// 例三：P=2 两个分区会话可同时打开；Busy 不消耗 epoch；键分区不匹配非法。
func TestTwoPartitionsInterleaved(t *testing.T) {
	e, err := New(2, 1, 100)
	if err != nil {
		t.Fatal(err)
	}
	ep0, err := e.Begin(0, 1)
	if err != nil || ep0 != 1 {
		t.Fatalf("Begin(0)=%d,%v want 1", ep0, err)
	}
	ep1, err := e.Begin(1, 2)
	if err != nil || ep1 != 1 {
		t.Fatalf("Begin(1)=%d,%v want 1", ep1, err)
	}
	if _, err := e.Begin(0, 3); err != ErrBusy {
		t.Fatalf("re-Begin(0) err=%v want ErrBusy", err)
	}
	if e.testEpoch(0) != 1 {
		t.Fatalf("epoch(0)=%d want 1 (rejected Begin must not burn epoch)", e.testEpoch(0))
	}
	// 键 2 属于分区 0，在分区 1 的会话里报告为参数非法。
	if _, err := e.Report(1, ep1, []Row{{2, 0}}, 3); err != ErrInvalidParam {
		t.Fatalf("wrong-part key err=%v want ErrInvalidParam", err)
	}
	// 合法键：键 1 属于分区 1。
	if _, err := e.Report(1, ep1, []Row{{1, 7}}, 4); err != nil {
		t.Fatalf("Report part1 key1: %v", err)
	}
	if r, err := e.End(0, ep0, true, 5); err != nil || r.Deleted != 0 || r.Tripped {
		t.Fatalf("End(0)=%+v,%v", r, err)
	}
	if r, err := e.End(1, ep1, true, 6); err != nil || r.Deleted != 0 {
		t.Fatalf("End(1)=%+v,%v", r, err)
	}
	if v, ok := e.testGet(1); !ok || v != 7 {
		t.Fatalf("T[1]=%d,%v want 7,true", v, ok)
	}
	// 空分区 End：删除 0、不熔断。
	empty, _ := New(1, 1, 50)
	ep, _ := empty.Begin(0, 1)
	if r, err := empty.End(0, ep, true, 1); err != nil || r.Deleted != 0 || r.Tripped {
		t.Fatalf("empty End=%+v,%v", r, err)
	}
}

type snapshot struct {
	maxNow  int64
	epoch   []int
	suspect []bool
	open    []bool
	live    []map[int64]int64
	absent  []map[int64]int
}

func (e *Engine) snapshot() snapshot {
	snap := snapshot{maxNow: e.testMaxNow()}
	for _, s := range e.shards {
		s.mu.Lock()
	}
	snap.epoch = make([]int, e.partCount)
	snap.suspect = make([]bool, e.partCount)
	snap.open = make([]bool, e.partCount)
	snap.live = make([]map[int64]int64, e.partCount)
	snap.absent = make([]map[int64]int, e.partCount)
	for i, s := range e.shards {
		snap.epoch[i] = s.epoch
		snap.suspect[i] = s.cb.Suspected()
		snap.open[i] = s.current != nil
		live, ab := s.tab.Dump()
		snap.live[i] = live
		snap.absent[i] = ab
	}
	for _, s := range e.shards {
		s.mu.Unlock()
	}
	return snap
}

func reflectDeep(x, y snapshot) bool {
	if x.maxNow != y.maxNow {
		return false
	}
	for i := range x.epoch {
		if x.epoch[i] != y.epoch[i] || x.suspect[i] != y.suspect[i] || x.open[i] != y.open[i] {
			return false
		}
		if len(x.live[i]) != len(y.live[i]) || len(x.absent[i]) != len(y.absent[i]) {
			return false
		}
		for k, v := range x.live[i] {
			if y.live[i][k] != v {
				return false
			}
		}
		for k, a := range x.absent[i] {
			if y.absent[i][k] != a {
				return false
			}
		}
	}
	return true
}

// 被拒操作不得改变 T、a、epoch、Suspect 与最大 now（被拒 Begin 不消耗 epoch）。
func TestRejectionsDoNotMutate(t *testing.T) {
	e, _ := New(2, 2, 50)
	seedScan(t, e, map[int64]int64{1: 1, 2: 2, 3: 3}, 1)

	mustReject := func(name string, want error, fn func() error) {
		t.Helper()
		before := e.snapshot()
		err := fn()
		if !errors.Is(err, want) {
			t.Fatalf("%s: err=%v want %v", name, err, want)
		}
		after := e.snapshot()
		if !reflectDeep(before, after) {
			t.Fatalf("%s mutated state:\nbefore=%+v\nafter =%+v", name, before, after)
		}
	}

	// 参数非法（拒绝次序最前）。
	mustReject("bad-part Begin", ErrInvalidParam, func() error {
		_, err := e.Begin(2, 5)
		return err
	})
	mustReject("neg-part End", ErrInvalidParam, func() error {
		_, err := e.End(-1, 1, true, 5)
		return err
	})
	// 先合法打开分区 1（now=5），随后在其会话上做非法调用。
	if ep, err := e.Begin(1, 5); err != nil {
		t.Fatalf("setup Begin(1): %v", err)
	} else if ep == 0 {
		t.Fatal("bad setup epoch")
	}
	mustReject("mismatch-key", ErrInvalidParam, func() error {
		_, err := e.Report(1, 99, []Row{{2, 1}}, 7)
		return err
	})
	mustReject("dup-rows", ErrInvalidParam, func() error {
		_, err := e.Report(1, 99, []Row{{1, 1}, {1, 9}}, 8)
		return err
	})
	mustReject("key-out-of-range", ErrInvalidParam, func() error {
		_, err := e.Report(1, 99, []Row{{1_000_000_001, 1}}, 9)
		return err
	})

	// 时钟回退（此时最大 now=8 以上被拒不更新；已接受最大为 4 seed / 5 Begin）。
	mustReject("clock-rollback", ErrClockRollback, func() error {
		_, err := e.Begin(0, 2)
		return err
	})

	// 状态类：分区 1 已有会话 -> Busy；错误 epoch -> NoSession（分区 0 空闲）。
	mustReject("busy", ErrBusy, func() error {
		_, err := e.Begin(1, 10)
		return err
	})
	mustReject("no-session", ErrNoSession, func() error {
		_, err := e.End(0, 77, true, 10)
		return err
	})

	// Approve：权限不足优先；非 Suspect 报 NotSuspect（用更大的 now 避免回退干扰）。
	mustReject("approve-role", ErrUnauthorized, func() error {
		_, err := e.Approve(1, 0, 20)
		return err
	})
	mustReject("approve-not-suspect", ErrNotSuspect, func() error {
		_, err := e.Approve(2, 0, 20)
		return err
	})
}

// 并发：不同分区会话交错，单调时钟下结果稳定，-race 下无竞争。
// 真实系统 now 来自单调时钟源：测试用 token 把"取时间戳->执行"原子化，
// 调用仍由多 goroutine 竞争（交错顺序不定），但 now 与接受序一致。
func TestConcurrentPartitions(t *testing.T) {
	e, _ := New(4, 2, 100)
	type ticket struct{ now int64 }
	ch := make(chan ticket, 1)
	ch <- ticket{now: 0}
	withNow := func(fn func(now int64) error) error {
		tk := <-ch
		tk.now++
		err := fn(tk.now)
		ch <- tk
		return err
	}
	var wg sync.WaitGroup
	for part := 0; part < 4; part++ {
		wg.Add(1)
		go func(part int) {
			defer wg.Done()
			for r := 0; r < 50; r++ {
				var ep int
				if err := withNow(func(now int64) error {
					var err error
					ep, err = e.Begin(part, now)
					return err
				}); err != nil {
					t.Errorf("Begin(%d): %v", part, err)
					return
				}
				var rows []Row
				for k := int64(part); k <= 40; k += 4 {
					if k == 0 {
						continue // 键范围从 1 开始
					}
					rows = append(rows, Row{K: k, V: k})
				}
				if err := withNow(func(now int64) error {
					_, err := e.Report(part, ep, rows, now)
					return err
				}); err != nil {
					t.Errorf("Report(%d): %v", part, err)
					return
				}
				if err := withNow(func(now int64) error {
					_, err := e.End(part, ep, true, now)
					return err
				}); err != nil {
					t.Errorf("End(%d): %v", part, err)
					return
				}
			}
		}(part)
	}
	wg.Wait()
	// 每分区 10 个键，完整反复扫描永不缺席删除。
	for part := 0; part < 4; part++ {
		if n := e.testLive(part); n != 10 {
			t.Fatalf("part %d live=%d want 10", part, n)
		}
		if e.testSuspect(part) {
			t.Fatalf("part %d unexpectedly Suspect", part)
		}
	}
}
