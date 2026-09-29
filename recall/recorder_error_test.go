package recall

import (
	"bytes"
	"context"
	"errors"
	"testing"
)

// 四类非法输入必须返回互不相同的可判定错误，失败不改变任何状态；被拒后仍可正常使用。
func TestIllegalInputsRejectedAndStateUnchanged(t *testing.T) {
	ctx := context.Background()
	logger, logs := testLogger()
	r := New(1, logger) // 快照上限 1
	appendN(t, ctx, r, 2)
	id, wm := mustOpen(t, ctx, r)
	if wm != 2 {
		t.Fatalf("watermark = %d, want 2", wm)
	}

	type tc struct {
		name string
		call func() error
		want error
	}
	cases := []tc{
		{"replay out of bound", func() error {
			_, err := r.Replay(ctx, id, 3)
			return err
		}, ErrReplayOutOfBound},
		{"replay on unknown snapshot", func() error {
			_, err := r.Replay(ctx, id+999, 1)
			return err
		}, ErrSnapshotClosed},
		{"close unknown snapshot", func() error {
			return r.CloseSnapshot(ctx, id+999)
		}, ErrCloseUnknownSnapshot},
		{"open beyond limit", func() error {
			_, _, err := r.OpenSnapshot(ctx)
			return err
		}, ErrTooManySnapshots},
	}

	seen := map[error]bool{}
	for _, c := range cases {
		err := c.call()
		if !errors.Is(err, c.want) {
			t.Fatalf("%s: err = %v, want %v", c.name, err, c.want)
		}
		seen[c.want] = true
	}
	if len(seen) != 4 {
		t.Fatalf("四类错误必须互不相同，去重后 %d 个: %v", len(seen), seen)
	}

	// 失败不改变状态：上界仍为最小水位 2；历史记录完整可读；仍无法再打开快照。
	if b := r.ReclaimBound(ctx); b != 2 {
		t.Fatalf("bound after rejections = %d, want 2", b)
	}
	if rec, err := r.Replay(ctx, id, 1); err != nil || string(rec.Payload) != "r1" {
		t.Fatalf("state changed after rejection: %+v,%v", rec, err)
	}
	if _, _, err := r.OpenSnapshot(ctx); !errors.Is(err, ErrTooManySnapshots) {
		t.Fatalf("limit state changed: %v", err)
	}

	// 被拒后仍可正常使用：关闭 -> 打开新快照 -> 回收 -> 追加 -> 重放。
	if err := r.CloseSnapshot(ctx, id); err != nil {
		t.Fatalf("CloseSnapshot after rejections: %v", err)
	}
	id2, _ := mustOpen(t, ctx, r)
	if err := r.CloseSnapshot(ctx, id2); err != nil {
		t.Fatalf("CloseSnapshot id2: %v", err)
	}
	if _, bound, _ := r.Reclaim(ctx); bound != 2 {
		t.Fatalf("bound after recovery usage = %d, want 2", bound)
	}
	seq, _ := r.Append(ctx, []byte("ok"))
	id3, _ := mustOpen(t, ctx, r)
	if rec, err := r.Replay(ctx, id3, seq); err != nil || string(rec.Payload) != "ok" {
		t.Fatalf("replay after recovery = %+v,%v", rec, err)
	}

	out := logs.String()
	for _, want := range []string{
		"replay sequence exceeds snapshot watermark",
		"snapshot does not exist or is closed",
		"cannot close unknown snapshot",
		"snapshot limit exceeded",
		"state unchanged",
	} {
		if !bytes.Contains([]byte(out), []byte(want)) {
			t.Fatalf("日志缺少拒绝原因 %q:\n%s", want, out)
		}
	}
}

// 回收位点判定优先于越界：已回收位点即使超过快照水位，也报 ErrReplayReclaimed。
func TestReplayReclaimedVsOutOfBound(t *testing.T) {
	ctx := context.Background()
	r := New(0, discardLogger())
	appendN(t, ctx, r, 3)
	idLow, _ := mustOpen(t, ctx, r)
	appendN(t, ctx, r, 2)
	idHigh, _ := mustOpen(t, ctx, r)
	if err := r.CloseSnapshot(ctx, idHigh); err != nil {
		t.Fatal(err)
	}
	if _, bound, _ := r.Reclaim(ctx); bound != 3 {
		t.Fatalf("bound = %d, want 3", bound)
	}
	if _, err := r.Replay(ctx, idLow, 2); !errors.Is(err, ErrReplayReclaimed) {
		t.Fatalf("err = %v, want ErrReplayReclaimed", err)
	}
	if _, err := r.Replay(ctx, idLow, 0); !errors.Is(err, ErrReplayReclaimed) {
		t.Fatalf("seq=0 err = %v, want ErrReplayReclaimed", err)
	}
	if _, err := r.Replay(ctx, idLow, 5); !errors.Is(err, ErrReplayOutOfBound) {
		t.Fatalf("err = %v, want ErrReplayOutOfBound", err)
	}
}

// 上限边界：达到上限后打开被拒，关闭一个后立刻又可以打开；状态不被失败调用改变。
func TestSnapshotLimit(t *testing.T) {
	ctx := context.Background()
	r := New(2, discardLogger())
	id1, _ := mustOpen(t, ctx, r)
	id2, _ := mustOpen(t, ctx, r)
	if _, _, err := r.OpenSnapshot(ctx); !errors.Is(err, ErrTooManySnapshots) {
		t.Fatalf("third open err = %v, want ErrTooManySnapshots", err)
	}
	if err := r.CloseSnapshot(ctx, id1); err != nil {
		t.Fatal(err)
	}
	id3, _ := mustOpen(t, ctx, r)
	if err := r.CloseSnapshot(ctx, id2); err != nil {
		t.Fatal(err)
	}
	if err := r.CloseSnapshot(ctx, id3); err != nil {
		t.Fatal(err)
	}
	if err := r.CloseSnapshot(ctx, 0); !errors.Is(err, ErrCloseUnknownSnapshot) {
		t.Fatalf("close id=0 err = %v, want ErrCloseUnknownSnapshot", err)
	}
}
