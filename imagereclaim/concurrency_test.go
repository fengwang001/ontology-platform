package imagereclaim

import (
	"fmt"
	"math/rand"
	"sync"
	"testing"
)

// TestConcurrentSameImagePull 并发拉取同一镜像恰有一次成功。
func TestConcurrentSameImagePull(t *testing.T) {
	r, _ := New(1_000_000, 0)
	const goroutines = 64
	var wg sync.WaitGroup
	var mu sync.Mutex
	success, exists, other := 0, 0, 0
	layers := []Layer{{ID: "base", Size: 100}, {ID: "app", Size: 50}}
	wg.Add(goroutines)
	for i := 0; i < goroutines; i++ {
		go func() {
			defer wg.Done()
			err := r.Pull("repo:same", layers, 1)
			mu.Lock()
			defer mu.Unlock()
			switch {
			case err == nil:
				success++
			case err.(*Error).Code == ErrImageExists:
				exists++
			default:
				other++
			}
		}()
	}
	wg.Wait()
	if success != 1 || exists != goroutines-1 || other != 0 {
		t.Fatalf("success=%d exists=%d other=%d (goroutines=%d)", success, exists, other, goroutines)
	}
	if r.Used() != 150 {
		t.Fatalf("used=%d want 150", r.Used())
	}
}

// TestConcurrentMixedOps 混合并发压力：used 不变量恒成立、层引用记账自洽、操作均为合法拒绝或成功。
func TestConcurrentMixedOps(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping concurrent stress in short mode")
	}
	const cap int64 = 50_000
	r, _ := New(cap, 2)

	var wg sync.WaitGroup
	var clock sync.Mutex
	var maxNow int64 = 1
	nextNow := func() int64 {
		clock.Lock()
		defer clock.Unlock()
		maxNow++
		return maxNow
	}

	allowedReject := map[ErrCode]bool{
		ErrImageExists: true, ErrImageNotFound: true, ErrImagePulling: true,
		ErrImageReady: true, ErrLayerConflict: true, ErrNoSpace: true,
		ErrNotRunning: true, ErrClockRewind: true,
	}

	worker := func(seed int64, done <-chan struct{}) {
		defer wg.Done()
		rng := rand.New(rand.NewSource(seed))
		pending := make(chan string, 16)
		for {
			select {
			case <-done:
				return
			default:
			}
			img := fmt.Sprintf("repo%d:img%d", rng.Intn(5), rng.Intn(12))
			var layers []Layer
			ln := 1 + rng.Intn(3)
			seen := map[string]bool{}
			for j := 0; j < ln; j++ {
				id := fmt.Sprintf("L%d", rng.Intn(10))
				if seen[id] {
					continue
				}
				seen[id] = true
				layers = append(layers, Layer{ID: id, Size: int64(1 + rng.Intn(3000))})
			}
			var err error
			switch rng.Intn(7) {
			case 0:
				err = r.BeginPull(img, layers, nextNow())
				if err == nil {
					select {
					case pending <- img:
					default:
					}
				}
			case 1:
				err = r.Pull(img, layers, nextNow())
			case 2:
				select {
				case p := <-pending:
					err = r.CommitPull(p, nextNow())
					if err != nil && err.(*Error).Code == ErrImageReady {
						err = nil
					}
				default:
					continue
				}
			case 3:
				select {
				case p := <-pending:
					err = r.AbortPull(p, nextNow())
				default:
					continue
				}
			case 4:
				err = r.Run(img, nextNow())
			case 5:
				err = r.Stop(img, nextNow())
			case 6:
				_, err = r.GC(nextNow(), 90, 30, int64(rng.Intn(3)))
			}
			if err != nil && !allowedReject[err.(*Error).Code] {
				t.Errorf("unexpected rejection %v", err)
				return
			}
			used := r.Used()
			if used < 0 || used > cap {
				t.Errorf("invariant violated: used=%d cap=%d", used, cap)
				return
			}
		}
	}

	const workers = 8
	done := make(chan struct{})
	wg.Add(workers)
	for i := 0; i < workers; i++ {
		go worker(int64(i+1), done)
	}
	// 压力运行一小段时间后收尾。
	for i := 0; i < 200; i++ {
		_, _ = r.GC(nextNow(), 95, 40, 0)
	}
	close(done)
	wg.Wait()

	// 最终做一次内部自洽校验：used 与全部镜像引用的不同层字节和一致。
	final := fullSnapshot(r)
	r.mu.Lock()
	var recompute int64
	uniq := map[string]int64{}
	for _, im := range r.images {
		for _, l := range im.layers {
			uniq[l.ID] = l.Size
		}
	}
	for _, s := range uniq {
		recompute += s
	}
	r.mu.Unlock()
	if r.Used() != recompute {
		t.Fatalf("used=%d recomputed=%d snapshot=%s", r.Used(), recompute, final)
	}
}

// TestReplayDeterminism 相同操作序列重放两次，被删镜像序列、释放字节与 used 完全一致。
func TestReplayDeterminism(t *testing.T) {
	rng := rand.New(rand.NewSource(424242))
	c, k, ops := generateOps(rng)

	type gcRecord struct {
		deleted []string
		freed   int64
		short   bool
		used    int64
	}
	runOnce := func() (int64, []gcRecord) {
		r, _ := New(c, k)
		var gcs []gcRecord
		for _, op := range ops {
			switch op.kind {
			case opBegin:
				_ = r.BeginPull(op.img, op.layers, op.now)
			case opCommit:
				_ = r.CommitPull(op.img, op.now)
			case opAbort:
				_ = r.AbortPull(op.img, op.now)
			case opPull:
				_ = r.Pull(op.img, op.layers, op.now)
			case opRun:
				_ = r.Run(op.img, op.now)
			case opStop:
				_ = r.Stop(op.img, op.now)
			case opGC:
				res, _ := r.GC(op.now, op.high, op.low, op.minAge)
				gcs = append(gcs, gcRecord{
					deleted: append([]string(nil), res.Deleted...),
					freed:   res.Freed, short: res.Short, used: r.Used(),
				})
			}
		}
		return r.Used(), gcs
	}

	used1, g1 := runOnce()
	used2, g2 := runOnce()
	if used1 != used2 || len(g1) != len(g2) {
		t.Fatalf("replay differs: used %d vs %d, gc count %d vs %d", used1, used2, len(g1), len(g2))
	}
	for i := range g1 {
		if !equalStrings(g1[i].deleted, g2[i].deleted) || g1[i].freed != g2[i].freed ||
			g1[i].short != g2[i].short || g1[i].used != g2[i].used {
			t.Fatalf("GC #%d differs:\n run1=%+v\n run2=%+v", i, g1[i], g2[i])
		}
	}
}
