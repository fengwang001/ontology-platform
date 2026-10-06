package thinpool

import (
	"errors"
	"reflect"
	"testing"
)

func testPool(t *testing.T, physical, over, warn, crit int) *Pool {
	t.Helper()
	p, err := New(Config{
		PhysicalBlocks: physical,
		OvercommitPct:  over,
		WarningPct:     warn,
		CriticalPct:    crit,
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return p
}

func mustCreate(t *testing.T, p *Pool, name string, v, r int) {
	t.Helper()
	if err := p.CreateVolume(name, v, r); err != nil {
		t.Fatalf("create %s: %v", name, err)
	}
}

func mustWrite(t *testing.T, p *Pool, name string, block int) {
	t.Helper()
	got, err := p.Write(name, block)
	if err != nil || !got {
		t.Fatalf("write %s[%d]: got=%v err=%v, want new allocation", name, block, got, err)
	}
}

func assertErrKind(t *testing.T, err error, want Kind) {
	t.Helper()
	if err == nil {
		t.Fatalf("want error kind %s, got nil", want)
	}
	var pe *Error
	if !errors.As(err, &pe) || pe.Kind != want {
		t.Fatalf("error=%v, want kind %s", err, want)
	}
}

func assertWriteKind(t *testing.T, p *Pool, name string, block int, want Kind) {
	t.Helper()
	_, err := p.Write(name, block)
	assertErrKind(t, err, want)
}

func TestOvercommitExactAndOneMore(t *testing.T) {
	// 10 物理块，超分配 150%：虚拟总和上限恰为 15。
	p := testPool(t, 10, 150, 80, 90)
	mustCreate(t, p, "a", 8, 0)
	mustCreate(t, p, "b", 7, 0) // 恰等于上限
	if s := p.Snapshot(); s.TotalVirtual != 15 {
		t.Fatalf("total virtual=%d want 15", s.TotalVirtual)
	}
	assertErrKind(t, p.CreateVolume("c", 1, 0), KindOvercommit) // 多一
}

func TestReservationSumsExactlyPool(t *testing.T) {
	p := testPool(t, 10, 400, 80, 90)
	mustCreate(t, p, "a", 10, 6)
	mustCreate(t, p, "b", 10, 4) // 保留总和恰等于池大小
	if s := p.Snapshot(); s.TotalReserve != 10 {
		t.Fatalf("reserve sum=%d", s.TotalReserve)
	}
	assertErrKind(t, p.CreateVolume("c", 1, 1), KindReservationPool)
}

func TestOwnReservationUsableOthersRejected(t *testing.T) {
	// 4 块池。a、b 各保留 2。空闲初始 4，总欠额 4。
	p := testPool(t, 4, 200, 80, 90)
	mustCreate(t, p, "a", 4, 2)
	mustCreate(t, p, "b", 4, 2)
	mustWrite(t, p, "a", 0) // a 使用自己的保留
	mustWrite(t, p, "a", 1)
	s := p.Snapshot()
	if s.Free != 2 || s.TotalDeficit != 2 {
		t.Fatalf("free=%d deficit=%d, want 2/2", s.Free, s.TotalDeficit)
	}
	// 空闲恰为 b 的保留 2：a 再写侵占 b 的保留被拒；b 自己仍可用。
	assertWriteKind(t, p, "a", 2, KindPoolExhausted)
	mustWrite(t, p, "b", 0)
	s = p.Snapshot()
	if s.Allocated != 3 || s.Free != 1 {
		t.Fatalf("allocated=%d free=%d, want 3/1", s.Allocated, s.Free)
	}
	// 被拒绝的分配不留痕：a 仍只占 2 块。
	assertWriteKind(t, p, "a", 2, KindPoolExhausted)
	if vol := p.Snapshot().Volumes[0]; vol.Allocated != 2 {
		t.Fatalf("a allocated=%d want 2", vol.Allocated)
	}
}

func TestDeficitRisesAfterReclaimThenAllocate(t *testing.T) {
	// 3 块池：a、b 各保留 1。
	p := testPool(t, 3, 400, 80, 90)
	mustCreate(t, p, "a", 4, 1)
	mustCreate(t, p, "b", 4, 1)
	mustWrite(t, p, "a", 0) // free 2，欠额 a0+b1=1
	mustWrite(t, p, "a", 1) // free 1，仍未侵占 b 的 1 块
	assertWriteKind(t, p, "a", 2, KindPoolExhausted)
	// a 回收到保留线以下：欠额从 1 回升到 2，并腾出 2 块空闲。
	got, err := p.Reclaim("a", 0, 2)
	if err != nil || got != 2 {
		t.Fatalf("reclaim=%d err=%v", got, err)
	}
	s := p.Snapshot()
	if s.Free != 3 || s.TotalDeficit != 2 {
		t.Fatalf("free=%d deficit=%d, want 3/2", s.Free, s.TotalDeficit)
	}
	mustWrite(t, p, "a", 0)
	mustWrite(t, p, "b", 0)
	mustWrite(t, p, "a", 1) // 池最终被占满，操作顺序与回收前不同
	s = p.Snapshot()
	if s.Allocated != 3 || s.Free != 0 {
		t.Fatalf("allocated=%d free=%d, want 3/0", s.Allocated, s.Free)
	}
}

func TestWatermarkExactThresholds(t *testing.T) {
	// 10 块：告警 50% 即 5 块取等；严重 80% 即 8 块取等。
	p := testPool(t, 10, 500, 50, 80)
	mustCreate(t, p, "a", 50, 0)
	for i := 0; i < 4; i++ {
		mustWrite(t, p, "a", i)
	}
	if s := p.Snapshot(); s.Level != LevelNormal {
		t.Fatalf("level=%s want normal", s.Level)
	}
	mustWrite(t, p, "a", 4) // allocated=5 恰等于告警阈值
	if s := p.Snapshot(); s.Level != LevelWarning {
		t.Fatalf("level=%s want warning at equality", s.Level)
	}
	for i := 5; i < 8; i++ {
		mustWrite(t, p, "a", i)
	}
	if s := p.Snapshot(); s.Level != LevelCritical || s.Allocated != 8 {
		t.Fatalf("level=%s allocated=%d, want critical/8", s.Level, s.Allocated)
	}
	evs := p.Events()
	if len(evs) != 2 {
		t.Fatalf("events=%v", evs)
	}
	if evs[0] != (Event{Seq: 1, From: LevelNormal, To: LevelWarning, Allocated: 5}) {
		t.Fatalf("event0=%v", evs[0])
	}
	if evs[1] != (Event{Seq: 2, From: LevelWarning, To: LevelCritical, Allocated: 8}) {
		t.Fatalf("event1=%v", evs[1])
	}
}

func TestReclaimCrossesTwoLevelsInOneEvent(t *testing.T) {
	p := testPool(t, 10, 500, 50, 80)
	mustCreate(t, p, "a", 50, 0)
	for i := 0; i < 10; i++ {
		mustWrite(t, p, "a", i)
	}
	got, err := p.Reclaim("a", 0, 6) // 10 -> 4，跨过告警 5
	if err != nil || got != 6 {
		t.Fatalf("reclaim=%d err=%v", got, err)
	}
	if s := p.Snapshot(); s.Level != LevelNormal || s.Allocated != 4 {
		t.Fatalf("level=%s allocated=%d", s.Level, s.Allocated)
	}
	evs := p.Events()
	if len(evs) != 3 {
		t.Fatalf("events=%v, want 3", evs)
	}
	last := evs[2] // 跨两档也只记一个事件
	if last.Seq != 3 || last.From != LevelCritical || last.To != LevelNormal || last.Allocated != 4 {
		t.Fatalf("last event=%v", last)
	}
}

func TestReclaimEndpointsAndZeroLength(t *testing.T) {
	p := testPool(t, 10, 200, 80, 90)
	mustCreate(t, p, "a", 5, 0)
	// 零长度是无操作，start 允许恰等于卷大小。
	if got, err := p.Reclaim("a", 5, 0); err != nil || got != 0 {
		t.Fatalf("zero len at end got=%d err=%v", got, err)
	}
	mustWrite(t, p, "a", 0)
	mustWrite(t, p, "a", 4)
	if got, err := p.Reclaim("a", 4, 1); err != nil || got != 1 { // 右端点块
		t.Fatalf("endpoint reclaim got=%d err=%v", got, err)
	}
	if _, e := p.Reclaim("a", 4, 2); e == nil {
		t.Fatal("want bounds error")
	} else {
		assertErrKind(t, e, KindInvalidArgument)
	} // 越出卷边界
	if _, e := p.Reclaim("a", 6, 0); e == nil {
		t.Fatal("want bounds error")
	} else {
		assertErrKind(t, e, KindInvalidArgument)
	} // start 越界
	if _, e := p.Reclaim("a", -1, 1); e == nil {
		t.Fatal("want bounds error")
	} else {
		assertErrKind(t, e, KindInvalidArgument)
	} // 负起点
	if s := p.Snapshot(); s.Allocated != 1 { // 块 0 仍映射，拒绝不留痕
		t.Fatalf("allocated=%d want 1", s.Allocated)
	}
}

func TestShrinkWithExactlyOneMappedBlock(t *testing.T) {
	p := testPool(t, 20, 200, 80, 90)
	mustCreate(t, p, "a", 10, 0)
	mustWrite(t, p, "a", 7) // 被截去区间 [5,10) 内恰有一个已映射块
	assertErrKind(t, p.Resize("a", 5), KindDataInRange)
	if _, err := p.Reclaim("a", 7, 1); err != nil {
		t.Fatal(err)
	}
	if err := p.Resize("a", 5); err != nil {
		t.Fatalf("shrink after reclaim: %v", err)
	}
	assertWriteKind(t, p, "a", 5, KindInvalidArgument)
	mustCreate(t, p, "b", 10, 5)
	assertErrKind(t, p.Resize("b", 3), KindReservationVolume) // 保留超新卷大小
}

func TestDeleteFreesAllAndNameReusable(t *testing.T) {
	p := testPool(t, 4, 200, 80, 90)
	mustCreate(t, p, "a", 4, 2)
	mustWrite(t, p, "a", 0)
	mustWrite(t, p, "a", 1)
	if err := p.DeleteVolume("a"); err != nil {
		t.Fatal(err)
	}
	s := p.Snapshot()
	if s.Allocated != 0 || s.TotalVirtual != 0 || s.TotalReserve != 0 ||
		s.TotalDeficit != 0 || len(s.Volumes) != 0 {
		t.Fatalf("pool not clean after delete: %+v", s)
	}
	mustCreate(t, p, "a", 4, 0) // 名称重用，旧映射不存在
	got, err := p.Write("a", 0)
	if err != nil || !got {
		t.Fatalf("write after reuse got=%v err=%v", got, err)
	}
	assertErrKind(t, p.DeleteVolume("ghost"), KindVolumeNotFound)
}

func TestRejectionsLeaveNoTrace(t *testing.T) {
	p := testPool(t, 6, 400, 80, 90)
	mustCreate(t, p, "a", 6, 2)

	assertErrKind(t, p.CreateVolume("", 1, 0), KindInvalidArgument)
	assertErrKind(t, p.CreateVolume("a", 1, 0), KindVolumeExists)
	assertErrKind(t, p.CreateVolume("big", 19, 0), KindOvercommit) // 6+19=25 > 上限 24
	assertErrKind(t, p.CreateVolume("r", 1, 2), KindReservationVolume)
	assertErrKind(t, p.CreateVolume("r2", 6, 5), KindReservationPool)
	assertWriteKind(t, p, "ghost", 0, KindVolumeNotFound)
	assertWriteKind(t, p, "a", 6, KindInvalidArgument)
	if _, e := p.Reclaim("ghost", 0, 1); e == nil {
		t.Fatal("want not found")
	} else {
		assertErrKind(t, e, KindVolumeNotFound)
	}

	mustCreate(t, p, "b", 6, 4)
	mustWrite(t, p, "a", 0)
	mustWrite(t, p, "a", 1)
	assertWriteKind(t, p, "a", 2, KindPoolExhausted)
	assertErrKind(t, p.SetReservation("b", 5), KindReservationPool)

	after := p.Snapshot()
	if len(after.Volumes) != 2 {
		t.Fatalf("volumes=%d want 2", len(after.Volumes))
	}
	if after.Allocated != 2 || after.TotalVirtual != 12 || after.TotalReserve != 6 {
		t.Fatalf("unexpected state: %+v", after)
	}

	// 保留不足场景：2 块池被占满，把保留 1 提到 2 会使欠额超过空闲 0。
	q := testPool(t, 2, 400, 80, 90)
	mustCreate(t, q, "a", 4, 1)
	mustCreate(t, q, "b", 4, 0)
	mustWrite(t, q, "a", 0)
	mustWrite(t, q, "b", 0)
	qBefore := q.Snapshot()
	evBefore := len(q.Events())
	assertErrKind(t, q.SetReservation("a", 2), KindReservationShortfall)
	if !reflect.DeepEqual(q.Snapshot(), qBefore) {
		t.Fatal("state changed after rejected SetReservation")
	}
	if len(q.Events()) != evBefore {
		t.Fatal("rejected SetReservation emitted an event")
	}
}
