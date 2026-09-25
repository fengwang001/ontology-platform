// Package stage 提供有界队列阶段骨架与整条聚合管线的编排。
package stage

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"time"

	"ontology/ckpt"
	"ontology/parse"
	"ontology/sink"
	"ontology/source"
)

// ErrTooManyGroups 表示内存分组数超过硬上限。
var ErrTooManyGroups = errors.New("stage: too many groups")

// Stats 是背压可观测计数（并发安全）。
type Stats struct {
	inflight atomic.Int64
	max      atomic.Int64
	blocked  atomic.Int64
}

func (s *Stats) enter() {
	n := s.inflight.Add(1)
	for {
		m := s.max.Load()
		if n <= m || s.max.CompareAndSwap(m, n) {
			return
		}
	}
}
func (s *Stats) leave()          { s.inflight.Add(-1) }
func (s *Stats) Blocked()        { s.blocked.Add(1) }
func (s *Stats) MaxInflight() int64 { return s.max.Load() }
func (s *Stats) BlockedN()       int64 { return s.blocked.Load() }
func (s *Stats) Inflight()       int64 { return s.inflight.Load() }

// CrashPoint 指定强制崩溃注入点。
type CrashPoint string

const (
	CrashNone      CrashPoint = ""
	CrashQueueFull CrashPoint = "queue" // source 读完、队列非空
	CrashAgg       CrashPoint = "agg"   // 聚合中途
	CrashTmp       CrashPoint = "tmp"   // sink 写完临时文件、改名前
)

// Config 配置管线。
type Config struct {
	Src       source.Source
	Queue     int
	MaxGroups int
	Interval  int64
	Dir       string // 输出与检查点目录
	Crash     CrashPoint
	SlowSink  time.Duration // 每次屏障落盘前的人为延迟
}

// Report 是一次运行的结果报告。
type Report struct {
	Off         int64
	Bad         int64
	MaxInflight int64
	Blocked     int64
	TmpCleaned  int
	FellBack    bool
	Err         error
}

// OutputPath 返回输出文件的标准位置。
func OutputPath(dir string) string { return filepath.Join(dir, "output.dat") }

// Pipeline 是一条可恢复的聚合管线。
type Pipeline struct {
	cfg    Config
	stats  Stats
	store  *ckpt.Store
	groups map[string]*sink.Group
	bad    int64
	off    int64
	once   sync.Once
	stop   chan struct{}
}

// New 恢复检查点、清理临时文件并装配管线，返回初始恢复报告。
func New(cfg Config) (*Pipeline, Report, error) {
	if cfg.Queue < 1 {
		cfg.Queue = 1
	}
	store := ckpt.NewStore(cfg.Dir)
	st, err := store.Load()
	fellBack := store.FellBack
	if err != nil {
		st = ckpt.State{}
	}
	n, _ := sink.CleanTemp(cfg.Dir)
	groups := make(map[string]*sink.Group, len(st.Groups))
	for i := range st.Groups {
		g := st.Groups[i]
		groups[g.Key] = &g
	}
	p := &Pipeline{cfg: cfg, store: store, groups: groups, bad: st.Bad,
		off: st.Off, stop: make(chan struct{})}
	return p, Report{Off: st.Off, Bad: st.Bad, TmpCleaned: n, FellBack: fellBack}, nil
}

// Stats 暴露背压计数。
func (p *Pipeline) StatsView() (max, blocked int64) {
	return p.stats.MaxInflight(), p.stats.BlockedN()
}

// Stop 请求优雅停止：排空在途并提交最后屏障。幂等。
func (p *Pipeline) Stop() { p.once.Do(func() { close(p.stop) }) }

// commit 在屏障处落盘输出并写出检查点；顺序严格为 rename 后 ckpt。
func (p *Pipeline) commit(off int64, crashAtTmp bool) error {
	if p.cfg.SlowSink > 0 {
		time.Sleep(p.cfg.SlowSink)
	}
	rows := make([]sink.Group, 0, len(p.groups))
	for _, g := range p.groups {
		rows = append(rows, *g)
	}
	snap := sink.Snapshot{Off: off, Bad: p.bad, Rows: rows}
	if err := writeWithCrash(OutputPath(p.cfg.Dir), snap, crashAtTmp); err != nil {
		return err
	}
	st := ckpt.State{Off: off, Bad: p.bad, Groups: rows}
	if err := p.store.Save(st); err != nil {
		return err
	}
	p.off = off
	return nil
}

// Run 运行管线直到 source 结束/出错、分组超限、崩溃或被 Stop。
func (p *Pipeline) Run(ctx context.Context) Report {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	raw := make(chan source.Record, p.cfg.Queue)
	items := make(chan source.Item, p.cfg.Queue)
	var wg sync.WaitGroup
	var runErr atomic.Value
	var maxOff int64
	fail := func(e error) {
		if e != nil {
			runErr.CompareAndSwap(nil, e)
			cancel()
		}
	}

	wg.Add(1)
	go func() { // parse
		defer wg.Done()
		defer close(items)
		parse.Run(raw, items, func() { atomic.AddInt64(&p.bad, 1) })
	}()

	wg.Add(1)
	go func() { // aggregate + sink
		defer wg.Done()
		for it := range items {
			p.stats.leave()
			if it.IsBarrier() {
				if p.cfg.Crash == CrashAgg {
					crashIf(it.Off > 1 && p.stats.Inflight() > 0)
				}
				if err := p.commit(it.Off, p.cfg.Crash == CrashTmp); err != nil {
					fail(err)
					return
				}
				continue
			}
			g, ok := p.groups[it.Key]
			if !ok {
				if p.cfg.MaxGroups > 0 && len(p.groups) >= p.cfg.MaxGroups {
					fail(ErrTooManyGroups)
					return
				}
				g = &sink.Group{Key: it.Key}
				p.groups[it.Key] = g
			}
			g.Sum += it.Val
			g.Cnt++
		}
	}()

	var produced int64
srcLoop:
	for {
		select {
		case <-ctx.Done():
			break srcLoop
		case <-p.stop:
			break srcLoop
		default:
		}
		rec, err := p.cfg.Src.Read(ctx)
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			break srcLoop
		}
		if err != nil {
			fail(err)
			break srcLoop
		}
		produced++
		maxOff = rec.Off
		if !p.sendRaw(ctx, raw, rec) {
			break srcLoop
		}
		p.stats.enter()
		if p.cfg.Interval > 0 && produced%p.cfg.Interval == 0 {
			if p.cfg.Crash == CrashQueueFull {
				crashIf(p.stats.Inflight() > 0)
			}
			select {
			case raw <- source.Record{Off: rec.Off}:
			case <-ctx.Done():
				break srcLoop
			}
		}
	}
	close(raw)
	wg.Wait()
	if e := runErr.Load(); e != nil {
		return p.report(e.(error))
	}
	if maxOff > p.off {
		if err := p.commit(maxOff, false); err != nil {
			return p.report(err)
		}
	}
	return p.report(nil)
}

// sendRaw 向有界队列投递；满了先记一次阻塞再等待。
func (p *Pipeline) sendRaw(ctx context.Context, out chan<- source.Record, rec source.Record) bool {
	select {
	case out <- rec:
		return true
	default:
		p.stats.Blocked()
		select {
		case out <- rec:
			return true
		case <-ctx.Done():
			return false
		case <-p.stop:
			return false
		}
	}
}

func (p *Pipeline) report(err error) Report {
	return Report{
		Off: p.off, Bad: atomic.LoadInt64(&p.bad),
		MaxInflight: p.stats.MaxInflight(), Blocked: p.stats.BlockedN(),
		Err: err,
	}
}

// crashIf 在注入条件满足时以进程退出模拟硬崩溃。
func crashIf(cond bool) {
	if cond {
		os.Exit(99)
	}
}

func writeWithCrash(path string, snap sink.Snapshot, crashAtTmp bool) error {
	if !crashAtTmp {
		return sink.Write(path, snap)
	}
	// 在唯一一次带崩溃标记的提交里，写完临时文件并 Sync 后退出。
	return sink.WriteCrashTmp(path, snap)
}
