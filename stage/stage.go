// Package stage 提供管线阶段的通用骨架：有界队列、背压、优雅停止，
// 并在 NewPipe 中装配 source→parse→聚合→sink 的完整链路。
package stage

import (
	"context"
	"errors"
	"io"
	"sync"
	"sync/atomic"

	"ontology/source"
)

// 可判定错误。
var (
	ErrTooManyGroups = errors.New("stage: group limit exceeded")
	ErrCrashed       = errors.New("stage: simulated crash")
)

// CrashAt 配置三个强制崩溃时刻。
type CrashAt struct {
	SourceDrained bool // source 读完但队列非空
	MidAggregate  bool // 聚合中途
	PreRename     bool // sink 临时文件写好但未 rename（由子进程承载真实退出）
}

// Config 是管线配置。
type Config struct {
	Src       source.Source
	OutDir    string
	CkptDir   string
	QueueCap  int   // 每级有界队列容量
	Epoch     int   // 每多少条记录一个检查点屏障
	Workers   int   // sink 并行 worker 数
	SinkDelay int64 // 每条记录的模拟下游延迟（纳秒）
	MaxGroups int   // 内存分组硬上限，<=0 表示不限
	Crash     CrashAt
}

// Report 是一次运行的报告。
type Report struct {
	Pos         int64
	Bad         int64
	MaxInFlight int64
	Blocked     int64
	OutputPath  string
	OutputBytes []byte
	Err         error
	FellBack    bool
}

// msg 是阶段间统一消息。barrier 非 0 表示屏障（携带位置）。
type msg struct {
	pos     int64
	key     string
	val     int64
	barrier int64
}

type inFlight struct {
	cur int64
	max int64
}

func (f *inFlight) add(delta int64) int64 {
	c := atomic.AddInt64(&f.cur, delta)
	for {
		m := atomic.LoadInt64(&f.max)
		if c <= m || atomic.CompareAndSwapInt64(&f.max, m, c) {
			break
		}
	}
	return c
}

func (f *inFlight) maximum() int64 { return atomic.LoadInt64(&f.max) }

func (f *inFlight) current() int64 { return atomic.LoadInt64(&f.cur) }

// Pipe 是一条可重复 Run（恢复）的管线。
type Pipe struct {
	cfg Config
	in  inFlight

	blocked atomic.Int64

	stopOnce sync.Once
	stopped  atomic.Bool

	kill chan struct{}
}

// NewPipe 创建管线。
func NewPipe(cfg Config) *Pipe {
	if cfg.QueueCap <= 0 {
		cfg.QueueCap = 1
	}
	if cfg.Epoch <= 0 {
		cfg.Epoch = 1
	}
	if cfg.Workers <= 0 {
		cfg.Workers = 1
	}
	return &Pipe{cfg: cfg, kill: make(chan struct{})}
}

// MaxInFlightBound 返回历史在途数的理论硬上界。
func (p *Pipe) MaxInFlightBound() int64 {
	return int64(2*p.cfg.QueueCap + p.cfg.Workers + 4)
}

// Blocked 返回 source 因背压被阻塞的发送次数。
func (p *Pipe) Blocked() int64 { return p.blocked.Load() }

// Stop 请求优雅停止，幂等；允许在 Run 之前调用。
func (p *Pipe) Stop() {
	p.stopOnce.Do(func() {
		p.stopped.Store(true)
		close(p.kill)
	})
}

// bsend 向有界通道发送：记录阻塞与在途数，崩溃/停止时立即返回。
func (p *Pipe) bsend(ctx context.Context, ch chan<- msg, m msg) error {
	select {
	case ch <- m:
		return nil
	case <-p.kill:
		return ErrCrashed
	case <-ctx.Done():
		return ctx.Err()
	default:
	}
	p.blocked.Add(1)
	select {
	case ch <- m:
		return nil
	case <-p.kill:
		return ErrCrashed
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (p *Pipe) feeder(ctx context.Context, q1 chan<- msg, start int64,
	wg *sync.WaitGroup, errb *errBox) {
	defer wg.Done()
	defer close(q1)
	var count int64
	emit := func(m msg) bool {
		p.in.add(1)
		if err := p.bsend(ctx, q1, m); err != nil {
			errb.set(err)
			return false
		}
		count++
		return true
	}
	for !p.stopped.Load() {
		f, err := p.cfg.Src.Next(ctx)
		if err == io.EOF || p.stopped.Load() {
			if err == io.EOF && p.cfg.Crash.SourceDrained && p.in.current() > 0 {
				errb.set(ErrCrashed)
				p.Stop()
				return
			}
			if count > 0 {
				emit(msg{barrier: start + count})
			}
			return
		}
		if err != nil {
			if count > 0 {
				emit(msg{barrier: start + count})
			}
			errb.set(err)
			return
		}
		if !emit(msg{pos: f.Pos, key: string(f.Data)}) {
			return
		}
		if count%int64(p.cfg.Epoch) == 0 {
			if !emit(msg{barrier: start + count}) {
				return
			}
		}
	}
}
