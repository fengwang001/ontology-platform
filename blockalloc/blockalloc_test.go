package blockalloc

import (
	"errors"
	"sync"
	"testing"

	"ontology/natlog"
)

func exampleAllocator(t *testing.T) *Allocator {
	t.Helper()
	al, err := New(2, 1024, 1087, 16, 2, 100)
	if err != nil {
		t.Fatal(err)
	}
	return al
}

func mustEntry(seq int64, k natlog.Kind, sub int64, addr, lo, hi int, at int64) natlog.Entry {
	return natlog.Entry{Seq: seq, Kind: k, Sub: sub, Addr: addr, Lo: lo, Hi: hi, At: at}
}

func TestNewInvalid(t *testing.T) {
	bad := []struct {
		a, l, h, s, m int
		tt            int64
	}{
		{0, 1024, 1087, 16, 2, 100},
		{4097, 1024, 1087, 16, 2, 100},
		{2, 1023, 1087, 16, 2, 100},
		{2, 1024, 1087, 17, 2, 100},
		{2, 1024, 1087, 16, 9, 100},
		{2, 1024, 1087, 16, 0, 100},
		{2, 1024, 1087, 16, 2, 1_000_000_001},
	}
	for _, c := range bad {
		if _, err := New(c.a, c.l, c.h, c.s, c.m, c.tt); !errors.Is(err, ErrInvalidArgument) {
			t.Fatalf("case %+v err=%v", c, err)
		}
	}
}

func TestSpecExample(t *testing.T) {
	al := exampleAllocator(t)

	a, p, err := al.Open(7, 0)
	if err != nil || a != 0 || p != 1024 {
		t.Fatalf("Open(7,0)=%d,%d,%v", a, p, err)
	}
	a, p, err = al.Open(9, 1)
	if err != nil || a != 1 || p != 1024 {
		t.Fatalf("Open(9,1)=%d,%d,%v", a, p, err)
	}
	for port := 1025; port <= 1039; port++ {
		if a, p, err = al.Open(7, 2); err != nil || a != 0 || p != port {
			t.Fatalf("fill block0 want %d got %d,%d,%v", port, a, p, err)
		}
	}
	if a, p, err = al.Open(7, 2); err != nil || a != 0 || p != 1040 {
		t.Fatalf("17th=%d,%d,%v", a, p, err)
	}
	for port := 1041; port <= 1055; port++ {
		if _, p, err = al.Open(7, 2); err != nil || p != port {
			t.Fatalf("fill block1 want %d got %d,%v", port, p, err)
		}
	}
	if _, _, err = al.Open(7, 2); !errors.Is(err, ErrBlockLimit) {
		t.Fatalf("33rd err=%v want block limit", err)
	}
	es := al.Log().Entries()
	if len(es) != 3 ||
		es[0] != mustEntry(1, natlog.Alloc, 7, 0, 1024, 1039, 0) ||
		es[1] != mustEntry(2, natlog.Alloc, 9, 1, 1024, 1039, 1) ||
		es[2] != mustEntry(3, natlog.Alloc, 7, 0, 1040, 1055, 2) {
		t.Fatalf("log=%+v", es)
	}
}

// TestIdleBoundaryAndReuse：恰等 idleSince+T 释放，小 1 不释放；空闲块复用后不释放。
func TestIdleBoundaryAndReuse(t *testing.T) {
	al := exampleAllocator(t)
	al.Open(7, 0)
	if a, p, err := al.Open(7, 0); err != nil || a != 0 || p != 1025 {
		t.Fatalf("second session: %d,%d,%v", a, p, err)
	}
	if err := al.Close(7, 0, 1025, 50); err != nil {
		t.Fatal(err)
	}
	if err := al.Close(7, 0, 1024, 50); err != nil {
		t.Fatal(err)
	}
	if a, p, err := al.Open(7, 149); err != nil || a != 0 || p != 1024 {
		t.Fatalf("reuse at 149: %d,%d,%v", a, p, err)
	}
	if n := len(al.Log().Entries()); n != 1 {
		t.Fatalf("entries=%d want 1", n)
	}
	// 用一个不分配新块的接受操作把已接受时刻推进到 200：复用空闲块本身不写日志。
	if a, _, err := al.Open(7, 200); err != nil || a != 0 {
		t.Fatalf("reuse at 200: %d,%v", a, err)
	}
	if sub, err := al.Log().Lookup(0, 1024, 200); err != nil || sub != 7 {
		t.Fatalf("lookup after reuse = %d,%v", sub, err)
	}
	if err := al.Close(7, 0, 1024, 200); err != nil {
		t.Fatal(err)
	}

	al2 := exampleAllocator(t)
	al2.Open(7, 0)
	al2.Close(7, 0, 1024, 50)
	if a, p, err := al2.Open(7, 150); err != nil || a != 0 || p != 1024 {
		t.Fatalf("reopen at 150: %d,%d,%v", a, p, err)
	}
	es := al2.Log().Entries()
	if len(es) != 3 ||
		es[1] != mustEntry(2, natlog.Free, 7, 0, 1024, 1039, 150) ||
		es[2] != mustEntry(3, natlog.Alloc, 7, 0, 1024, 1039, 150) {
		t.Fatalf("entries=%+v", es)
	}
	if sub, _ := al2.Log().Lookup(0, 1024, 149); sub != 7 {
		t.Fatal("lookup 149")
	}
	if sub, _ := al2.Log().Lookup(0, 1024, 150); sub != 7 {
		t.Fatal("lookup 150")
	}

	al3 := exampleAllocator(t)
	al3.Open(7, 0)
	al3.Open(7, 0)
	// 另一订户先在址1占位，使 7 的块释放后址0(0绑定) 与址1(1绑定) 不并列：
	// Open(9,150) 会落到址0 块0；改为先排空址0，新订户只能落址1。
	al3.Open(8, 1)
	al3.Close(7, 0, 1024, 50)
	al3.Close(7, 0, 1025, 50)
	al3.Drain(0, 100)
	a, _, err := al3.Open(9, 150)
	if err != nil || a != 1 {
		t.Fatalf("Open(9,150) addr=%d,%v", a, err)
	}
	if _, err := al3.Log().Lookup(0, 1024, 150); !errors.Is(err, natlog.ErrUnallocated) {
		t.Fatalf("lookup freed block err=%v", err)
	}
}

// TestSameAddrExhaustion：绑定址无空闲块报地址耗尽，别的地址有空闲块也不换。
func TestSameAddrExhaustion(t *testing.T) {
	al, err := New(2, 1024, 1087, 16, 8, 1000)
	if err != nil {
		t.Fatal(err)
	}
	// 先放一个址1 订户；之后址0 绑定数始终 ≤ 址1，四个订户依次填满址0 四块。
	if a, _, _ := al.Open(2, 0); a != 0 {
		t.Fatalf("sub2 addr=%d want 0", a)
	}
	if a, _, _ := al.Open(3, 1); a != 1 {
		t.Fatalf("sub3 addr=%d want 1", a)
	}
	if a, _, _ := al.Open(4, 2); a != 0 { // 1:1 并列取小
		t.Fatalf("sub4 addr=%d want 0", a)
	}
	if a, _, _ := al.Open(5, 3); a != 1 { // 2:1
		t.Fatalf("sub5 addr=%d want 1", a)
	}
	if a, _, _ := al.Open(6, 4); a != 0 { // 2:2 并列取小
		t.Fatalf("sub6 addr=%d want 0", a)
	}
	if a, _, _ := al.Open(7, 5); a != 1 { // 3:2
		t.Fatalf("sub7 addr=%d want 1", a)
	}
	// 址0 块0(sub2)、块1(sub4)、块2(sub6) 已占；把 sub2 块0 开满，
	// 第 16 个会话取同址最小空闲块即块3（1072..1087），再开满。
	for i := 0; i < 15; i++ {
		if a, p, err := al.Open(2, 6); err != nil || a != 0 || p != 1025+i {
			t.Fatalf("fill sub2 block0 %d: %d,%d,%v", i, a, p, err)
		}
	}
	if a, p, err := al.Open(2, 6); err != nil || a != 0 || p != 1072 {
		t.Fatalf("sub2 block3: %d,%d,%v", a, p, err)
	}
	for i := 0; i < 15; i++ {
		if a, _, err := al.Open(2, 6); err != nil || a != 0 {
			t.Fatalf("fill sub2 block3: %d,%v", a, err)
		}
	}
	// sub2 持两块全满（M=8 未超限），址0 无空闲块，址1 有空闲块 → 地址耗尽。
	if _, _, err := al.Open(2, 8); !errors.Is(err, ErrAddrExhausted) {
		t.Fatalf("err=%v want addr exhausted", err)
	}
}

// TestDrainedAddress：排空址老订户旧块可用、不可取新块；新订户不进入。
func TestDrainedAddress(t *testing.T) {
	al, err := New(2, 1024, 1087, 16, 2, 1000)
	if err != nil {
		t.Fatal(err)
	}
	al.Open(7, 0)
	if err := al.Drain(0, 1); err != nil {
		t.Fatal(err)
	}
	if a, p, err := al.Open(7, 2); err != nil || a != 0 || p != 1025 {
		t.Fatalf("old sub old block: %d,%d,%v", a, p, err)
	}
	for i := 0; i < 14; i++ {
		if _, _, err := al.Open(7, 2); err != nil {
			t.Fatal(err)
		}
	}
	if _, _, err := al.Open(7, 2); !errors.Is(err, ErrAddrDrained) {
		t.Fatalf("err=%v want drained", err)
	}
	if a, _, err := al.Open(9, 3); err != nil || a != 1 {
		t.Fatalf("new sub addr=%d,%v", a, err)
	}
	if err := al.Drain(1, 4); err != nil {
		t.Fatal(err)
	}
	if _, _, err := al.Open(11, 5); !errors.Is(err, ErrPoolExhausted) {
		t.Fatalf("err=%v want pool exhausted", err)
	}
	if err := al.Drain(1, 6); err != nil {
		t.Fatal(err)
	}
	if err := al.Drain(1, 5); !errors.Is(err, ErrClockWarp) {
		t.Fatalf("warp err=%v", err)
	}
}

// TestRejectedDoesNotLand：被拒操作不落地释放、不推进时钟。
func TestRejectedDoesNotLand(t *testing.T) {
	al := exampleAllocator(t)
	al.Open(7, 0)
	al.Close(7, 0, 1024, 50) // releaseAt=150
	if err := al.Close(9, 0, 1024, 150); !errors.Is(err, ErrNoSession) {
		t.Fatalf("err=%v", err)
	}
	if n := len(al.Log().Entries()); n != 1 {
		t.Fatalf("rejected close landed: %d", n)
	}
	if _, _, err := al.Open(7, 149); err != nil {
		t.Fatalf("open 149: %v", err)
	}

	al2 := exampleAllocator(t)
	al2.Open(7, 0)
	al2.Close(7, 0, 1024, 50)
	n1 := len(al2.Log().Entries())
	if _, _, err := al2.Open(7, 40); !errors.Is(err, ErrClockWarp) {
		t.Fatalf("warp err=%v", err)
	}
	if len(al2.Log().Entries()) != n1 {
		t.Fatal("warp rejection landed log")
	}
	// 40 的 Open 被拒（时钟回退），块仍空闲且未落地；200 时 Open 别的订户，
	// 先落地 FREE@150（7 解绑），再 ALLOC 新块。
	if a, _, err := al2.Open(8, 200); err != nil || a != 0 {
		t.Fatalf("open 8 at 200: %d,%v", a, err)
	}
	es := al2.Log().Entries()
	if len(es) != 3 ||
		es[1] != mustEntry(2, natlog.Free, 7, 0, 1024, 1039, 150) ||
		es[2] != mustEntry(3, natlog.Alloc, 8, 0, 1024, 1039, 200) {
		t.Fatalf("FREE at logical time: %+v", es)
	}
}

// TestUnbindRebind：块全部释放后解绑，绑定数回落影响选址。
func TestUnbindRebind(t *testing.T) {
	al := exampleAllocator(t)
	al.Open(1, 0)
	if a, _, _ := al.Open(2, 0); a != 1 { // 址1（绑定数 0<1）
		t.Fatalf("sub2 addr=%d want 1", a)
	}
	al.Close(1, 0, 1024, 10)
	// 址1 保留订户 2 的绑定；200 时址0 绑定数回落为 0 < 址1 的 1。
	if a, _, err := al.Open(4, 200); err != nil || a != 0 {
		t.Fatalf("rebind addr=%d,%v", a, err)
	}
	frees := 0
	for _, e := range al.Log().Entries() {
		if e.Kind == natlog.Free && e.At == 110 {
			frees++
		}
	}
	if frees != 1 {
		t.Fatalf("frees=%d want 1", frees)
	}
}

// TestErrorOrder：Open 拒绝次序——非法 > 回退 > 超限 > 排空 > 耗尽 > 池耗尽。
func TestErrorOrder(t *testing.T) {
	al, err := New(1, 1024, 1039, 16, 1, 0)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := al.Open(0, -1); !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("invalid>warp: %v", err)
	}
	al.Open(1, 10)
	if _, _, err := al.Open(1, 9); !errors.Is(err, ErrClockWarp) {
		t.Fatalf("warp: %v", err)
	}
	for i := 0; i < 15; i++ {
		if _, _, err := al.Open(1, 10); err != nil {
			t.Fatal(err)
		}
	}
	// T=0 时 Close 立刻释放块；此处不 close，直接再开 → M 块且全满 → 超限。
	if _, _, err := al.Open(1, 10); !errors.Is(err, ErrBlockLimit) {
		t.Fatalf("limit: %v", err)
	}

	// 排空 + 耗尽 + 池耗尽的次序用第二实例。
	al2, _ := New(1, 1024, 1039, 16, 2, 1000)
	al2.Open(1, 0)
	for i := 0; i < 15; i++ {
		al2.Open(1, 0)
	}
	al2.Drain(0, 1)
	// 一块全满、块数未达 M=2、址已排空 → 排空先于耗尽。
	if _, _, err := al2.Open(1, 2); !errors.Is(err, ErrAddrDrained) {
		t.Fatalf("drained before exhausted: %v", err)
	}

	// Close 次序：非法 > 回退 > 无此会话。
	if err := al2.Close(0, 0, 1024, -1); !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("close invalid>warp: %v", err)
	}
	if err := al2.Close(1, 0, 1024, 0); !errors.Is(err, ErrClockWarp) {
		t.Fatalf("close warp: %v", err)
	}
	if err := al2.Close(2, 0, 1024, 3); !errors.Is(err, ErrNoSession) {
		t.Fatalf("close no session: %v", err)
	}
}

func TestConcurrent(t *testing.T) {
	al, err := New(4, 1024, 2047, 16, 4, 1000)
	if err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	for sub := int64(1); sub <= 16; sub++ {
		wg.Add(1)
		go func(sub int64) {
			defer wg.Done()
			var got [][2]int
			for i := 0; i < 20; i++ {
				now := int64(sub * 100)
				a, p, err := al.Open(sub, now)
				if err == nil {
					got = append(got, [2]int{a, p})
				}
			}
			for i, g := range got {
				_ = al.Close(sub, g[0], g[1], int64(sub*100)+int64(i)+1)
			}
		}(sub)
	}
	wg.Wait()
}
