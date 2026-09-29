package recall

import (
	"bytes"
	"context"
	"errors"
	"testing"
)

// 追加与水位：序号从 1 递增；打开快照时水位固定为当前序号，之后追加不可见。
func TestAppendAndSnapshotWatermark(t *testing.T) {
	ctx := context.Background()
	logger, logs := testLogger()
	r := New(0, logger)

	appendN(t, ctx, r, 3)
	id, wm := mustOpen(t, ctx, r)
	if wm != 3 {
		t.Fatalf("watermark = %d, want 3", wm)
	}

	if seq, _ := r.Append(ctx, []byte("later")); seq != 4 {
		t.Fatalf("seq = %d, want 4", seq)
	}
	if _, err := r.Replay(ctx, id, 4); !errors.Is(err, ErrReplayOutOfBound) {
		t.Fatalf("Replay seq=4 err = %v, want ErrReplayOutOfBound", err)
	}
	rec, err := r.Replay(ctx, id, 1)
	if err != nil {
		t.Fatalf("Replay seq=1 err = %v", err)
	}
	if string(rec.Payload) != "r1" || rec.Seq != 1 {
		t.Fatalf("replay = %+v, want seq=1 payload=r1", rec)
	}

	out := logs.String()
	for _, want := range []string{"recall.append", "recall.open_snapshot", "watermark=3", "result=rejected", "result=replayed", "decision="} {
		if !bytes.Contains([]byte(out), []byte(want)) {
			t.Fatalf("日志缺少判定字段 %q\n%s", want, out)
		}
	}
}

// 回收含等号边界：序号恰好等于最小水位的记录也被回收，之后不可再重放。
func TestReclaimInclusiveBoundary(t *testing.T) {
	ctx := context.Background()
	logger, logs := testLogger()
	r := New(0, logger)
	appendN(t, ctx, r, 5)

	idLow, wmLow := mustOpen(t, ctx, r)
	appendN(t, ctx, r, 3)
	idHigh, wmHigh := mustOpen(t, ctx, r)
	if wmLow != 5 || wmHigh != 8 {
		t.Fatalf("watermarks = %d,%d, want 5,8", wmLow, wmHigh)
	}

	if b := r.ReclaimBound(ctx); b != 5 {
		t.Fatalf("bound = %d, want min watermark 5", b)
	}
	n, bound, err := r.Reclaim(ctx)
	if err != nil || bound != 5 || n != 5 {
		t.Fatalf("Reclaim = (%d,%d,%v), want (5,5,nil)", n, bound, err)
	}

	// 等号位点 5 已永久不可重放（对两个快照都如此）。
	if _, err := r.Replay(ctx, idLow, 5); !errors.Is(err, ErrReplayReclaimed) {
		t.Fatalf("Replay seq=5 err = %v, want ErrReplayReclaimed", err)
	}
	if _, err := r.Replay(ctx, idHigh, 5); !errors.Is(err, ErrReplayReclaimed) {
		t.Fatalf("Replay seq=5 on high snap err = %v, want ErrReplayReclaimed", err)
	}
	// 紧邻边界的 6 仍可重放（高水位快照可见）。
	if rec, err := r.Replay(ctx, idHigh, 6); err != nil || string(rec.Payload) != "r6" {
		t.Fatalf("Replay seq=6 = %+v, %v", rec, err)
	}
	// 再次回收：上界仍为 5，不产生新回收且水位不回退。
	n2, bound2, _ := r.Reclaim(ctx)
	if n2 != 0 || bound2 != 5 {
		t.Fatalf("second Reclaim = (%d,%d), want (0,5)", n2, bound2)
	}

	if out := logs.String(); !bytes.Contains([]byte(out), []byte("bound=5")) ||
		!bytes.Contains([]byte(out), []byte("inclusively")) {
		t.Fatalf("日志缺少含等号回收判定依据:\n%s", out)
	}
}

// 无活跃快照：回收上界为当前最大序号，全部回收。
func TestReclaimAllWhenNoSnapshot(t *testing.T) {
	ctx := context.Background()
	logger, logs := testLogger()
	r := New(0, logger)
	appendN(t, ctx, r, 4)

	if b := r.ReclaimBound(ctx); b != 4 {
		t.Fatalf("bound with no snapshots = %d, want 4", b)
	}
	n, bound, err := r.Reclaim(ctx)
	if err != nil || n != 4 || bound != 4 {
		t.Fatalf("Reclaim = (%d,%d,%v), want (4,4,nil)", n, bound, err)
	}
	// 全部回收后再追加，位点 5 可用，历史位点不可复活。
	seq, _ := r.Append(ctx, []byte("fresh"))
	if seq != 5 {
		t.Fatalf("seq after full reclaim = %d, want 5", seq)
	}
	id, _ := mustOpen(t, ctx, r)
	if _, err := r.Replay(ctx, id, 4); !errors.Is(err, ErrReplayReclaimed) {
		t.Fatalf("reclaimed seq=4 replayable: %v", err)
	}
	if rec, err := r.Replay(ctx, id, 5); err != nil || string(rec.Payload) != "fresh" {
		t.Fatalf("Replay seq=5 = %+v,%v", rec, err)
	}
	if out := logs.String(); !bytes.Contains([]byte(out), []byte("reclaimed=4")) {
		t.Fatalf("日志缺少全量回收结果:\n%s", out)
	}
}

// 关闭最小水位快照后，上界按剩余活跃快照水位重估；全部关闭后上界推进到最大序号。
func TestCloseReevaluatesMinWatermark(t *testing.T) {
	ctx := context.Background()
	logger, logs := testLogger()
	r := New(0, logger)
	appendN(t, ctx, r, 2)
	id2, wm2 := mustOpen(t, ctx, r)
	appendN(t, ctx, r, 2)
	id4, wm4 := mustOpen(t, ctx, r)
	if wm2 != 2 || wm4 != 4 {
		t.Fatalf("watermarks = %d,%d, want 2,4", wm2, wm4)
	}
	if b := r.ReclaimBound(ctx); b != 2 {
		t.Fatalf("bound = %d, want 2", b)
	}

	if err := r.CloseSnapshot(ctx, id2); err != nil {
		t.Fatalf("CloseSnapshot: %v", err)
	}
	if b := r.ReclaimBound(ctx); b != 4 {
		t.Fatalf("bound after close = %d, want 4", b)
	}
	if err := r.CloseSnapshot(ctx, id4); err != nil {
		t.Fatalf("CloseSnapshot: %v", err)
	}
	if b := r.ReclaimBound(ctx); b != 4 {
		t.Fatalf("bound after all closed = %d, want 4", b)
	}
	n, bound, _ := r.Reclaim(ctx)
	if n != 4 || bound != 4 {
		t.Fatalf("Reclaim = (%d,%d), want (4,4)", n, bound)
	}
	if _, err := r.Replay(ctx, id4, 4); !errors.Is(err, ErrSnapshotClosed) {
		t.Fatalf("replay on closed snapshot err = %v, want ErrSnapshotClosed", err)
	}
	if out := logs.String(); !bytes.Contains([]byte(out), []byte("min watermark recomputed")) {
		t.Fatalf("日志缺少最小水位重估判定:\n%s", out)
	}
}
