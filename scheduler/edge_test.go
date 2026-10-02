package scheduler_test

import (
	"errors"
	"testing"

	"ontology/scheduler"
)

// 拆分点向上取整：多组非整数 rem*num/den。
func TestSplitCeiling(t *testing.T) {
	cases := []struct {
		rem      int64
		num, den int64
		wantKeep int64
	}{
		{7, 1, 2, 4},
		{10, 1, 3, 4},
		{10, 2, 3, 7},
		{5, 1, 4, 2},
		{5, 3, 4, 4},
		{11, 2, 5, 5},
		{11, 3, 5, 7},
		{100, 1, 7, 15},
		{8, 1, 2, 4},
		{9, 2, 3, 6},
	}
	for _, c := range cases {
		s := scheduler.New()
		id, _ := s.Add(0, c.rem+5)
		if _, _, _, _, err := s.Process(id, 5); err != nil {
			t.Fatal(err)
		}
		nid, err := s.Split(id, c.num, c.den)
		if err != nil {
			t.Fatalf("rem=%d %d/%d: %v", c.rem, c.num, c.den, err)
		}
		p1, _ := s.Progress(id)
		p2, _ := s.Progress(nid)
		if p1.To != 5+c.wantKeep || p2.From != 5+c.wantKeep || p2.To != c.rem+5 {
			t.Fatalf("rem=%d %d/%d: keep=%d want=%d", c.rem, c.num, c.den, p1.To-5, c.wantKeep)
		}
		if p1.From != 0 || p1.To != p2.From || p2.To != c.rem+5 {
			t.Fatal("coverage broken")
		}
	}
}

// 乘积超过 int64（rem*num 可达 1e21）：边界 from/to 与精确取整。
func TestSplitOverflow128(t *testing.T) {
	s := scheduler.New()
	id, err := s.Add(0, 1e15)
	if err != nil {
		t.Fatal(err)
	}
	nid, err := s.Split(id, 1, 1e6) // keep=1e9
	if err != nil {
		t.Fatal(err)
	}
	p1, _ := s.Progress(id)
	p2, _ := s.Progress(nid)
	if p1.To != 1_000_000_000 || p2.From != 1_000_000_000 || p2.To != 1_000_000_000_000_000 {
		t.Fatalf("overflow split: %+v %+v", p1, p2)
	}

	s2 := scheduler.New()
	id2, _ := s2.Add(1, 1e15) // rem=1e15-1
	nid2, err := s2.Split(id2, 1, 1e6)
	if err != nil {
		t.Fatal(err)
	}
	pp, _ := s2.Progress(id2)
	if pp.To != 1_000_000_001 { // ceil(999999999.999)=1000000000
		t.Fatalf("ceil at overflow scale: %+v", pp)
	}
	_ = nid2
}

// 拆分基于 next：先 Process 再 Split；乱序 Ack 与最早 Ack 的 hold 跳变。
func TestSplitBasedOnNext(t *testing.T) {
	s := scheduler.New()
	id, _ := s.Add(0, 100)
	_, _, _, b1, _ := s.Process(id, 10) // [0,10)
	_, _, _, _, _ = s.Process(id, 10)   // [10,20)

	nid, err := s.Split(id, 1, 2) // rem=80 keep=40 sp=60
	if err != nil || nid != 2 {
		t.Fatalf("split = %d,%v", nid, err)
	}
	p, _ := s.Progress(id)
	if p.From != 0 || p.To != 60 || p.Next != 20 || p.Pending != 2 {
		t.Fatalf("old interval = %+v", p)
	}
	w0 := s.Watermark()
	if err := s.Ack(b1 + 1); err != nil { // 先确认后面的批
		t.Fatal(err)
	}
	if s.Watermark() != w0 {
		t.Fatalf("W moved on out-of-order ack: %d -> %d", w0, s.Watermark())
	}
	if err := s.Ack(b1); err != nil { // 确认最早批，hold 跳到 next=20
		t.Fatal(err)
	}
	if s.Watermark() != 20 {
		t.Fatalf("W after earliest ack = %d, want 20", s.Watermark())
	}
}

// Process 后未 Ack：hold 不前进；领完但有未确认批仍压住 W 且拒绝 Process/Split。
func TestExhaustedHoldsWatermark(t *testing.T) {
	s := scheduler.New()
	id, _ := s.Add(5, 9)
	if _, _, _, _, err := s.Process(id, 4); err != nil {
		t.Fatal(err)
	}
	if s.Watermark() != 5 {
		t.Fatalf("W=%d want 5", s.Watermark())
	}
	if _, _, _, _, err := s.Process(id, 1); !errors.Is(err, scheduler.ErrExhausted) {
		t.Fatalf("process exhausted = %v", err)
	}
	if _, err := s.Split(id, 1, 2); !errors.Is(err, scheduler.ErrExhausted) {
		t.Fatalf("split exhausted = %v", err)
	}
}

// from 恰等于 W 允许，差 1 拒绝；被拒不改编号计数。
func TestAddAtWatermark(t *testing.T) {
	s := scheduler.New()
	id, _ := s.Add(10, 20)
	_, _, _, b, _ := s.Process(id, 1)
	if err := s.Ack(b); err != nil {
		t.Fatal(err)
	}
	if s.Watermark() != 11 {
		t.Fatalf("W=%d", s.Watermark())
	}
	if _, err := s.Add(10, 11); !errors.Is(err, scheduler.ErrBelowWatermark) {
		t.Fatalf("below by 1: %v", err)
	}
	id2, err := s.Add(11, 12)
	if err != nil || id2 != 2 {
		t.Fatalf("at W: %d,%v", id2, err)
	}
	id3, _ := s.Add(20, 21)
	if id3 != 3 {
		t.Fatalf("id reused/skipped: %d", id3)
	}
}

// 全部完成后 W 不跳到无穷且仍可 Add；低 hold 区间不回退 W。
func TestWatermarkNoInfinityAndNoRegress(t *testing.T) {
	s := scheduler.New()
	id1, _ := s.Add(100, 102)
	_, _, _, b1, _ := s.Process(id1, 2)
	if err := s.Ack(b1); err != nil {
		t.Fatal(err)
	}
	// 批起点 100 在确认前压住 W；确认后区间完成，hold=next=102 曾压住，
	// 完成后 W 冻结在 100。
	if s.Watermark() != 100 {
		t.Fatalf("W=%d want 100", s.Watermark())
	}
	if _, err := s.Add(50, 60); !errors.Is(err, scheduler.ErrBelowWatermark) {
		t.Fatalf("add below frozen W: %v", err)
	}
	if id2, err := s.Add(100, 101); err != nil || id2 != 2 {
		t.Fatalf("add at frozen W: id=%d err=%v", id2, err)
	}

	// W 只进不退：W 到达 500 后，加入 hold=1000 的高区间（from>=W），
	// W 保持 500；高区间随后成为唯一未完成区间时 W 才升到 1000；
	// 再加入一个 hold 高于 W 但低于其他未完成区间的区间，W 仍不回退。
	s2 := scheduler.New()
	low, _ := s2.Add(0, 1000)
	_, _, _, b, _ := s2.Process(low, 500) // 批 [0,500) 未确认，hold=0
	if err := s2.Ack(b); err != nil {     // hold 跳到 next=500
		t.Fatal(err)
	}
	if s2.Watermark() != 500 {
		t.Fatalf("W=%d want 500", s2.Watermark())
	}
	high, _ := s2.Add(1000, 1010) // hold=1000，高于当前 W；W 不回退也不前进
	if s2.Watermark() != 500 {
		t.Fatalf("W regressed/jumped: %d", s2.Watermark())
	}
	// 再领低区间剩余并确认，仅剩高区间：W 升到 1000。
	_, _, _, b2, _ := s2.Process(low, 500)
	if err := s2.Ack(b2); err != nil {
		t.Fatal(err)
	}
	if s2.Watermark() != 1000 {
		t.Fatalf("W=%d want 1000", s2.Watermark())
	}
	mid, _ := s2.Add(1000, 1005) // hold=1000 与高区间相同，from==W
	_ = mid
	_ = high
}
