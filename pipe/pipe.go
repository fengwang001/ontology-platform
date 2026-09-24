// Package pipe 编排 source→parse→聚合→落盘 的有背压流式管线并支持恢复。
package pipe

import (
	"context"
	"errors"
	"os"
	"sync"
	"time"

	"ontology/ckpt"
	"ontology/parse"
	"ontology/sink"
	"ontology/source"
	"ontology/stage"
)

// ErrTooManyGroups 表示内存分组数超过硬上限（不静默丢组）。
var ErrTooManyGroups = errors.New("pipe: too many groups")

// Config 是管线配置。
type Config struct {
	Dir                 string
	Source              source.Resumable
	Barrier             int64 // 每多少条数据落一个周期屏障
	Q1, Q2              int   // 两级有界队列容量（>=1）
	MaxGroups           int
	RecordDelay         time.Duration // 每条记录的 sink 人为延迟
	SinkErr             error         // 首次提交即返回该错（sink 立刻报错）
	CrashAggRecords     int64         // 聚合处理够该条数后立即崩溃
	CrashBeforeRename   int           // 接下来 n 次快照写临时文件后、改名前崩溃
	CrashAtFinalBarrier bool          // source 读完且队列非空时立即崩溃
}

// Report 是一次运行的结果。
type Report struct {
	Records    int64
	BadRecords int64
	MaxFlight  int
	Blocked    int64
	Groups     int
	Offset     int64
	FellBack   bool
	Err        error
}

// Pipe 是管线句柄。
type Pipe struct {
	cfg      Config
	stopOnce sync.Once
	stopped  chan struct{}
	rep      Report
}

// New 创建管线（尚未运行）。
func New(cfg Config) *Pipe {
	return &Pipe{cfg: cfg, stopped: make(chan struct{})}
}

// Stop 请求优雅停止；重复调用幂等。停止信号在启动前到达同样安全。
func (p *Pipe) Stop() { p.stopOnce.Do(func() { close(p.stopped) }) }

// Run 执行管线直到 source 结束 / 出错 / 停止，返回最终报告。
func (p *Pipe) Run(ctx context.Context) Report {
	fatal, cancelFatal := context.WithCancel(ctx)
	defer cancelFatal()

	sk, err := sink.New(p.cfg.Dir)
	if err != nil {
		p.rep.Err = err
		return p.rep
	}
	if p.cfg.CrashBeforeRename > 0 {
		sk.CrashBeforeRename = p.cfg.CrashBeforeRename
	}
	store := ckpt.New(p.cfg.Dir)
	res, err := store.Recover()
	if err != nil {
		p.rep.Err = err
		return p.rep
	}
	p.rep.FellBack = res.FellBack
	st := res.State
	if st.Groups == nil {
		st.Groups = map[string]sink.Group{}
	}
	p.cfg.Source.SeekTo(st.Offset)

	q1 := stage.NewQueue[source.Item](p.cfg.Q1)
	q2 := stage.NewQueue[parse.Envelope](p.cfg.Q2)
	gauge := stage.NewGauge(q1, q2)
	var mu sync.Mutex
	var terminal error
	setErr := func(e error) {
		mu.Lock()
		if terminal == nil {
			terminal = e
		}
		cancelFatal()
		mu.Unlock()
	}

	// sendCtx：fatal（崩溃类错误）或外部 ctx 取消时失败；优雅停止不取消，
	// 保证停止时仍可把尾屏障送入队列。
	sendCtx := fatal
	var wg sync.WaitGroup
	wg.Add(3)

	// source：产出数据并注入周期屏障；出错/结束/停止时尝试补尾屏障。
	go func() {
		defer wg.Done()
		defer q1.Close()
		count := st.Offset
		every := barrierEvery(p.cfg.Source)
		for {
			select {
			case <-p.stopped:
				if count > st.Offset {
					q1.Send(sendCtx, source.Item{Offset: count, Barrier: count})
				}
				return
			default:
			}
			it, ok, rerr := p.cfg.Source.Next()
			if rerr != nil {
				setErr(rerr) // 已在途数据由下游排空；本次新数据止于 count
				if count > st.Offset {
					q1.Send(sendCtx, source.Item{Offset: count, Barrier: count})
				}
				return
			}
			if !ok {
				if p.cfg.CrashAtFinalBarrier && q1.Len() > 0 {
					os.Exit(42)
				}
				if count > st.Offset {
					q1.Send(sendCtx, source.Item{Offset: count, Barrier: count})
				}
				return
			}
			if !q1.Send(sendCtx, it) {
				return
			}
			count = it.Offset
			gauge.Sample()
			if every > 0 && it.Offset%every == 0 {
				if !q1.Send(sendCtx, source.Item{Offset: it.Offset, Barrier: it.Offset}) {
					return
				}
			}
		}
	}()

	// parse：坏记录计数，不中断。
	go func() {
		defer wg.Done()
		defer q2.Close()
		for it := range q1.Ch() {
			env, good := parse.Convert(it)
			if !good {
				mu.Lock()
				p.rep.BadRecords++
				mu.Unlock()
			}
			if !q2.Send(fatal, env) {
				return
			}
			gauge.Sample()
		}
	}()

	// aggregate/sink：单 worker，FIFO，保证屏障前所有记录已入 map。
	go func() {
		defer wg.Done()
		processed := int64(0)
		committed := 0
		commit := func(barrier int64) error {
			if p.cfg.SinkErr != nil && committed == 0 {
				return p.cfg.SinkErr
			}
			committed++
			st.Offset = barrier
			if err := sk.Commit(st); err != nil {
				if errors.Is(err, sink.ErrCrashSimulated) {
					os.Exit(42)
				}
				return err
			}
			return store.Save(st)
		}
		for env := range q2.Ch() {
			if env.Barrier > 0 {
				if err := commit(env.Barrier); err != nil {
					setErr(err)
					return
				}
				continue
			}
			if env.Bad {
				continue
			}
			if _, known := st.Groups[env.Rec.Key]; !known &&
				p.cfg.MaxGroups > 0 && len(st.Groups) >= p.cfg.MaxGroups {
				setErr(ErrTooManyGroups)
				return
			}
			g := st.Groups[env.Rec.Key]
			if g.N == 0 || env.Rec.Value < g.Min {
				g.Min = env.Rec.Value
			}
			if env.Rec.Value > g.Max {
				g.Max = env.Rec.Value
			}
			g.Sum += env.Rec.Value
			g.N++
			st.Groups[env.Rec.Key] = g
			processed++
			p.rep.Records++
			if p.cfg.RecordDelay > 0 {
				time.Sleep(p.cfg.RecordDelay)
			}
			if p.cfg.CrashAggRecords > 0 && processed >= p.cfg.CrashAggRecords {
				os.Exit(42)
			}
		}
	}()

	wg.Wait()
	p.rep.Err = terminal
	p.rep.MaxFlight = gauge.Max()
	p.rep.Blocked = q1.Blocked() + q2.Blocked()
	p.rep.Groups = len(st.Groups)
	p.rep.Offset = st.Offset
	return p.rep
}

func barrierEvery(src source.Source) int64 {
	if m, ok := src.(*source.Mem); ok {
		return m.BarrierEvery()
	}
	return 0
}
