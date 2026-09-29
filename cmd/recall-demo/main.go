// recall-demo 演示按水位回收的撤回日志回收器：
// 追加、打开/关闭快照、含等号回收与各类拒绝，并将输入、结果与判定依据写入日志。
package main

import (
	"context"
	"errors"
	"log/slog"
	"os"

	"ontology/recall"
)

func main() {
	ctx := context.Background()
	logger := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelDebug}))

	// 快照上限 2，用于演示 ErrTooManySnapshots。
	r := recall.New(2, logger)

	mustAppend := func(payload string) int64 {
		seq, err := r.Append(ctx, []byte(payload))
		if err != nil {
			logger.Error("append failed", "err", err)
			os.Exit(1)
		}
		return seq
	}

	// 1) 追加 3 条后打开低水位快照（水位=3，含等号回收边界）。
	mustAppend("undo-1")
	mustAppend("undo-2")
	mustAppend("undo-3")
	lowID, lowWM, err := r.OpenSnapshot(ctx)
	must(err, "open low snapshot")
	logger.Info("low snapshot opened", "id", lowID, "watermark", lowWM)

	// 2) 再追加 2 条并打开高水位快照（水位=5）。
	mustAppend("undo-4")
	mustAppend("undo-5")
	highID, highWM, err := r.OpenSnapshot(ctx)
	must(err, "open high snapshot")
	logger.Info("high snapshot opened", "id", highID, "watermark", highWM)

	// 3) 上界 = 最小水位 3；回收 1..3（含等号）。
	n, bound, err := r.Reclaim(ctx)
	must(err, "reclaim")
	logger.Info("reclaimed up to min watermark (inclusive)", "reclaimed", n, "bound", bound)

	// 4) 边界位点 3 永久不可重放；4 仍可被高水位快照读取。
	if _, err = r.Replay(ctx, lowID, 3); !errors.Is(err, recall.ErrReplayReclaimed) {
		logger.Warn("unexpected replay result at boundary", "err", err)
	}
	rec, err := r.Replay(ctx, highID, 4)
	must(err, "replay seq=4")
	logger.Info("replay retained record", "snapshot", highID, "seq", rec.Seq, "payload", string(rec.Payload))

	// 5) 四类非法输入：越界、未知快照重放、关闭未知快照、打开数超上限。
	if _, err = r.Replay(ctx, lowID, 99); !errors.Is(err, recall.ErrReplayOutOfBound) {
		logger.Warn("expected out-of-bound", "err", err)
	}
	if _, err = r.Replay(ctx, 9999, 1); !errors.Is(err, recall.ErrSnapshotClosed) {
		logger.Warn("expected closed snapshot", "err", err)
	}
	if err = r.CloseSnapshot(ctx, 9999); !errors.Is(err, recall.ErrCloseUnknownSnapshot) {
		logger.Warn("expected unknown close", "err", err)
	}
	if _, _, err = r.OpenSnapshot(ctx); !errors.Is(err, recall.ErrTooManySnapshots) {
		logger.Warn("expected limit exceeded", "err", err)
	}

	// 6) 关闭两个快照后无活跃快照，再回收即全部清除。
	must(r.CloseSnapshot(ctx, lowID), "close low snapshot")
	must(r.CloseSnapshot(ctx, highID), "close high snapshot")
	n, bound, err = r.Reclaim(ctx)
	must(err, "final reclaim")
	logger.Info("all snapshots closed; remaining records reclaimed", "reclaimed", n, "bound", bound)
}

func must(err error, what string) {
	if err != nil {
		slog.Default().Error(what+" failed", "err", err)
		os.Exit(1)
	}
}
