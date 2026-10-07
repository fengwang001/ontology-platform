package sender

import (
	"math/rand"
	"sync"
	"testing"
	"time"
)

// TestConcurrentCalls 并发调用各类事件与查询，配合 -race 验证串行化；
// 结束后校验基本不变量仍然成立。
func TestConcurrentCalls(t *testing.T) {
	s := newTest(t, Config{
		MSS: 8, InitialWindow: 64, BufferCap: 4096,
		CorkTimeout:   5 * time.Millisecond,
		ProbeInterval: 5 * time.Millisecond,
	})
	var wg sync.WaitGroup
	for g := 0; g < 8; g++ {
		wg.Add(1)
		go func(seed int64) {
			defer wg.Done()
			r := rand.New(rand.NewSource(seed))
			now := testBase
			for i := 0; i < 500; i++ {
				now = now.Add(time.Duration(r.Intn(3)) * time.Millisecond)
				switch r.Intn(6) {
				case 0:
					_, _ = s.Write(now, 1+r.Intn(64))
				case 1:
					_, _ = s.Ack(now, r.Intn(s.InFlight()+1), r.Intn(128))
				case 2:
					_, _ = s.Advance(now)
				case 3:
					_, _ = s.SetNoDelay(now, r.Intn(2) == 0)
				case 4:
					_, _ = s.SetCork(now, r.Intn(2) == 0)
				default:
					_ = s.InFlight()
					_ = s.Buffered()
					_ = s.Retained()
					_, _ = s.NextDeadline()
				}
			}
		}(int64(g))
	}
	wg.Wait()
	if s.InFlight() < 0 || s.Buffered() < 0 || s.Retained() < 0 {
		t.Fatalf("invariant broken: inFlight=%d buffered=%d retained=%d",
			s.InFlight(), s.Buffered(), s.Retained())
	}
	if s.Retained() > s.Buffered() {
		t.Fatalf("retained %d exceeds buffered %d", s.Retained(), s.Buffered())
	}
}

// TestEventCostIsConstant 验证稳态事件（无段发出）的处理不做任何堆
// 分配，且与历史事件数无关：先制造十万级历史事件，再测量分配次数。
// 状态全部为定长计数器，分配次数恒为零即开销为 O(1) 的可验证证据。
func TestEventCostIsConstant(t *testing.T) {
	s := newTest(t, Config{
		MSS: 10, InitialWindow: 1 << 30, BufferCap: 1 << 30,
		CorkTimeout:   time.Hour,
		ProbeInterval: time.Hour,
	})
	now := testBase
	// 制造深厚历史：十万个被接受的事件。
	for i := 0; i < 100000; i++ {
		now = now.Add(time.Millisecond)
		if _, err := s.Advance(now); err != nil {
			t.Fatal(err)
		}
	}
	allocs := testing.AllocsPerRun(1000, func() {
		now = now.Add(time.Millisecond)
		if _, err := s.Advance(now); err != nil {
			t.Fatal(err)
		}
		if _, err := s.Ack(now, 0, 1<<30); err != nil {
			t.Fatal(err)
		}
		_ = s.InFlight()
		_ = s.Buffered()
		_ = s.Retained()
		_, _ = s.NextDeadline()
	})
	if allocs != 0 {
		t.Fatalf("steady-state events allocated %v times per run, want 0", allocs)
	}
}

// BenchmarkEventThroughput 在深厚历史下测量各类事件吞吐；历史长度不
// 影响单事件开销。
func BenchmarkEventThroughput(b *testing.B) {
	s, err := NewScheduler(Config{
		MSS: 1460, InitialWindow: 1 << 30, BufferCap: 1 << 30,
		CorkTimeout:   time.Hour,
		ProbeInterval: time.Hour,
	})
	if err != nil {
		b.Fatal(err)
	}
	now := testBase
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		now = now.Add(time.Millisecond)
		if _, err := s.Write(now, 1460); err != nil {
			b.Fatal(err)
		}
		if _, err := s.Ack(now, 1460, 1<<30); err != nil {
			b.Fatal(err)
		}
	}
}

// BenchmarkSteadyState 测量无段发出的稳态事件（时间推进 + 查询）。
func BenchmarkSteadyState(b *testing.B) {
	s, err := NewScheduler(Config{
		MSS: 1460, InitialWindow: 1 << 30, BufferCap: 1 << 30,
		CorkTimeout:   time.Hour,
		ProbeInterval: time.Hour,
	})
	if err != nil {
		b.Fatal(err)
	}
	now := testBase
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		now = now.Add(time.Millisecond)
		if _, err := s.Advance(now); err != nil {
			b.Fatal(err)
		}
	}
}
