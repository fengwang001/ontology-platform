package scheduler_test

import (
	"errors"
	"sync"
	"testing"

	"ontology/scheduler"
)

// 拒绝原因可区分：重复 Ack 与无此批；被拒不改状态/计数。
func TestAckErrorDistinction(t *testing.T) {
	s := scheduler.New()
	id, _ := s.Add(0, 5)
	_, _, _, b, _ := s.Process(id, 2)
	if err := s.Ack(b); err != nil {
		t.Fatal(err)
	}
	p0 := s.HoldProbes() // 只统计之后被拒操作的探测
	if err := s.Ack(b); !errors.Is(err, scheduler.ErrAlreadyAcked) {
		t.Fatalf("dup ack = %v", err)
	}
	if err := s.Ack(b + 100); !errors.Is(err, scheduler.ErrNoBatch) {
		t.Fatalf("missing batch = %v", err)
	}
	if err := s.Ack(0); !errors.Is(err, scheduler.ErrInvalid) {
		t.Fatalf("ack(0) = %v", err)
	}
	// 拒绝操作零探测、不改状态。
	if s.HoldProbes() != p0 {
		t.Fatalf("rejected op probed: %d vs %d", s.HoldProbes(), p0)
	}
}

// 参数非法的各种形态与优先级。
func TestInvalidArgsOrdering(t *testing.T) {
	s := scheduler.New()
	for _, c := range [][2]int64{
		{-1, 10}, {0, 0}, {10, 9}, {0, 1_000_000_000_000_001},
	} {
		if _, err := s.Add(c[0], c[1]); !errors.Is(err, scheduler.ErrInvalid) {
			t.Fatalf("Add(%d,%d)=%v", c[0], c[1], err)
		}
	}
	id, _ := s.Add(10, 20)
	if _, err := s.Add(5, 5); !errors.Is(err, scheduler.ErrInvalid) {
		t.Fatalf("invalid-before-watermark: %v", err)
	}
	if _, _, _, _, err := s.Process(id, 0); !errors.Is(err, scheduler.ErrInvalid) {
		t.Fatalf("n=0: %v", err)
	}
	if _, err := s.Split(id, 1, 0); !errors.Is(err, scheduler.ErrInvalid) {
		t.Fatalf("den=0: %v", err)
	}
	if _, err := s.Split(id, 2, 1); !errors.Is(err, scheduler.ErrInvalid) {
		t.Fatalf("num>den: %v", err)
	}
	if _, _, _, _, err := s.Process(0, 1); !errors.Is(err, scheduler.ErrInvalid) {
		t.Fatalf("id=0: %v", err)
	}
	if _, _, _, _, err := s.Process(999, 1); !errors.Is(err, scheduler.ErrNoInterval) {
		t.Fatalf("missing id: %v", err)
	}
	if _, err := s.Progress(999); !errors.Is(err, scheduler.ErrNoInterval) {
		t.Fatalf("progress missing: %v", err)
	}
}

// holdProbes 分档对照：同一 Process / Ack 的增量在不同规模下必须相等。
func TestHoldProbesScaleIndependent(t *testing.T) {
	measure := func(nIntervals, nPending int64) (procDelta, ackDelta int64) {
		s := scheduler.New()
		target, _ := s.Add(0, nPending+2)
		for i := int64(0); i < nPending; i++ {
			if _, _, _, _, err := s.Process(target, 1); err != nil {
				t.Fatal(err)
			}
		}
		for i := int64(0); i < nIntervals; i++ {
			if _, err := s.Add(1_000_000_000, 1_000_000_002); err != nil {
				t.Fatal(err)
			}
		}

		before := s.HoldProbes()
		if _, _, _, _, err := s.Process(target, 1); err != nil {
			t.Fatal(err)
		}
		procDelta = s.HoldProbes() - before

		before = s.HoldProbes()
		if err := s.Ack(1); err != nil { // 最早批：hold 跳到批 2 起点
			t.Fatal(err)
		}
		ackDelta = s.HoldProbes() - before
		return
	}

	p1, a1 := measure(10, 10)
	p2, a2 := measure(10_000, 10)
	p3, a3 := measure(10, 10_000)
	p4, a4 := measure(10_000, 10_000)
	if !(p1 == p2 && p2 == p3 && p3 == p4) {
		t.Fatalf("process probe deltas vary by scale: %d %d %d %d", p1, p2, p3, p4)
	}
	if !(a1 == a2 && a2 == a3 && a3 == a4) {
		t.Fatalf("ack probe deltas vary by scale: %d %d %d %d", a1, a2, a3, a4)
	}
	if p1 > 4 || a1 > 4 {
		t.Fatalf("probe delta too large: process=%d ack=%d", p1, a1)
	}
	t.Logf("scale-independent probe deltas: process=%d ack=%d", p1, a1)
}

// 同一操作序列重放：返回值、W 与 holdProbes 完全一致。
func TestReplayDeterminism(t *testing.T) {
	run := func() ([]string, int64, int64) {
		s := scheduler.New()
		var log []string
		id, _ := s.Add(10, 40)
		log = append(log, "Add", itoa(id))
		_, _, _, b, _ := s.Process(id, 5)
		nid, _ := s.Split(id, 3, 4)
		_, _, _, b2, _ := s.Process(id, 2)
		_ = b2
		_ = s.Ack(b)
		_, _, _, b3, _ := s.Process(nid, 8)
		_ = s.Ack(b3)
		log = append(log, "W", itoa(s.Watermark()), "probes", itoa(s.HoldProbes()))
		p, _ := s.Progress(id)
		log = append(log, "p", itoa(p.From), itoa(p.To), itoa(p.Next), itoa(int64(p.Pending)))
		return log, s.Watermark(), s.HoldProbes()
	}
	l1, w1, q1 := run()
	l2, w2, q2 := run()
	if w1 != w2 || q1 != q2 || !equalStrings(l1, l2) {
		t.Fatalf("replay differs: %v vs %v (W %d/%d probes %d/%d)", l1, l2, w1, w2, q1, q2)
	}
}

func itoa(v int64) string {
	if v == 0 {
		return "0"
	}
	neg := v < 0
	if neg {
		v = -v
	}
	var b [24]byte
	i := len(b)
	for v > 0 {
		i--
		b[i] = byte('0' + v%10)
		v /= 10
	}
	if neg {
		i--
		b[i] = '-'
	}
	return string(b[i:])
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// 并发调用：串行等价性由互斥锁保证；这里跑竞态检测与 W 单调检查。
func TestConcurrent(t *testing.T) {
	const G, Each = 8, 200
	s := scheduler.New()
	// 总余量必须 >= 总 Process 次数，否则领完后 Process 返回 ErrExhausted。
	id, err := s.Add(0, G*Each+1)
	if err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	batches := make(chan int64, G*Each)
	for g := 0; g < G; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < Each; i++ {
				_, _, _, b, perr := s.Process(id, 1)
				if perr != nil {
					t.Error(perr)
					return
				}
				batches <- b
				_ = s.Watermark()
			}
		}()
	}
	wg.Wait()
	close(batches)
	p, err := s.Progress(id)
	if err != nil {
		t.Fatal(err)
	}
	if p.Next != int64(G*Each) {
		t.Fatalf("next=%d want %d", p.Next, G*Each)
	}
	// 领取阶段：最早批起点 0 未确认，W 一直为 0。
	if s.Watermark() != 0 {
		t.Fatalf("W during processing = %d", s.Watermark())
	}
	var bs []int64
	for b := range batches {
		bs = append(bs, b)
	}
	// 按批号收集；乱序确认 1600..2，最后确认批 1。
	byID := make([]int64, G*Each+1)
	for _, b := range bs {
		byID[b] = b
	}
	for i := G * Each; i >= 2; i-- {
		if err := s.Ack(byID[i]); err != nil {
			t.Fatal(err)
		}
	}
	// 最早批未确认前 W 不动。
	if s.Watermark() != 0 {
		t.Fatalf("W moved before earliest ack: %d", s.Watermark())
	}
	if err := s.Ack(1); err != nil {
		t.Fatal(err)
	}
	p2, _ := s.Progress(id)
	if p2.Pending != 0 {
		t.Fatalf("pending=%d", p2.Pending)
	}
	// 全部批确认：hold=next=G*Each，区间未领完（还剩 1），W=G*Each。
	if s.Watermark() != int64(G*Each) {
		t.Fatalf("W=%d want %d", s.Watermark(), G*Each)
	}
}
