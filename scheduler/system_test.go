package scheduler

import (
	"fmt"
	"sync"
	"testing"
)

func testConfig() Config {
	return Config{DispatchLead: 30, CutoffLead: 60, EarliestLead: 100, LatestLead: 2000, MaxShiftSpan: 300}
}

func newTestSystem(t *testing.T) *System {
	t.Helper()
	sys, err := NewSystem(testConfig())
	if err != nil {
		t.Fatalf("NewSystem: %v", err)
	}
	return sys
}

func mustAddRegion(t *testing.T, sys *System, now int64, id string) {
	t.Helper()
	if err := sys.AddRegion(now, id); err != nil {
		t.Fatalf("AddRegion(%s): %v", id, err)
	}
}

func mustAddSlot(t *testing.T, sys *System, now int64, region string, start, end int64, cap int) {
	t.Helper()
	if err := sys.AddSlot(now, region, start, end, cap); err != nil {
		t.Fatalf("AddSlot(%s,%d): %v", region, start, err)
	}
}

func mustPlace(t *testing.T, sys *System, now int64, resID, region string, slotStart int64, shift bool) PlaceResult {
	t.Helper()
	res, err := sys.Place(now, resID, region, slotStart, shift)
	if err != nil {
		t.Fatalf("Place(%s): %v", resID, err)
	}
	return res
}

func wantCode(t *testing.T, what string, err error, code Code) {
	t.Helper()
	if got := CodeOf(err); got != code {
		t.Fatalf("%s: got code %d (%v), want %d", what, got, err, code)
	}
}

func slotView(t *testing.T, sys *System, now int64, region string, start int64) SlotView {
	t.Helper()
	views, err := sys.ListSlots(now, region, 0, 1<<62)
	if err != nil {
		t.Fatalf("ListSlots: %v", err)
	}
	for _, v := range views {
		if v.Start == start {
			return v
		}
	}
	t.Fatalf("slot %d not found in region %s", start, region)
	return SlotView{}
}

// 最早与最晚可预约提前量两端取等均允许，越界一端分别报过早/过晚。
func TestLeadBoundariesInclusive(t *testing.T) {
	sys, err := NewSystem(Config{DispatchLead: 30, CutoffLead: 60, EarliestLead: 100, LatestLead: 1000, MaxShiftSpan: 300})
	if err != nil {
		t.Fatalf("NewSystem: %v", err)
	}
	mustAddRegion(t, sys, 0, "R")
	mustAddSlot(t, sys, 0, "R", 99, 100, 1)
	mustAddSlot(t, sys, 0, "R", 100, 101, 1)
	mustAddSlot(t, sys, 0, "R", 1000, 1001, 1)
	mustAddSlot(t, sys, 0, "R", 1001, 1002, 1)

	if _, err := sys.Place(0, "r-early", "R", 100, false); err != nil {
		t.Fatalf("d == EarliestLead should be allowed: %v", err)
	}
	if _, err := sys.Place(0, "r-late", "R", 1000, false); err != nil {
		t.Fatalf("d == LatestLead should be allowed: %v", err)
	}
	_, err = sys.Place(0, "r-too-early", "R", 99, false)
	wantCode(t, "d == EarliestLead-1", err, CodeTooEarly)
	_, err = sys.Place(0, "r-too-late", "R", 1001, false)
	wantCode(t, "d == LatestLead+1", err, CodeTooLate)
}

// 改期截止：恰等于 时段起点-截止提前量 不允许，早一秒允许。
func TestRescheduleCutoffExact(t *testing.T) {
	sys := newTestSystem(t)
	mustAddRegion(t, sys, 0, "R")
	mustAddSlot(t, sys, 0, "R", 1000, 1300, 2)
	mustAddSlot(t, sys, 0, "R", 1500, 1800, 2)
	mustPlace(t, sys, 0, "r1", "R", 1000, false)
	mustPlace(t, sys, 0, "r2", "R", 1000, false)

	// 截止时刻 = 1000 - 60 = 940
	if err := sys.Reschedule(939, "r1", "R", 1500); err != nil {
		t.Fatalf("one second before cutoff should be allowed: %v", err)
	}
	err := sys.Reschedule(940, "r2", "R", 1500)
	wantCode(t, "exactly at cutoff", err, CodePastCutoff)
}

// 释放时刻恰等即释放，且无需任何操作触碰；释放后预约仍占名额。
func TestReleaseExactAndLazy(t *testing.T) {
	sys := newTestSystem(t)
	mustAddRegion(t, sys, 0, "R")
	mustAddSlot(t, sys, 0, "R", 1000, 1300, 2)
	mustPlace(t, sys, 0, "r1", "R", 1000, false)
	mustPlace(t, sys, 0, "r2", "R", 1000, false)

	// 释放时刻 = 1000 - 30 = 970
	v, err := sys.GetReservation(969, "r1")
	if err != nil || v.Released {
		t.Fatalf("at 969 should not be released: %+v %v", v, err)
	}
	err = sys.Deliver(969, "r1")
	wantCode(t, "deliver before release", err, CodeNotReleased)

	// 中间没有任何操作触碰 r1，恰等释放时刻即视为已释放
	v, err = sys.GetReservation(970, "r1")
	if err != nil || !v.Released {
		t.Fatalf("at 970 should be released: %+v %v", v, err)
	}
	if err := sys.Deliver(970, "r1"); err != nil {
		t.Fatalf("deliver exactly at release time: %v", err)
	}

	// 释放后仍占名额：cap=2，r1 已送达释放名额，r2 仍占 1 个
	if got := slotView(t, sys, 970, "R", 1000).Occupied; got != 1 {
		t.Fatalf("occupied after deliver = %d, want 1", got)
	}
	// 已释放不可改期
	mustAddSlot(t, sys, 970, "R", 1500, 1800, 1)
	err = sys.Reschedule(970, "r2", "R", 1500)
	wantCode(t, "reschedule after release", err, CodeAlreadyReleased)
}

// 顺延跨度恰等允许；超过跨度则报时段已满。
func TestShiftSpanExact(t *testing.T) {
	sys := newTestSystem(t)
	mustAddRegion(t, sys, 0, "R")
	mustAddSlot(t, sys, 0, "R", 1000, 1300, 1) // 将被占满
	mustAddSlot(t, sys, 0, "R", 1300, 1600, 1) // 跨度恰为 300 == MaxShiftSpan
	mustPlace(t, sys, 0, "full", "R", 1000, false)

	res := mustPlace(t, sys, 0, "r1", "R", 1000, true)
	if !res.Shifted || res.SlotStart != 1300 || res.OrigSlotStart != 1000 {
		t.Fatalf("shift at exact span: %+v", res)
	}

	// 候选在跨度之外（301 > 300）则顺延失败
	sys2 := newTestSystem(t)
	mustAddRegion(t, sys2, 0, "R")
	mustAddSlot(t, sys2, 0, "R", 1000, 1300, 1)
	mustAddSlot(t, sys2, 0, "R", 1301, 1601, 1)
	mustPlace(t, sys2, 0, "full", "R", 1000, false)
	_, err := sys2.Place(0, "r1", "R", 1000, true)
	wantCode(t, "candidate beyond span", err, CodeSlotFull)
}

// 顺延只向后不向前：前方有余量的时段不作候选。
func TestShiftOnlyBackward(t *testing.T) {
	sys := newTestSystem(t)
	mustAddRegion(t, sys, 0, "R")
	mustAddSlot(t, sys, 0, "R", 700, 1000, 1)  // 前方有空余，不可选
	mustAddSlot(t, sys, 0, "R", 1000, 1300, 1) // 将被占满
	mustAddSlot(t, sys, 0, "R", 1300, 1600, 1) // 后方候选
	mustPlace(t, sys, 0, "full", "R", 1000, false)

	res := mustPlace(t, sys, 0, "r1", "R", 1000, true)
	if res.SlotStart != 1300 {
		t.Fatalf("shift must go backward in time line (later slot): %+v", res)
	}

	// 没有后方候选时，前方空余时段不被考虑
	sys2 := newTestSystem(t)
	mustAddRegion(t, sys2, 0, "R")
	mustAddSlot(t, sys2, 0, "R", 700, 1000, 1)
	mustAddSlot(t, sys2, 0, "R", 1000, 1300, 1)
	mustPlace(t, sys2, 0, "full", "R", 1000, false)
	_, err := sys2.Place(0, "r1", "R", 1000, true)
	wantCode(t, "forward slot not a candidate", err, CodeSlotFull)
}

// 超额时段不参与顺延候选。
func TestOversoldSlotNotCandidate(t *testing.T) {
	sys := newTestSystem(t)
	mustAddRegion(t, sys, 0, "R")
	mustAddSlot(t, sys, 0, "R", 1000, 1200, 1) // 将被占满
	mustAddSlot(t, sys, 0, "R", 1200, 1300, 2) // 将被调成超额
	mustAddSlot(t, sys, 0, "R", 1300, 1600, 1) // 最终落位
	mustPlace(t, sys, 0, "full", "R", 1000, false)
	mustPlace(t, sys, 0, "b1", "R", 1200, false)
	mustPlace(t, sys, 0, "b2", "R", 1200, false)
	if err := sys.SetCapacity(0, "R", 1200, 1); err != nil {
		t.Fatalf("SetCapacity: %v", err)
	}
	if v := slotView(t, sys, 0, "R", 1200); !v.Oversold {
		t.Fatalf("slot 1200 should be oversold: %+v", v)
	}

	res := mustPlace(t, sys, 0, "r1", "R", 1000, true)
	if res.SlotStart != 1300 {
		t.Fatalf("oversold slot must be skipped: %+v", res)
	}
}

// 调低上限进入超额：新预约一律报满；已占回落到不超过上限后恢复。
func TestOversoldLowerAndRecover(t *testing.T) {
	sys := newTestSystem(t)
	mustAddRegion(t, sys, 0, "R")
	mustAddSlot(t, sys, 0, "R", 1000, 1300, 2)
	mustPlace(t, sys, 0, "r1", "R", 1000, false)
	mustPlace(t, sys, 0, "r2", "R", 1000, false)

	if err := sys.SetCapacity(0, "R", 1000, 1); err != nil {
		t.Fatalf("SetCapacity: %v", err)
	}
	if v := slotView(t, sys, 0, "R", 1000); !v.Oversold || v.Occupied != 2 || v.Cap != 1 {
		t.Fatalf("oversold view: %+v", v)
	}
	_, err := sys.Place(0, "r3", "R", 1000, false)
	wantCode(t, "place into oversold slot", err, CodeSlotFull)

	// 取消一笔后已占回落到等于上限：超额解除，但仍为满员
	if err := sys.Cancel(0, "r1"); err != nil {
		t.Fatalf("Cancel: %v", err)
	}
	if v := slotView(t, sys, 0, "R", 1000); v.Oversold || v.Occupied != 1 {
		t.Fatalf("recovered view: %+v", v)
	}
	if _, err := sys.Place(0, "r3", "R", 1000, false); CodeOf(err) != CodeSlotFull {
		t.Fatalf("occupied == cap is full, not oversold: %v", err)
	}
	// 再取消一笔后真正有余量
	if err := sys.Cancel(0, "r2"); err != nil {
		t.Fatalf("Cancel: %v", err)
	}
	if _, err := sys.Place(0, "r3", "R", 1000, false); err != nil {
		t.Fatalf("place after recovery: %v", err)
	}
}

// 改期到满时段被拒且原名额保持；改期成功时名额原子迁移。
func TestRescheduleFailKeepsQuota(t *testing.T) {
	sys := newTestSystem(t)
	mustAddRegion(t, sys, 0, "R")
	mustAddSlot(t, sys, 0, "R", 1000, 1300, 1)
	mustAddSlot(t, sys, 0, "R", 1300, 1600, 1)
	mustAddSlot(t, sys, 0, "R", 1600, 1900, 1)
	mustPlace(t, sys, 0, "r1", "R", 1000, false)
	mustPlace(t, sys, 0, "r2", "R", 1300, false)

	err := sys.Reschedule(0, "r1", "R", 1300)
	wantCode(t, "reschedule to full slot", err, CodeSlotFull)
	if v := slotView(t, sys, 0, "R", 1000); v.Occupied != 1 {
		t.Fatalf("origin quota must be kept: %+v", v)
	}
	if v := slotView(t, sys, 0, "R", 1300); v.Occupied != 1 {
		t.Fatalf("target quota unchanged: %+v", v)
	}
	v, _ := sys.GetReservation(0, "r1")
	if v.SlotStart != 1000 {
		t.Fatalf("reservation must stay in origin slot: %+v", v)
	}

	if err := sys.Reschedule(0, "r1", "R", 1600); err != nil {
		t.Fatalf("reschedule to free slot: %v", err)
	}
	if got := slotView(t, sys, 0, "R", 1000).Occupied; got != 0 {
		t.Fatalf("origin released: %d", got)
	}
	if got := slotView(t, sys, 0, "R", 1600).Occupied; got != 1 {
		t.Fatalf("target occupied: %d", got)
	}
}

// 释放后取消须记录标志；释放前取消不记录。
func TestPostReleaseCancelRecorded(t *testing.T) {
	sys := newTestSystem(t)
	mustAddRegion(t, sys, 0, "R")
	mustAddSlot(t, sys, 0, "R", 1000, 1300, 2)
	mustPlace(t, sys, 0, "r1", "R", 1000, false)
	mustPlace(t, sys, 0, "r2", "R", 1000, false)

	if err := sys.Cancel(500, "r1"); err != nil {
		t.Fatalf("cancel before release: %v", err)
	}
	v, _ := sys.GetReservation(500, "r1")
	if !v.Canceled || v.CanceledAfterRelease {
		t.Fatalf("pre-release cancel flags: %+v", v)
	}

	if err := sys.Cancel(970, "r2"); err != nil {
		t.Fatalf("cancel after release: %v", err)
	}
	v, _ = sys.GetReservation(970, "r2")
	if !v.Canceled || !v.CanceledAfterRelease {
		t.Fatalf("post-release cancel flags: %+v", v)
	}
	if got := slotView(t, sys, 970, "R", 1000).Occupied; got != 0 {
		t.Fatalf("occupied after cancels: %d", got)
	}
}

// 并发下单不突破上限。
func TestConcurrentPlaceDoesNotExceedCap(t *testing.T) {
	sys := newTestSystem(t)
	mustAddRegion(t, sys, 0, "R")
	mustAddSlot(t, sys, 0, "R", 1000, 1300, 8)

	const goroutines = 64
	var wg sync.WaitGroup
	results := make(chan error, goroutines)
	for i := 0; i < goroutines; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			_, err := sys.Place(0, fmt.Sprintf("res-%d", i), "R", 1000, false)
			results <- err
		}(i)
	}
	wg.Wait()
	close(results)

	successes := 0
	for err := range results {
		if err == nil {
			successes++
		} else {
			wantCode(t, "concurrent place", err, CodeSlotFull)
		}
	}
	if successes != 8 {
		t.Fatalf("successes = %d, want 8", successes)
	}
	if got := slotView(t, sys, 0, "R", 1000).Occupied; got != 8 {
		t.Fatalf("occupied = %d, want 8", got)
	}
}

// 拒绝次序与各类可程序化区分的错误。
func TestRejectionOrderAndMisc(t *testing.T) {
	if _, err := NewSystem(Config{EarliestLead: 100, LatestLead: 50}); CodeOf(err) != CodeInvalidParam {
		t.Fatalf("invalid config: %v", err)
	}

	sys := newTestSystem(t)
	mustAddRegion(t, sys, 0, "R")
	mustAddSlot(t, sys, 0, "R", 1000, 1300, 1)
	mustPlace(t, sys, 100, "r1", "R", 1000, false)

	// 时钟回退优先于对象不存在
	_, err := sys.Place(99, "x", "NOPE", 1000, false)
	wantCode(t, "rollback before region-not-found", err, CodeClockRollback)
	_, err = sys.GetReservation(99, "r1")
	wantCode(t, "query rollback", err, CodeClockRollback)
	if err := sys.Cancel(99, "r1"); err == nil {
		t.Fatal("cancel with rollback should fail")
	}

	// 对象不存在三类可区分
	_, err = sys.Place(100, "x", "NOPE", 1000, false)
	wantCode(t, "region not found", err, CodeRegionNotFound)
	_, err = sys.Place(100, "x", "R", 2000, false)
	wantCode(t, "slot not found", err, CodeSlotNotFound)
	err = sys.Cancel(100, "ghost")
	wantCode(t, "reservation not found", err, CodeReservationNotFound)

	// 改期到原时段报无需改期
	err = sys.Reschedule(100, "r1", "R", 1000)
	wantCode(t, "reschedule to same slot", err, CodeNoChange)

	// 重复取消 / 送达后取消 / 取消后送达
	if err := sys.Cancel(100, "r1"); err != nil {
		t.Fatalf("cancel: %v", err)
	}
	err = sys.Cancel(100, "r1")
	wantCode(t, "double cancel", err, CodeAlreadyCanceled)
	err = sys.Deliver(100, "r1")
	wantCode(t, "deliver canceled", err, CodeAlreadyCanceled)

	mustPlace(t, sys, 100, "r2", "R", 1000, false)
	if err := sys.Deliver(970, "r2"); err != nil {
		t.Fatalf("deliver: %v", err)
	}
	err = sys.Cancel(970, "r2")
	wantCode(t, "cancel delivered", err, CodeAlreadyDelivered)

	// 被拒绝的操作不推进时钟：r2 送达于 970，之后 969 仍应报时钟回退而非更晚错误
	err = sys.Cancel(969, "r2")
	wantCode(t, "rollback after accepted op", err, CodeClockRollback)
}

// 顺延候选搜索开销不随区域预约总数增长（配合 -benchtime 对比两个规模）。
func BenchmarkPlaceWithShift(b *testing.B) {
	for _, n := range []int{1_000, 100_000} {
		b.Run(fmt.Sprintf("reservations=%d", n), func(b *testing.B) {
			cfg := Config{DispatchLead: 30, CutoffLead: 60, EarliestLead: 100, LatestLead: 1 << 40, MaxShiftSpan: 300}
			sys, _ := NewSystem(cfg)
			_ = sys.AddRegion(0, "R")
			_ = sys.AddSlot(0, "R", 1000, 1100, 0) // 恒满，触发顺延
			_ = sys.AddSlot(0, "R", 1100, 1200, 1<<30)
			// 在更晚的时段里堆积 n 笔预约，干扰候选搜索
			_ = sys.AddSlot(0, "R", 1200, 1300, 1<<30)
			for i := 0; i < n; i++ {
				_, _ = sys.Place(0, fmt.Sprintf("fill-%d", i), "R", 1200, false)
			}
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				_, _ = sys.Place(0, fmt.Sprintf("bench-%d", i), "R", 1000, true)
			}
		})
	}
}

// 释放判定开销不随预约总数增长。
func BenchmarkGetReservationReleased(b *testing.B) {
	for _, n := range []int{1_000, 100_000} {
		b.Run(fmt.Sprintf("reservations=%d", n), func(b *testing.B) {
			cfg := Config{DispatchLead: 30, CutoffLead: 60, EarliestLead: 100, LatestLead: 1 << 40, MaxShiftSpan: 300}
			sys, _ := NewSystem(cfg)
			_ = sys.AddRegion(0, "R")
			_ = sys.AddSlot(0, "R", 1000, 1100, 1<<30)
			for i := 0; i < n; i++ {
				_, _ = sys.Place(0, fmt.Sprintf("fill-%d", i), "R", 1000, false)
			}
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				_, _ = sys.GetReservation(2000, "fill-0")
			}
		})
	}
}
