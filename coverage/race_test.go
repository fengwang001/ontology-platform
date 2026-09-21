package coverage

import (
	"math/rand"
	"sync"
	"testing"
)

// TestConcurrentAddRemoveVerify 在多协程并发 Add/Remove/查询下，
// 断言计数永不为负、Segments 永无半成品状态、结束后 Verify 通过，
// 且最终覆盖与初始预置状态逐元素一致。
func TestConcurrentAddRemoveVerify(t *testing.T) {
	c := New()

	// 预置每层；写者总是 Add 后紧跟同区间 Remove，Remove 时区间
	// 必然仍被跟踪。并发结束后净效果恰好回到预置层数。
	var intervals [][2]int64
	for i := 0; i < 200; i++ {
		lo := int64(i % 12)
		hi := lo + int64(1+(i%7))
		intervals = append(intervals, [2]int64{lo, hi})
		_ = c.Add(lo, hi)
	}

	var writersWG, readersWG sync.WaitGroup
	var rngMu sync.Mutex
	rng := rand.New(rand.NewSource(99))
	stop := make(chan struct{})

	for r := 0; r < 4; r++ {
		readersWG.Add(1)
		go func() {
			defer readersWG.Done()
			for {
				select {
				case <-stop:
					return
				default:
				}
				segs := c.Segments()
				for i, s := range segs {
					if s.Count <= 0 || s.Lo >= s.Hi {
						t.Errorf("invalid segment: %+v", s)
						return
					}
					if i > 0 {
						prev := segs[i-1]
						if s.Lo < prev.Hi ||
							(s.Lo == prev.Hi && s.Count == prev.Count) {
							t.Errorf("observed non-canonical segments near %d", s.Lo)
							return
						}
					}
				}
				if c.CountAt(0).Count < 0 {
					t.Error("negative CountAt observed")
					return
				}
				_ = c.MaxCoverage()
			}
		}()
	}

	for w := 0; w < 6; w++ {
		writersWG.Add(1)
		go func() {
			defer writersWG.Done()
			for k := 0; k < 3000; k++ {
				rngMu.Lock()
				iv := intervals[rng.Intn(len(intervals))]
				rngMu.Unlock()
				if err := c.Add(iv[0], iv[1]); err != nil {
					t.Error(err)
				} else if err := c.Remove(iv[0], iv[1]); err != nil {
					t.Error(err)
				}
			}
		}()
	}

	writersWG.Wait()
	close(stop)
	readersWG.Wait()

	if err := c.Verify(); err != nil {
		t.Fatalf("final Verify: %v", err)
	}

	want := New()
	for _, iv := range intervals {
		_ = want.Add(iv[0], iv[1])
	}
	gotSegs, wantSegs := c.Segments(), want.Segments()
	if len(gotSegs) != len(wantSegs) {
		t.Fatalf("final segments len %d, want %d", len(gotSegs), len(wantSegs))
	}
	for i := range wantSegs {
		if gotSegs[i] != wantSegs[i] {
			t.Fatalf("final seg %d = %+v, want %+v", i, gotSegs[i], wantSegs[i])
		}
	}
}
