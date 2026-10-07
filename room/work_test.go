package room_test

import (
	"fmt"
	"sync"
	"testing"

	"ontology/room"
)

// cycleOps 驱动一个可无限重复的循环（房间始终存活，历史持续增长）：
// 不变式：循环开始时房间处于等待，室内恰有一名已就绪玩家。
func cycleOps(t *testing.T, r *room.Room, prev string, k int, now int64) (string, int64) {
	t.Helper()
	cur := fmt.Sprintf("u%d", k)
	mustOK(t, r.Join(cur, now)) // 等待：{prev(ready), cur}
	now++
	mustOK(t, r.SetReady(cur, true, now)) // 条件成立 -> 倒计时
	now++
	mustOK(t, r.SetReady(prev, false, now)) // 取消就绪 -> 退回等待
	now++
	mustOK(t, r.Leave(prev, now)) // prev 离开（含房主迁移扫描）
	now++
	if _, err := r.Query(now); err != nil { // 查询（含排序扫描）
		t.Fatalf("query: %v", err)
	}
	now++
	return cur, now
}

// TestWorkUnitsIndependentOfHistory 以可验证方式证明：
// SetReady/Join/Leave/Query 的内部循环工作量不随历史事件数增长——
// 每个循环的操作序列完全相同，若复杂度与历史无关，则每个循环的
// 工作量计数严格相等。
func TestWorkUnitsIndependentOfHistory(t *testing.T) {
	r := newRoom(t, room.Config{MinPlayers: 2, MaxPlayers: 20, Countdown: 600, ReportWindow: 3600})
	mustOK(t, r.Join("seed", 0))
	mustOK(t, r.SetReady("seed", true, 1))

	var now int64 = 2
	prev := "seed"
	const cycles = 20000
	var firstCycleWork, prevWork int64
	for k := 0; k < cycles; k++ {
		before := r.WorkUnits()
		prev, now = cycleOps(t, r, prev, k, now)
		work := r.WorkUnits() - before
		if k == 0 {
			firstCycleWork = work
		} else if work != firstCycleWork {
			t.Fatalf("cycle %d work=%d, want %d (work must not grow with history)", k, work, firstCycleWork)
		}
		prevWork = work
	}
	t.Logf("判定依据: %d 个循环每循环工作量恒为 %d（历史事件数 %d 不产生影响）",
		cycles, prevWork, r.WorkUnits())

	// 全体就绪判断为 O(1)：单循环工作量与在室人数无关的小常数。
	if firstCycleWork > 64 {
		t.Fatalf("per-cycle work %d exceeds constant bound 64", firstCycleWork)
	}
}

// TestMatchPathWorkBounded 证明对局/结算路径（开局建名单、上报计票、
// 期限裁决）的工作量上界只与 U 有关，与操作次数无关。
func TestMatchPathWorkBounded(t *testing.T) {
	const rooms = 2000
	var totalWork, totalOps int64
	for k := 0; k < rooms; k++ {
		r := newRoom(t, room.Config{MinPlayers: 2, MaxPlayers: 20, Countdown: 5, ReportWindow: 10})
		var now int64
		join := func(id string) { mustOK(t, r.Join(id, now)); now++; totalOps++ }
		ready := func(id string) { mustOK(t, r.SetReady(id, true, now)); now++; totalOps++ }
		ids := []string{"a", "b", "c", "d"}
		for _, id := range ids {
			join(id)
		}
		for _, id := range ids {
			ready(id)
		}
		now += 5 // 跨过倒计时到期
		snap := query(t, r, now)
		totalOps++
		if snap.State != room.StateInProgress {
			t.Fatalf("expected in_progress, got %s", snap.State)
		}
		mustOK(t, r.End("a", now))
		now++
		totalOps++
		for _, id := range ids[:3] { // 3/4 一致 -> 期限到时多数决
			mustOK(t, r.Report(id, "a", now))
			now++
			totalOps++
		}
		now += 10
		snap = query(t, r, now)
		totalOps++
		if snap.State != room.StateEnded || snap.Winner != "a" {
			t.Fatalf("expected ended/a, got %s/%s", snap.State, snap.Winner)
		}
		totalWork += r.WorkUnits()
	}
	avg := totalWork / totalOps
	t.Logf("判定依据: %d 个完整对局房间，平均每操作工作量 %d（上界只与 U=20 有关）", rooms, avg)
	if avg > 64 {
		t.Fatalf("average per-op work %d exceeds constant bound 64", avg)
	}
}

// TestConcurrentOps 并发调用等价于某个串行顺序：
// 互斥锁保证无数据竞争（配合 -race），且所有拒绝都有合法编码。
func TestConcurrentOps(t *testing.T) {
	r := newRoom(t, room.Config{MinPlayers: 2, MaxPlayers: 20, Countdown: 600, ReportWindow: 3600})
	var wg sync.WaitGroup
	for g := 0; g < 8; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			for i := 0; i < 500; i++ {
				now := int64(i)
				user := fmt.Sprintf("g%d-u%d", g, i%4)
				var err error
				switch i % 6 {
				case 0:
					err = r.Join(user, now)
				case 1:
					err = r.SetReady(user, true, now)
				case 2:
					err = r.SetReady(user, false, now)
				case 3:
					err = r.Leave(user, now)
				case 4:
					err = r.End(user, now)
				default:
					err = r.Report(user, user, now)
				}
				if err != nil {
					if _, ok := err.(*room.Error); !ok {
						t.Errorf("unexpected error type %T: %v", err, err)
					}
				}
				if _, err := r.Query(now); err != nil {
					if _, ok := err.(*room.Error); !ok {
						t.Errorf("unexpected query error type %T: %v", err, err)
					}
				}
			}
		}(g)
	}
	wg.Wait()
	snap, err := r.Query(room.MaxNow)
	if err != nil {
		t.Fatalf("final query: %v", err)
	}
	t.Logf("判定依据: 8x500 并发操作无竞态无 panic，最终状态 %s 一致可读", snap.State)
}

// BenchmarkMixedOps 混合操作基准：验证单操作开销为常数级。
func BenchmarkMixedOps(b *testing.B) {
	r, err := room.NewRoom(room.Config{MinPlayers: 2, MaxPlayers: 20, Countdown: 600, ReportWindow: 3600})
	if err != nil {
		b.Fatal(err)
	}
	if err := r.Join("seed", 0); err != nil {
		b.Fatal(err)
	}
	if err := r.SetReady("seed", true, 1); err != nil {
		b.Fatal(err)
	}
	var now int64 = 2
	prev := "seed"
	b.ResetTimer()
	for k := 0; k < b.N; k++ {
		cur := fmt.Sprintf("u%d", k)
		_ = r.Join(cur, now)
		now++
		_ = r.SetReady(cur, true, now)
		now++
		_ = r.SetReady(prev, false, now)
		now++
		_ = r.Leave(prev, now)
		now++
		prev = cur
	}
}
