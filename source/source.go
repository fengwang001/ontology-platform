package source

import (
	"context"
	"errors"
	"io"
	"sync/atomic"
	"time"
)

// Item 是数据源产出的一条原始数据。
type Item struct {
	Offset int64
	Data   []byte
}

// Func 是可注入的数据源函数：返回下一条数据。
// 返回 io.EOF 表示正常结束；其余 error 表示中途报错。
type Func func(ctx context.Context, offset int64) (Item, error)

// ErrEnded 可由注入函数返回来模拟“提前结束”。
var ErrEnded = errors.New("source: ended early")

// Source 按 offset 顺序产出数据，支持产出速率限制、
// 中途报错、提前结束，并统计下游满导致的阻塞次数。
type Source struct {
	fn      Func
	rate    time.Duration // 每条产出之间的等待；0 表示全速
	blocked atomic.Int64
}

// New 构造数据源。rate>0 时每条产出前等待 rate。
func New(fn Func, rate time.Duration) *Source {
	return &Source{fn: fn, rate: rate}
}

// Blocked 返回因下游队列满而阻塞的次数。
func (s *Source) Blocked() int64 { return s.blocked.Load() }

// Run 从 offset=from 开始顺序产出到有界 channel out，直到 fn 返回
// io.EOF / ErrEnded（正常结束）、fn 返回其他错误（中途报错）、或 ctx 被取消。
// out 满时阻塞并累加阻塞次数，背压由此传导回注入的数据源函数。
func (s *Source) Run(ctx context.Context, from int64, out chan<- Item) error {
	for offset := from; ; offset++ {
		if s.rate > 0 {
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(s.rate):
			}
		}
		item, err := s.fn(ctx, offset)
		if err != nil {
			if errors.Is(err, ErrEnded) || errors.Is(err, io.EOF) {
				return nil
			}
			return err
		}
		s.send(ctx, out, item)
		if ctx.Err() != nil {
			return ctx.Err()
		}
	}
}

func (s *Source) send(ctx context.Context, out chan<- Item, item Item) {
	select {
	case out <- item:
	default:
		s.blocked.Add(1)
		select {
		case out <- item:
		case <-ctx.Done():
		}
	}
}
