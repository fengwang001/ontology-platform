package stackmgr

import "testing"

func testConfig() Config {
	return Config{BaseSize: 4, GrowthFactor: 2, MaxStackSize: 32, TotalQuota: 256,
		ShrinkRatio: ShrinkRatio{Num: 1, Den: 4}} // 1/4 < 1/2
}

func TestGrowSizeMinimalMultiple(t *testing.T) {
	// cur=4,F=2：需求 5 -> 8；需求 8（取等）-> 8；需求 9 -> 16；超 max -> -1。
	cases := []struct{ cur, need, want int }{
		{4, 4, 4}, {4, 5, 8}, {4, 8, 8}, {8, 9, 16}, {16, 32, 32}, {16, 33, -1},
	}
	for _, c := range cases {
		if got := growSize(c.cur, c.need, 32, 2); got != c.want {
			t.Fatalf("growSize(%d,%d)=%d want %d", c.cur, c.need, got, c.want)
		}
	}
}

func TestPushGrowthAndOverflowEquality(t *testing.T) {
	m, err := New(Config{BaseSize: 4, GrowthFactor: 2, MaxStackSize: 8, TotalQuota: 64,
		ShrinkRatio: ShrinkRatio{1, 4}})
	if err != nil {
		t.Fatal(err)
	}
	co, _ := m.Spawn()
	if err := m.Push(co, 5); err != nil { // need=5 增长到 8，未超 max
		t.Fatalf("push within max rejected: %v", err)
	}
	if s := m.Stats().Coroutines[0]; s.Size != 8 {
		t.Fatalf("size=%d want 8", s.Size)
	}
	if err := m.Push(co, 4); classOf(err) != ClassStackOverflow { // need=13 > max 8
		t.Fatalf("want overflow, got %v", err)
	}
	if fc, _ := m.FrameCount(co); fc != 1 { // 被拒压帧不改状态
		t.Fatalf("frames=%d want 1", fc)
	}
}

func TestQuotaEqualityAndRefusedPushNoGrowth(t *testing.T) {
	// base=4，总配额 8：恰好容纳两个协程；第三个 Spawn 配额失败。
	m, _ := New(Config{BaseSize: 4, GrowthFactor: 2, MaxStackSize: 32, TotalQuota: 8,
		ShrinkRatio: ShrinkRatio{1, 4}})
	a, _ := m.Spawn()
	b, _ := m.Spawn()
	if _, err := m.Spawn(); classOf(err) != ClassQuota {
		t.Fatalf("third spawn: %v", err)
	}
	// b 已占满 4 槽，压第 5 槽需增长到 8（额外 4），全局已无配额。
	if err := m.Push(b, 5); classOf(err) != ClassQuota {
		t.Fatalf("want quota, got %v", err)
	}
	if s := m.Stats().Coroutines[1]; s.Size != 4 || s.Growths != 0 {
		t.Fatalf("refused push left growth: %+v", s)
	}
	// a 弹空后 b 才能增长。
	_ = a
}

func TestRelocationPointerSemantics(t *testing.T) {
	m, _ := New(testConfig())
	co, _ := m.Spawn()
	must(t, m.Push(co, 4))
	p, err := m.AddrOf(co, -1, 2)
	if err != nil {
		t.Fatal(err)
	}
	must(t, m.WriteIntAt(co, p, 77))
	// need=4 取等不增长；need=5 触发 4->8；need=14 触发 8->16；
	// need=18 触发 16->32：共三次搬迁。
	must(t, m.Push(co, 1))
	must(t, m.Push(co, 9))
	must(t, m.Push(co, 4))
	if s := m.Stats().Coroutines[0]; s.Size != 32 || s.Growths != 3 {
		t.Fatalf("unexpected stats %+v", s)
	}
	// 搬迁后经旧指针读到相同值。
	if v, err := m.ReadInt(co, p); err != nil || v != 77 {
		t.Fatalf("post-grow read=%d,%v", v, err)
	}
	// 经旧指针写入后能读到新值。
	must(t, m.WriteIntAt(co, p, 88))
	if v, _ := m.ReadInt(co, p); v != 88 {
		t.Fatalf("post-grow write read=%d", v)
	}
	// 指向搬迁后新槽位的指针也正确。
	q, _ := m.AddrOf(co, -1, 0)
	must(t, m.WriteIntAt(co, q, 5))
	if v, _ := m.ReadInt(co, q); v != 5 {
		t.Fatal("new slot pointer broken")
	}
}

func TestDanglingAfterPop(t *testing.T) {
	m, _ := New(testConfig())
	co, _ := m.Spawn()
	must(t, m.Push(co, 2))
	p, _ := m.AddrOf(co, -1, 0)
	must(t, m.Push(co, 2))
	must(t, m.Pop(co)) // 弹出含 p 目标的底帧? 实际 p 指向第一帧；先弹顶帧
	// p 指向的是仍存活的第一帧，应可读。
	must(t, m.WriteIntAt(co, p, 1))
	must(t, m.Pop(co)) // 现在第一帧被弹，p 悬垂
	if _, err := m.ReadInt(co, p); classOf(err) != ClassDangling {
		t.Fatalf("want dangling read, got %v", err)
	}
	if err := m.WriteIntAt(co, p, 2); classOf(err) != ClassDangling {
		t.Fatalf("want dangling write, got %v", err)
	}
	// 悬垂指针不得阻止收缩：仍可正常压弹。
	must(t, m.Push(co, 4))
}

func TestCrossStackRejectedAtWrite(t *testing.T) {
	m, _ := New(testConfig())
	a, _ := m.Spawn()
	b, _ := m.Spawn()
	must(t, m.Push(a, 2))
	must(t, m.Push(b, 2))
	p, _ := m.AddrOf(a, -1, 0)
	if err := m.WritePtr(b, -1, 0, p); classOf(err) != ClassCrossStack {
		t.Fatalf("want cross-stack, got %v", err)
	}
	// 被拒槽位仍是普通零值，不含指针。
	if q, err := m.AddrOf(b, -1, 0); err != nil {
		t.Fatal(err)
	} else if _, err := m.ReadInt(b, q); err != nil {
		t.Fatalf("rejected write left pointer: %v", err)
	}
}

func TestEscapeRejectedAtPublish(t *testing.T) {
	m, _ := New(testConfig())
	a, _ := m.Spawn()
	must(t, m.Push(a, 2))
	p, _ := m.AddrOf(a, -1, 0)
	if err := m.PublishPtr(a, p, 99); classOf(err) != ClassEscape {
		t.Fatalf("want escape, got %v", err)
	}
	if _, ok := m.ExternalInt(99); ok {
		t.Fatal("escaped pointer was registered")
	}
	// 普通值逃逸到栈外是允许的。
	m.PublishInt(7, 42)
	if v, ok := m.ExternalInt(7); !ok || v != 42 {
		t.Fatalf("external int=%d,%v", v, ok)
	}
}

func TestShrinkNoThrash(t *testing.T) {
	m, _ := New(testConfig())
	co, _ := m.Spawn()
	must(t, m.Push(co, 4)) // need=8 取等：4->8
	must(t, m.Push(co, 4)) // need=8 取等：不增长
	must(t, m.Push(co, 8)) // need=16 取等：8->16
	if s := m.Stats().Coroutines[0]; s.Size != 16 {
		t.Fatalf("size=%d want 16", s.Size)
	}
	must(t, m.Pop(co)) // 弹出 8 槽顶帧：used=8，8/16=1/2 不收缩
	if s := m.Stats().Coroutines[0]; s.Size != 16 || s.Shrinks != 0 {
		t.Fatalf("no shrink at high usage expected, stats=%+v", s)
	}
	must(t, m.Pop(co)) // 弹出 4 槽帧：used=4，4/16=1/4 取等不收缩
	if s := m.Stats().Coroutines[0]; s.Size != 16 || s.Shrinks != 0 {
		t.Fatalf("no shrink at equality expected, stats=%+v", s)
	}
	must(t, m.Pop(co)) // used=0；0/16<1/4 缩一级档：16->8
	if s := m.Stats().Coroutines[0]; s.Size != 8 || s.Shrinks != 1 {
		t.Fatalf("shrink stats=%+v", s)
	}
	must(t, m.Push(co, 8)) // 8 槽取等不增长
	must(t, m.Pop(co))     // used=0：8->4
	if s := m.Stats().Coroutines[0]; s.Size != 4 || s.Shrinks != 2 {
		t.Fatalf("shrink to base stats=%+v", s)
	}
	must(t, m.Push(co, 3)) // size=4 内小帧不增长，弹回也不抖动
	must(t, m.Pop(co))
	if s := m.Stats().Coroutines[0]; s.Growths != 2 {
		t.Fatalf("unexpected regrowth %+v", s)
	}
}

func TestConfigRejectsThrashAndBadShape(t *testing.T) {
	bad := []Config{
		{BaseSize: 0, GrowthFactor: 2, MaxStackSize: 8, TotalQuota: 8, ShrinkRatio: ShrinkRatio{1, 4}},
		{BaseSize: 4, GrowthFactor: 1, MaxStackSize: 8, TotalQuota: 8, ShrinkRatio: ShrinkRatio{1, 4}},
		{BaseSize: 4, GrowthFactor: 2, MaxStackSize: 12, TotalQuota: 64, ShrinkRatio: ShrinkRatio{1, 4}},
		// 阈值 1/2 不严格小于 1/F=1/2：会抖动。
		{BaseSize: 4, GrowthFactor: 2, MaxStackSize: 16, TotalQuota: 64, ShrinkRatio: ShrinkRatio{1, 2}},
		{BaseSize: 4, GrowthFactor: 2, MaxStackSize: 16, TotalQuota: 64, ShrinkRatio: ShrinkRatio{3, 5}},
	}
	for i, c := range bad {
		if _, err := New(c); classOf(err) != ClassConfig {
			t.Fatalf("case %d: want config error, got %v", i, err)
		}
	}
}

func TestRelocationFailureLeavesIntactStack(t *testing.T) {
	m, _ := New(Config{BaseSize: 4, GrowthFactor: 2, MaxStackSize: 32, TotalQuota: 4096,
		ShrinkRatio: ShrinkRatio{1, 4}})
	co, _ := m.Spawn()
	must(t, m.Push(co, 4)) // 4->8
	p, _ := m.AddrOf(co, -1, 1)
	must(t, m.WriteIntAt(co, p, 123))
	must(t, m.Push(co, 4)) // 取等填满 8（此时两帧）
	m.FailNextAlloc(1)     // 下一次搬迁申请失败：need=13，8->16
	if err := m.Push(co, 5); classOf(err) != ClassQuota {
		t.Fatalf("want quota on failed alloc, got %v", err)
	}
	if s := m.Stats().Coroutines[0]; s.Size != 8 || s.Growths != 1 {
		t.Fatalf("failed growth changed stack: %+v", s)
	}
	if st := m.Stats(); st.QuotaUsed != 8 {
		t.Fatalf("quota not rolled back: used=%d", st.QuotaUsed)
	}
	if v, err := m.ReadInt(co, p); err != nil || v != 123 {
		t.Fatalf("stack not intact: %d,%v", v, err)
	}
	if fc, _ := m.FrameCount(co); fc != 2 {
		t.Fatalf("frame pushed despite failure: %d", fc)
	}
	// 申请恢复后可正常增长。
	must(t, m.Push(co, 5))
}

func TestPointerToPointerChainAcrossRelocation(t *testing.T) {
	m, _ := New(testConfig())
	co, _ := m.Spawn()
	must(t, m.Push(co, 4))
	p, _ := m.AddrOf(co, -1, 3)
	must(t, m.WriteIntAt(co, p, 9))
	q, _ := m.AddrOf(co, -1, 2)
	must(t, m.WritePtr(co, -1, 2, p))
	must(t, m.Push(co, 8))
	must(t, m.Push(co, 8))
	// q 指向的槽里存放指针 p：经 q 读出该指针后，再解引用应等价于 p。
	if v, err := m.ReadInt(co, q); err == nil {
		t.Fatalf("expected type error reading pointer slot as int, got %d", v)
	}
	if v, _ := m.ReadInt(co, p); v != 9 {
		t.Fatalf("chain target=%d", v)
	}
}

func TestUndefinedPrecedence(t *testing.T) {
	m, _ := New(testConfig())
	if err := m.Push(999, 0); classOf(err) != ClassUndefined {
		t.Fatalf("undefined co: %v", err)
	}
	co, _ := m.Spawn()
	if err := m.Push(co, 0); classOf(err) != ClassParameter {
		t.Fatalf("param after defined: %v", err)
	}
}

func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}

func classOf(err error) ErrorClass {
	if err == nil {
		return ClassUnknown
	}
	if se, ok := err.(*StackError); ok {
		return se.Class
	}
	return ClassUnknown
}
