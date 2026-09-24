// Package source 是可注入的数据源：可配置产出上限、延迟与中途报错。
package source

import (
	"errors"
	"fmt"
	"sync/atomic"
	"time"

	"ontology/internal/stage"
)

// ErrMidStream 在配置 FailAfter 命中时返回。
var ErrMidStream = errors.New("source: mid-stream failure")

// ErrStopped 在停止信号到达、未能完成产出时返回。
var ErrStopped = errors.New("source: stopped")

// Config 配置数据源。Limit 为总产出数；Delay 为每条产出后间隔；
// FailAfter>0 表示成功产出 FailAfter 条后返回 ErrMidStream。
type Config struct {
	Limit     int
	Delay     time.Duration
	FailAfter int
}

// Source 按 1 起单调序号产出原始字节，键在 a/b/c 间循环。
type Source struct {
	cfg     Config
	out     chan<- stage.Msg[[]byte]
	blocked atomic.Int64
	seq     atomic.Int64
}

// New 构造数据源，out 为下游入口。
func New(cfg Config, out chan<- stage.Msg[[]byte]) *Source {
	return &Source{cfg: cfg, out: out}
}

// Blocked 返回 Feed 中被下游背压阻塞的次数。
func (s *Source) Blocked() int64 { return s.blocked.Load() }

// Seq 返回已分配的最大序号。
func (s *Source) Seq() int64 { return s.seq.Load() }

func keyFor(seq int64) string {
	return []string{"a", "b", "c"}[seq%3]
}

// Feed 产出全部记录并在末尾发尾屏障（携带最终序号）。
// 中途报错同样发尾屏障，让下游把已产出部分排空、落盘、写检查点。
func (s *Source) Feed(stop <-chan struct{}) error {
	n := s.cfg.Limit
	fail := s.cfg.FailAfter > 0 && s.cfg.FailAfter <= n
	if fail {
		n = s.cfg.FailAfter
	}
	for i := 0; i < n; i++ {
		seq := s.seq.Add(1)
		rec := []byte(fmt.Sprintf("%s=%d", keyFor(seq), seq))
		if !s.send(stage.Msg[[]byte]{Seq: seq, V: rec}, stop) {
			return ErrStopped
		}
		if s.cfg.Delay > 0 {
			select {
			case <-time.After(s.cfg.Delay):
			case <-stop:
				return ErrStopped
			}
		}
	}
	// 尾屏障：即便失败也发出，保证下游静止点能完成。
	s.send(stage.Msg[[]byte]{Seq: s.seq.Load(), Barrier: true}, stop)
	if fail {
		return ErrMidStream
	}
	return nil
}

func (s *Source) send(m stage.Msg[[]byte], stop <-chan struct{}) bool {
	// 先试非阻塞发送；失败即记一次背压，再阻塞等待。
	select {
	case s.out <- m:
		return true
	default:
	}
	s.blocked.Add(1)
	select {
	case s.out <- m:
		return true
	case <-stop:
		return false
	}
}
