package consistency

import (
	"context"
	"fmt"
	"math/rand/v2"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// TestConcurrentReadsAndApplies 在 -race 下并发执行应用/心跳与读取：
// 读取彼此并发、也与写入并发；校验全局游标单调、快照与全历史参照一致。
func TestConcurrentReadsAndApplies(t *testing.T) {
	ctx := context.Background()
	views := []string{"a", "b", "c"}
	s, err := NewStore(8, views...)
	if err != nil {
		t.Fatal(err)
	}

	// mirrorMu 把“Store 与全历史参照的成对操作”绑成原子块：多个读取
	// 执行体可同时持有 RLock（彼此并发，仍会在 Store 的内部 RWMutex 上
	// 与写入执行体并发），写入执行体则串行发布到两者。
	var mirrorMu sync.RWMutex
	ref := newNaive(8, views...)
	var tsCounter atomic.Int64
	var lastReadAt atomic.Int64

	var wg sync.WaitGroup
	stop := make(chan struct{})

	// 3 个写入执行体：全局递增时间戳，随机应用或心跳到随机视图。
	for range 3 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				select {
				case <-stop:
					return
				default:
				}
				ts := tsCounter.Add(1)
				view := views[ts%int64(len(views))]
				mirrorMu.Lock()
				if ts%5 == 0 {
					if err := s.Heartbeat(ctx, view, ts); err == nil {
						_ = ref.heartbeat(view, ts)
					}
				} else {
					value := fmt.Sprintf("v%d", ts)
					if err := s.Apply(ctx, view, ts, value); err == nil {
						_ = ref.apply(view, ts, value)
					}
				}
				mirrorMu.Unlock()
			}
		}()
	}

	// 6 个读取执行体：混合 Read 与 ReadAt。
	for id := range 6 {
		wg.Add(1)
		go func(id int64) {
			defer wg.Done()
			rng := rand.New(rand.NewPCG(uint64(id)+1, uint64(id)*7+1))
			localLast := Timestamp(-1)
			for {
				select {
				case <-stop:
					return
				default:
				}
				var snap *Snapshot
				var err error
				if id%2 == 0 {
					mirrorMu.RLock()
					snap, err = s.Read(ctx)
					if err == nil {
						validateAgainstRef(t, ref, snap)
					}
					mirrorMu.RUnlock()
				} else {
					latest := tsCounter.Load()
					if latest == 0 {
						continue
					}
					at := 1 + rng.Int64N(latest)
					mirrorMu.RLock()
					snap, err = s.ReadAt(ctx, at)
					if err == nil {
						validateAgainstRef(t, ref, snap)
					}
					mirrorMu.RUnlock()
				}
				if err != nil {
					continue // 未准备好/太旧是合法结果
				}

				// Read 跨所有执行体全局单调不减。
				if id%2 == 0 {
					if snap.At < localLast {
						t.Errorf("reader local monotonic broken: %d < %d", snap.At, localLast)
						return
					}
					localLast = snap.At
					for {
						cur := lastReadAt.Load()
						if snap.At < cur {
							t.Errorf("global read cursor went backwards: %d < %d", snap.At, cur)
							return
						}
						if lastReadAt.CompareAndSwap(cur, snap.At) {
							break
						}
					}
				}
			}
		}(int64(id))
	}

	time.Sleep(150 * time.Millisecond)
	close(stop)
	wg.Wait()
}

func validateAgainstRef(t *testing.T, ref *naiveStore, snap *Snapshot) {
	t.Helper()
	for _, vs := range snap.Views {
		if got := ref.valueAt(vs.View, snap.At); got != nil && got != vs.Value {
			t.Errorf("view %s at %d value=%v want %v", vs.View, snap.At, vs.Value, got)
		}
	}
}

// TestLoggerPrintsInputsAndDecisions 校验日志逐步记录输入、时间点与判定依据。
func TestLoggerPrintsInputsAndDecisions(t *testing.T) {
	ctx := context.Background()
	var mu sync.Mutex
	var sb strings.Builder

	s, err := NewStore(2, "a", "b")
	if err != nil {
		t.Fatal(err)
	}
	s.SetLogger(func(format string, args ...any) {
		mu.Lock()
		defer mu.Unlock()
		fmt.Fprintf(&sb, format+"\n", args...)
	})

	_ = s.Apply(ctx, "a", 5, "a5")
	_ = s.Heartbeat(ctx, "a", 3) // 不前进
	_, _ = s.Read(ctx)           // b 尚无版本：not-ready
	_ = s.Apply(ctx, "b", 5, "b5")
	_, _ = s.ReadAt(ctx, 7) // 超过 minProgress(5)：not-ready
	snap, err := s.ReadAt(ctx, 5)
	if err != nil {
		t.Fatal(err)
	}
	if snap.At != 5 {
		t.Fatalf("at=%d want 5", snap.At)
	}
	_ = s.Apply(ctx, "a", 7, "a7")
	_ = s.Apply(ctx, "a", 9, "a9") // a 淘汰 ts=5
	_, _ = s.ReadAt(ctx, 5)        // a 最旧版本为 7：too-old

	mu.Lock()
	log := sb.String()
	mu.Unlock()

	for _, want := range []string{
		"APPLY  ok view=a ts=5",
		"HEARTB reject view=a ts=3",
		"reason=timestamp-not-advancing",
		"READ   reject at=-1",
		"reason=not-ready",
		"READ-AT reject at=7 minProgress=5",
		"READ-AT ok at=5 minProgress=5",
		"APPLY  ok view=a ts=9 kept=2",
		"reason=too-old",
	} {
		if !strings.Contains(log, want) {
			t.Fatalf("log missing %q\n--- log ---\n%s", want, log)
		}
	}
}
