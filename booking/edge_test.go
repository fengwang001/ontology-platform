package booking

import (
	"strconv"
	"sync"
	"testing"
)

func testConfig() Config {
	return Config{
		DispatchLead:    300,
		RescheduleLead:  600,
		MinBookAhead:    60,
		MaxBookAhead:    7200,
		MaxPostponeSpan: 1800,
	}
}

func mustRegion(t *testing.T, s *Scheduler, name string) {
	t.Helper()
	if err := s.AddRegion(name); err != nil {
		t.Fatalf("AddRegion: %v", err)
	}
}

func mustSlot(t *testing.T, s *Scheduler, region string, start, end int64, capc int) {
	t.Helper()
	if err := s.AddSlot(region, start, end, capc); err != nil {
		t.Fatalf("AddSlot: %v", err)
	}
}

func wantCode(t *testing.T, err error, code ErrorCode, ctx string) {
	t.Helper()
	if CodeOf(err) != code {
		t.Fatalf("%s: want code %d got %v", ctx, code, err)
	}
}

// 最早与最晚提前量两端取等均允许，越界一秒报错。
func TestLeadBoundsEquality(t *testing.T) {
	s, _ := New(testConfig())
	mustRegion(t, s, "R")
	now := int64(1000)
	earliest := now + 60
	latest := now + 7200
	mustSlot(t, s, "R", earliest, earliest+30, 1)
	mustSlot(t, s, "R", latest, latest+30, 1)
	mustSlot(t, s, "R", earliest-60, earliest-30, 1)
	if _, err := s.Place(now, PlaceRequest{OrderID: "o1", Region: "R", SlotStart: earliest}); err != nil {
		t.Fatalf("earliest equality: %v", err)
	}
	if _, err := s.Place(now, PlaceRequest{OrderID: "o2", Region: "R", SlotStart: latest}); err != nil {
		t.Fatalf("latest equality: %v", err)
	}
	_, err := s.Place(now, PlaceRequest{OrderID: "o3", Region: "R", SlotStart: earliest - 60})
	wantCode(t, err, ErrTooEarly, "one second too early")
}

// 改期截止恰等不允许；前一秒允许。
func TestRescheduleDeadlineEquality(t *testing.T) {
	s, _ := New(testConfig())
	mustRegion(t, s, "R")
	start := int64(2000)
	mustSlot(t, s, "R", start, start+30, 2)
	mustSlot(t, s, "R", start+100, start+130, 2)
	if _, err := s.Place(500, PlaceRequest{OrderID: "o", Region: "R", SlotStart: start}); err != nil {
		t.Fatal(err)
	}
	wantCode(t, s.Reschedule(1400, "o", "R", start+100), ErrRescheduleDeadlinePassed, "deadline equality")
	if err := s.Reschedule(1399, "o", "R", start+100); err != nil {
		t.Fatalf("one second before deadline should pass: %v", err)
	}
}

// 释放时刻恰等即释放，且由查询（时刻的函数）观察，无需中间操作触碰。
func TestReleaseAtExactMoment(t *testing.T) {
	s, _ := New(testConfig())
	mustRegion(t, s, "R")
	start := int64(2000)
	mustSlot(t, s, "R", start, start+30, 1)
	if _, err := s.Place(1000, PlaceRequest{OrderID: "o", Region: "R", SlotStart: start}); err != nil {
		t.Fatal(err)
	}
	info, err := s.QueryOrder(1699, "o")
	if err != nil || info.Released {
		t.Fatalf("1699 not released: %+v err=%v", info, err)
	}
	info, err = s.QueryOrder(1700, "o")
	if err != nil || !info.Released {
		t.Fatalf("1700 must be released: %+v err=%v", info, err)
	}
	wantCode(t, s.Reschedule(1700, "o", "R", start), ErrAlreadyReleased, "reschedule after release")
}

// 顺延跨度恰等允许；只向后不向前；用户拒绝顺延报满。
func TestPostponeRules(t *testing.T) {
	s, _ := New(testConfig())
	mustRegion(t, s, "R")
	now := int64(0)
	base := int64(3600)
	mustSlot(t, s, "R", base, base+30, 1)
	mustSlot(t, s, "R", base+600, base+630, 0) // 满，须被跳过
	mustSlot(t, s, "R", base+1800, base+1830, 1)
	if _, err := s.Place(now, PlaceRequest{OrderID: "occ", Region: "R", SlotStart: base}); err != nil {
		t.Fatal(err)
	}
	res, err := s.Place(now, PlaceRequest{OrderID: "p", Region: "R", SlotStart: base, AcceptPostpone: true})
	if err != nil {
		t.Fatalf("postpone at exact span: %v", err)
	}
	if !res.Postponed || res.OrigSlot != base || res.SlotStart != base+1800 {
		t.Fatalf("unexpected postpone: %+v", res)
	}
	mustSlot(t, s, "R", base+2000, base+2030, 1)
	_, err = s.Place(now, PlaceRequest{OrderID: "q", Region: "R", SlotStart: base, AcceptPostpone: true})
	// base+1801 超出跨度不可达，故仍落 base+1800（仅剩 0 余量）→ 无候选。
	wantCode(t, err, ErrSlotFull, "beyond span")
	_, err = s.Place(now, PlaceRequest{OrderID: "r", Region: "R", SlotStart: base})
	wantCode(t, err, ErrSlotFull, "decline postpone")
}

// 调低上限进入超额：新预约报满、满/超额时段不作顺延候选；取消回落恢复；调高立即生效。
func TestOverbookAndSettle(t *testing.T) {
	s, _ := New(testConfig())
	mustRegion(t, s, "R")
	now := int64(0)
	a, b := int64(1000), int64(2000)
	mustSlot(t, s, "R", a, a+30, 2)
	mustSlot(t, s, "R", b, b+30, 2)
	for _, id := range []string{"1", "2"} {
		if _, err := s.Place(now, PlaceRequest{OrderID: id, Region: "R", SlotStart: a}); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.SetCapacity(now, "R", a, 1); err != nil {
		t.Fatal(err)
	}
	infos, _ := s.QuerySlots(now, "R", 0, 10000)
	if !infos[0].Overbooked || infos[0].Occupied != 2 {
		t.Fatalf("want overbooked occ=2: %+v", infos[0])
	}
	_, err := s.Place(now, PlaceRequest{OrderID: "3", Region: "R", SlotStart: a})
	wantCode(t, err, ErrSlotFull, "overbooked direct")

	// 满时段不作顺延候选：源 500 满，候选 b 容量 0（满），无候选。
	mustSlot(t, s, "R", 500, 530, 1)
	if _, err := s.Place(now, PlaceRequest{OrderID: "occ500", Region: "R", SlotStart: 500}); err != nil {
		t.Fatal(err)
	}
	// 先让 b 占 1，再把 b 容量调到 0，使其成为超额（occ>cap），
	// 超额时段不得作为顺延候选。
	if _, err := s.Place(now, PlaceRequest{OrderID: "b1", Region: "R", SlotStart: b}); err != nil {
		t.Fatal(err)
	}
	if err := s.SetCapacity(now, "R", b, 0); err != nil {
		t.Fatal(err)
	}
	_, err = s.Place(now, PlaceRequest{OrderID: "4", Region: "R", SlotStart: 500, AcceptPostpone: true})
	wantCode(t, err, ErrSlotFull, "full slot not a candidate")

	if _, err := s.Cancel(now, "b1"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Cancel(now, "1"); err != nil {
		t.Fatal(err)
	}
	infos, _ = s.QuerySlots(now, "R", 0, 10000)
	if infos[0].Overbooked {
		t.Fatalf("should settle: %+v", infos[0])
	}
	// occ==cap(1) 仍为满，调高上限后立即能接新预约；此处直接验证“回落脱离超额”
	// 的语义由 Overbooked=false 覆盖，接客能力通过调高上限验证。
	if err := s.SetCapacity(now, "R", a, 5); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Place(now, PlaceRequest{OrderID: "5", Region: "R", SlotStart: a}); err != nil {
		t.Fatalf("after settle+raise should accept: %v", err)
	}
	if _, err := s.Place(now, PlaceRequest{OrderID: "6", Region: "R", SlotStart: a}); err != nil {
		t.Fatalf("raised cap: %v", err)
	}
}

// 改期到满时段被拒且名额保持；成功为原子迁移；改期到原时段报无需改期。
func TestRescheduleFailureKeepsQuota(t *testing.T) {
	s, _ := New(testConfig())
	mustRegion(t, s, "R")
	a, b := int64(3000), int64(3200)
	mustSlot(t, s, "R", a, a+30, 1)
	mustSlot(t, s, "R", b, b+30, 1)
	if _, err := s.Place(1000, PlaceRequest{OrderID: "mv", Region: "R", SlotStart: a}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Place(1000, PlaceRequest{OrderID: "blk", Region: "R", SlotStart: b}); err != nil {
		t.Fatal(err)
	}
	wantCode(t, s.Reschedule(1000, "mv", "R", b), ErrSlotFull, "target full")
	infos, _ := s.QuerySlots(1000, "R", 0, 10000)
	if infos[0].Occupied != 1 || infos[1].Occupied != 1 {
		t.Fatalf("quota must be retained: %+v", infos)
	}
	info, _ := s.QueryOrder(1000, "mv")
	if info.SlotStart != a {
		t.Fatalf("order must stay at a: %+v", info)
	}
	wantCode(t, s.Reschedule(1000, "mv", "R", a), ErrNoRescheduleNeeded, "same slot")
	if _, err := s.Cancel(1000, "blk"); err != nil {
		t.Fatal(err)
	}
	if err := s.Reschedule(1000, "mv", "R", b); err != nil {
		t.Fatalf("reschedule success: %v", err)
	}
	infos, _ = s.QuerySlots(1000, "R", 0, 10000)
	if infos[0].Occupied != 0 || infos[1].Occupied != 1 {
		t.Fatalf("atomic migration: %+v", infos)
	}
}

// 送达只允许在释放后；释放后取消须记录；终结后名额回落。
func TestCancelAfterReleaseAndDeliver(t *testing.T) {
	s, _ := New(testConfig())
	mustRegion(t, s, "R")
	start := int64(2000)
	mustSlot(t, s, "R", start, start+30, 2)
	if _, err := s.Place(1000, PlaceRequest{OrderID: "o", Region: "R", SlotStart: start}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Place(1000, PlaceRequest{OrderID: "c", Region: "R", SlotStart: start}); err != nil {
		t.Fatal(err)
	}
	wantCode(t, s.Deliver(1699, "o"), ErrNotReleased, "deliver before release")
	if err := s.Deliver(1700, "o"); err != nil {
		t.Fatalf("deliver at release: %v", err)
	}
	wantCode(t, s.Deliver(1701, "o"), ErrAlreadyDelivered, "double deliver")
	after, err := s.Cancel(1700, "c")
	if err != nil || !after {
		t.Fatalf("cancel after release flag: after=%v err=%v", after, err)
	}
	info, _ := s.QueryOrder(1701, "c")
	if !info.Canceled || !info.CanceledAfterRelease {
		t.Fatalf("flags: %+v", info)
	}
	_, errCancel := s.Cancel(1702, "c")
	wantCode(t, errCancel, ErrAlreadyCanceled, "double cancel")
	infos, _ := s.QuerySlots(1701, "R", start, start+1)
	if infos[0].Occupied != 0 {
		t.Fatalf("both finalized, occ want 0 got %d", infos[0].Occupied)
	}
}

// 并发下单不突破上限。
func TestConcurrentPlaceWithinCapacity(t *testing.T) {
	s, _ := New(testConfig())
	mustRegion(t, s, "R")
	start := int64(5000)
	mustSlot(t, s, "R", start, start+30, 50)
	var wg sync.WaitGroup
	for i := 0; i < 500; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			_, _ = s.Place(0, PlaceRequest{OrderID: "o" + strconv.Itoa(i), Region: "R", SlotStart: start})
		}(i)
	}
	wg.Wait()
	infos, err := s.QuerySlots(0, "R", start, start+1)
	if err != nil {
		t.Fatal(err)
	}
	if infos[0].Occupied != 50 {
		t.Fatalf("capacity breached: %d", infos[0].Occupied)
	}
}

// 时钟回退与三类对象不存在可程序化区分。
func TestClockRollbackAndNotFound(t *testing.T) {
	s, _ := New(testConfig())
	mustRegion(t, s, "R")
	mustSlot(t, s, "R", 2000, 2030, 1)
	if _, err := s.Place(500, PlaceRequest{OrderID: "o", Region: "R", SlotStart: 2000}); err != nil {
		t.Fatal(err)
	}
	var err error
	_, err = s.Place(499, PlaceRequest{OrderID: "x", Region: "R", SlotStart: 2000})
	wantCode(t, err, ErrClockRollback, "rollback")
	_, err = s.Place(500, PlaceRequest{OrderID: "y", Region: "ZZ", SlotStart: 2000})
	wantCode(t, err, ErrRegionNotFound, "region")
	_, err = s.Place(500, PlaceRequest{OrderID: "y", Region: "R", SlotStart: 2001})
	wantCode(t, err, ErrSlotNotFound, "slot")
	_, err = s.QueryOrder(500, "ghost")
	wantCode(t, err, ErrOrderNotFound, "order")
}

// 可验证的性能证明：在 10000 笔预约之上，顺延下单与释放判定的耗时
// 应与小规模场景同阶。基准输出 ns/op，可与 bench=N 对照观察不随预约数增长。
func BenchmarkPostponeIndependentOfOrderCount(b *testing.B) {
	s, _ := New(testConfig())
	_ = s.AddRegion("R")
	// 固定数量时段（候选搜索只与时段数有关），10000 笔预约落在远离 base 的时段上。
	for st := int64(7000); st < 10000; st += 1000 {
		if err := s.AddSlot("R", st, st+600, 100000); err != nil {
			b.Fatal(err)
		}
	}
	for i := 0; i < 10000; i++ {
		if _, err := s.Place(0, PlaceRequest{
			OrderID: "bulk" + strconv.Itoa(i), Region: "R", SlotStart: 7000,
		}); err != nil {
			b.Fatal(err)
		}
	}
	// base 满，须顺延到恰等跨度 1800 的候选；搜索不触碰那 10000 笔预约。
	if err := s.AddSlot("R", 3600, 3630, 1); err != nil {
		b.Fatal(err)
	}
	if err := s.AddSlot("R", 5400, 5430, 100000); err != nil {
		b.Fatal(err)
	}
	// 先占满 base，使每次都必须顺延。
	if _, err := s.Place(0, PlaceRequest{OrderID: "baseocc", Region: "R", SlotStart: 3600}); err != nil {
		b.Fatal(err)
	}
	id := 0
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		id++
		_, _ = s.Place(0, PlaceRequest{
			OrderID:        "bench" + strconv.Itoa(id),
			Region:         "R",
			SlotStart:      3600,
			AcceptPostpone: true,
		})
	}
}

// 释放判定为时刻的纯函数 O(1)：在 10000 笔预约之上查询单笔。
func BenchmarkReleaseCheckConstant(b *testing.B) {
	s, _ := New(testConfig())
	_ = s.AddRegion("R")
	_ = s.AddSlot("R", 7000, 7600, 100000)
	for i := 0; i < 10000; i++ {
		if _, err := s.Place(0, PlaceRequest{
			OrderID: "k" + strconv.Itoa(i), Region: "R", SlotStart: 7000,
		}); err != nil {
			b.Fatal(err)
		}
	}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _ = s.QueryOrder(int64(i%6997), "k5000")
	}
}
