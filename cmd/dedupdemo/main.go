// dedupdemo 用一串构造好的事件演示事件时间去重窗口，
// 运行时可在日志中看到每个事件的输入、新/重复判定及判定依据。
//
//	go run ./cmd/dedupdemo
package main

import (
	"log/slog"
	"os"
	"time"

	"ontology/dedup"
)

func main() {
	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))

	d, err := dedup.New(dedup.Config{
		TTL:        10 * time.Minute,
		MaxEntries: 4,
		Logger:     logger,
	})
	if err != nil {
		logger.Error("create deduper failed", "err", err)
		os.Exit(1)
	}

	base := time.Date(2026, 9, 28, 10, 0, 0, 0, time.UTC)
	at := func(n time.Duration) time.Time { return base.Add(n) }

	events := []dedup.Event{
		{ID: "evt-1", Time: at(0)},                // 新事件
		{ID: "evt-2", Time: at(1 * time.Minute)},  // 新事件
		{ID: "evt-1", Time: at(2 * time.Minute)},  // 重复：丢弃，不刷新 evt-1 的记忆
		{ID: "evt-3", Time: at(30 * time.Second)}, // 迟到新事件：不丢弃
		{ID: "evt-1", Time: at(9 * time.Minute)},  // evt-1 记忆仍存活 -> 重复
		{ID: "evt-4", Time: at(10 * time.Minute)}, // 推进水位线到边界，evt-1 恰好过期清除
		{ID: "evt-1", Time: at(11 * time.Minute)}, // 已过期 -> 重新作为新事件
		{ID: "", Time: at(12 * time.Minute)},      // 非法：空标识，拒绝且无任何状态变更
	}

	for _, e := range events {
		res, err := d.Process(e)
		if err != nil {
			logger.Warn("process rejected", "id", e.ID, "err", err)
			continue
		}
		logger.Info("process result",
			"id", e.ID,
			"accepted", res.Accepted,
			"duplicate", res.Duplicate,
			"watermark", res.Watermark.UTC().Format(time.RFC3339Nano),
			"expired_ids", res.ExpiredIDs,
		)
	}

	s := d.Snapshot()
	logger.Info("final snapshot",
		"watermark", s.Watermark.UTC().Format(time.RFC3339Nano),
		"memories", len(s.Memories),
		"accepted", s.AcceptedCount,
		"duplicates", s.DuplicateCount,
	)
}
