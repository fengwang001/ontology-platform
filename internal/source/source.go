// Package source 是可注入的记录数据源：支持速率、坏行、中途报错与提前结束。
package source

import (
	"context"
	"fmt"
	"strconv"
	"sync/atomic"
	"time"

	"ontology/internal/stage"
)

// Msg 是 source 输出的一条消息；Barrier 为全局屏障标记，不占序号。
type Msg struct {
	Off     int
	Data    []byte
	Barrier bool
}

// Config 描述数据源行为。
type Config struct {
	N            int           // 正常记录总条数
	Keys         int           // key 种类数（k0..kKeys-1），为 0 时全部同组
	BadEvery     int           // 每隔多少条注入一条坏记录（0 关闭）
	Rate         time.Duration // 每条记录产出前的等待（0 为全速）
	FailAt       int           // 产出该条数后返回错误（<0 关闭）
	EarlyEndAt   int           // >0 时产出该条数即正常结束（用于提前结束场景）
	BarrierEvery int           // 每隔多少条记录注入一个屏障
}

// Stats 是 source 运行后的可观测统计。
type Stats struct {
	Produced atomic.Int64
	Blocks   atomic.Int64
}

type Source struct {
	cfg   Config
	stats Stats
}

func New(cfg Config) *Source { return &Source{cfg: cfg} }

// Stats 返回运行统计（停止后读取）。
func (s *Source) Stats() *Stats { return &s.stats }

// Run 把记录泵入 out，直到 EOF、发生错误或 ctx 取消。返回已产出条数与错误。
// 队列满时先做一次非阻塞尝试，失败则阻塞计数 +1，背压由此真实传导。
func (s *Source) Run(ctx context.Context, out *stage.Queue[Msg]) (int, error) {
	total := s.cfg.N
	if s.cfg.EarlyEndAt > 0 && s.cfg.EarlyEndAt < total {
		total = s.cfg.EarlyEndAt
	}
	produced := 0
	flushBarrier := func(off int) error {
		return out.Send(ctx, Msg{Off: off, Barrier: true})
	}
	for i := 0; i < total; i++ {
		if s.cfg.Rate > 0 {
			select {
			case <-time.After(s.cfg.Rate):
			case <-ctx.Done():
				return produced, stage.ErrStopped
			}
		}
		msg := Msg{Off: i + 1, Data: encode(i, s.cfg.Keys, s.cfg.BadEvery)}
		if !out.TrySend(msg) {
			s.stats.Blocks.Add(1)
			if err := out.Send(ctx, msg); err != nil {
				return produced, err
			}
		}
		produced++
		s.stats.Produced.Store(int64(produced))
		if s.cfg.BarrierEvery > 0 && produced%s.cfg.BarrierEvery == 0 && produced < total {
			if err := flushBarrier(produced); err != nil {
				return produced, err
			}
		}
		if s.cfg.FailAt >= 0 && produced == s.cfg.FailAt {
			return produced, fmt.Errorf("source: injected failure after %d", produced)
		}
	}
	if err := flushBarrier(produced); err != nil {
		return produced, err
	}
	return produced, nil
}

func encode(i, keys, badEvery int) []byte {
	if badEvery > 0 && (i+1)%badEvery == 0 {
		return []byte("broken-row-without-comma")
	}
	key := "k0"
	if keys > 0 {
		key = "k" + strconv.Itoa(i%keys)
	}
	return []byte(key + "," + strconv.Itoa(i+1) + "\n")
}
