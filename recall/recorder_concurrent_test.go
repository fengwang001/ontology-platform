package recall

import (
	"bytes"
	"context"
	"fmt"
	"log/slog"
	"sync"
	"sync/atomic"
	"testing"
)

// 并发追加+打开快照：上界始终等于活跃快照水位集合的最小值（串行参照）。
func TestConcurrentOpenBoundMatchesReference(t *testing.T) {
	ctx := context.Background()
	r := New(0, discardLogger())
	appendN(t, ctx, r, 10)

	const workers = 8
	const rounds = 100

	var mu sync.Mutex
	type openInfo struct {
		id uint64
		wm int64
	}
	var opened []openInfo
	var maxSeq int64 = 10

	var wg sync.WaitGroup
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < rounds; i++ {
				if i%2 == 0 {
					seq, err := r.Append(ctx, []byte("x"))
					if err != nil {
						t.Errorf("Append: %v", err)
						return
					}
					mu.Lock()
					if seq > maxSeq {
						maxSeq = seq
					}
					mu.Unlock()
				} else {
					id, wm, err := r.OpenSnapshot(ctx)
					if err != nil {
						t.Errorf("OpenSnapshot: %v", err)
						return
					}
					mu.Lock()
					opened = append(opened, openInfo{id, wm})
					mu.Unlock()
				}
			}
		}()
	}
	wg.Wait()

	if len(opened) == 0 {
		t.Fatal("expected opened snapshots")
	}

	// 串行参照：上界 = 所有活跃快照水位的最小值。
	minWM := maxSeq
	for _, o := range opened {
		if o.wm < minWM {
			minWM = o.wm
		}
	}
	if got := r.ReclaimBound(ctx); got != minWM {
		t.Fatalf("bound = %d, serial reference = %d", got, minWM)
	}

	// 并发关闭期间，只要还有原始快照存活，上界必须恒等于初始最小水位，不回退也不超前。
	var boundRegressed atomic.Bool
	stop := make(chan struct{})
	var readers sync.WaitGroup
	for w := 0; w < 4; w++ {
		readers.Add(1)
		go func() {
			defer readers.Done()
			for {
				select {
				case <-stop:
					return
				default:
					if r.ReclaimBound(ctx) < minWM {
						boundRegressed.Store(true)
					}
				}
			}
		}()
	}

	var closers sync.WaitGroup
	for _, o := range opened {
		closers.Add(1)
		go func(id uint64) {
			defer closers.Done()
			if err := r.CloseSnapshot(ctx, id); err != nil {
				t.Errorf("CloseSnapshot %d: %v", id, err)
			}
		}(o.id)
	}
	closers.Wait()
	close(stop)
	readers.Wait()

	if boundRegressed.Load() {
		t.Fatal("回收上界在并发关闭期间发生回退")
	}

	// 全部关闭后，上界 = 当前最大序号；与串行参照（收集到的 append 序号最大值）一致。
	n, bound, err := r.Reclaim(ctx)
	if err != nil {
		t.Fatalf("Reclaim: %v", err)
	}
	if bound != maxSeq {
		t.Fatalf("final bound = %d, serial reference maxSeq = %d", bound, maxSeq)
	}
	if int64(n) != maxSeq {
		t.Fatalf("reclaimed = %d, want %d (all records incl. boundary)", n, maxSeq)
	}
}

// 并发打开再关闭若干快照后，回收上界必须与顺序执行结果一致。
func TestConcurrentOpenCloseSameAsSerial(t *testing.T) {
	ctx := context.Background()

	// 并发路径
	rc := New(0, discardLogger())
	appendN(t, ctx, rc, 10)
	const pairs = 32
	ids := make(chan uint64, pairs)
	var openWG sync.WaitGroup
	for i := 0; i < pairs; i++ {
		openWG.Add(1)
		go func() {
			defer openWG.Done()
			id, _, err := rc.OpenSnapshot(ctx)
			if err != nil {
				t.Errorf("OpenSnapshot: %v", err)
				return
			}
			ids <- id
		}()
	}
	openWG.Wait()
	close(ids)
	var closeWG sync.WaitGroup
	for id := range ids {
		closeWG.Add(1)
		go func(id uint64) {
			defer closeWG.Done()
			if err := rc.CloseSnapshot(ctx, id); err != nil {
				t.Errorf("CloseSnapshot: %v", err)
			}
		}(id)
	}
	closeWG.Wait()
	cN, cBound, err := rc.Reclaim(ctx)
	if err != nil {
		t.Fatalf("concurrent Reclaim: %v", err)
	}

	// 顺序参照路径：同样 10 条记录、32 次打开、32 次关闭，全部顺序执行。
	rs := New(0, discardLogger())
	appendN(t, ctx, rs, 10)
	var serialIDs []uint64
	for i := 0; i < pairs; i++ {
		id, _, err := rs.OpenSnapshot(ctx)
		if err != nil {
			t.Fatalf("serial OpenSnapshot: %v", err)
		}
		serialIDs = append(serialIDs, id)
	}
	for _, id := range serialIDs {
		if err := rs.CloseSnapshot(ctx, id); err != nil {
			t.Fatalf("serial CloseSnapshot: %v", err)
		}
	}
	sN, sBound, err := rs.Reclaim(ctx)
	if err != nil {
		t.Fatalf("serial Reclaim: %v", err)
	}

	if cN != sN || cBound != sBound {
		t.Fatalf("concurrent=(%d,%d) serial=(%d,%d)", cN, cBound, sN, sBound)
	}
	if cBound != 10 {
		t.Fatalf("bound = %d, want 10", cBound)
	}
}

// 并发回收期间，活跃快照水位内的记录绝不被回收（快照不丢数据）。
func TestConcurrentReclaimPreservesSnapshotData(t *testing.T) {
	ctx := context.Background()
	var buf bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&buf, nil))
	r := New(0, logger)
	appendN(t, ctx, r, 50)
	idLow, wmLow := mustOpen(t, ctx, r) // 水位 50：决定回收上界（含等号回收）
	if wmLow != 50 {
		t.Fatalf("low watermark = %d, want 50", wmLow)
	}
	appendN(t, ctx, r, 50)
	idHigh, wmHigh := mustOpen(t, ctx, r) // 水位 100：保护 (50,100] 区间不丢
	if wmHigh != 100 {
		t.Fatalf("high watermark = %d, want 100", wmHigh)
	}

	var wg sync.WaitGroup

	// 后台追加 + 回收
	wg.Add(2)
	go func() {
		defer wg.Done()
		for i := 0; i < 50; i++ {
			if _, err := r.Append(ctx, []byte(fmt.Sprintf("a%d", i))); err != nil {
				t.Errorf("Append: %v", err)
				return
			}
		}
	}()
	go func() {
		defer wg.Done()
		for i := 0; i < 100; i++ {
			if _, _, err := r.Reclaim(ctx); err != nil {
				t.Errorf("Reclaim: %v", err)
				return
			}
		}
	}()

	// 重放者：严格高于回收上界、且不超高水位快照水位的位点始终可读，快照不丢数据。
	for seq := int64(51); seq <= 100; seq++ {
		rec, err := r.Replay(ctx, idHigh, seq)
		if err != nil {
			t.Fatalf("high snapshot lost seq %d: %v", seq, err)
		}
		if string(rec.Payload) != "r"+itoa(seq) {
			t.Fatalf("seq %d payload = %q", seq, rec.Payload)
		}
	}
	wg.Wait()

	// 快照存活时，回收上界被钳在 50，水位内数据未丢。
	if b := r.ReclaimBound(ctx); b != 50 {
		t.Fatalf("bound with live snapshot = %d, want 50", b)
	}
	// 含等号边界：50 已按规则回收；51..100 完好。
	if _, err := r.Replay(ctx, idLow, 50); err == nil {
		t.Fatal("seq=50 at inclusive boundary should be reclaimed")
	}
	if _, err := r.Replay(ctx, idHigh, 51); err != nil {
		t.Fatalf("seq=51 should be retained: %v", err)
	}

	// 关闭后全部回收，旧位点永久不可重放。
	if err := r.CloseSnapshot(ctx, idLow); err != nil {
		t.Fatal(err)
	}
	// 高水位快照仍活跃，上界重估为 100（含等号）。
	if _, bound, _ := r.Reclaim(ctx); bound != 100 {
		t.Fatalf("bound with only high snapshot = %d, want 100", bound)
	}
	if _, err := r.Replay(ctx, idHigh, 100); err == nil {
		t.Fatal("seq=100 at inclusive boundary should be reclaimed")
	}
	if err := r.CloseSnapshot(ctx, idHigh); err != nil {
		t.Fatal(err)
	}
	_, bound, _ := r.Reclaim(ctx)
	if bound != 150 {
		t.Fatalf("bound after all closed = %d, want 150", bound)
	}
	if _, err := r.Replay(ctx, idHigh, 150); err == nil {
		t.Fatal("reclaimed record unexpectedly replayable")
	}
}
