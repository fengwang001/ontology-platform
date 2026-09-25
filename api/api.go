// Package api 是谓词下推流水线的对外接口。
package api

import (
	"errors"
	"fmt"
	"slices"
	"sync"

	"ontology/pipe"
	"ontology/pred"
)

// 对外的类型别名与哨兵错误。
type (
	Op    = pipe.Op
	Move  = pipe.Move
	Event = pred.Event
	Out   = pred.Event
)

var (
	ErrBadPredicate = pred.ErrBadPredicate
	ErrBadEvent     = pred.ErrBadEvent
	ErrBarrier      = pipe.ErrBarrier
)

// Pipeline 是已构建下推计划的流水线实例。
type Pipeline struct {
	mu   sync.RWMutex
	plan *pipe.Plan
	out  []Out
}

// New 构建流水线：校验谓词并生成下推计划（段内合并），失败不产出实例。
func New(ops []Op) (*Pipeline, error) {
	plan, err := pipe.Build(ops)
	if err != nil {
		return nil, err
	}
	return &Pipeline{plan: plan}, nil
}

// Feed 喂入一批事件：任一条非法则整批不生效（状态与输出均不变）。
func (p *Pipeline) Feed(evs []Event) ([]Out, error) {
	for _, e := range evs {
		if err := e.Valid(); err != nil {
			return nil, err
		}
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	out := p.plan.Run(evs)
	p.out = append(p.out, out...)
	return out, nil
}

// Output 返回迄今全部输出的副本（只读，可并发）。
func (p *Pipeline) Output() []Out {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return slices.Clone(p.out)
}

// SelfCheck 对内置事件序列核验四条不变量；只使用临时实例，可并发调用。
func SelfCheck() error {
	seqs := builtinSeqs()
	// 不变量 1：优化链与朴素链逐事件一致（每组序列用全新链，状态互不污染）。
	for ci := 0; ci < 3; ci++ {
		for si, evs := range seqs {
			plan, err := pipe.Build(cloneChain(ci))
			if err != nil {
				return fmt.Errorf("selfcheck: build: %w", err)
			}
			if got, want := plan.Run(evs), pipe.RunNaive(cloneChain(ci), evs); !slices.Equal(got, want) {
				return fmt.Errorf("selfcheck: invariant1 chain%d seq%d", ci, si)
			}
		}
	}
	// 不变量 2：同段无状态重排（合法 move）+ AND 合并，输出不变。
	for si, evs := range seqs {
		re, err := pipe.Build(cloneChain(3), Move{From: 0, To: 1})
		if err != nil {
			return fmt.Errorf("selfcheck: legal move rejected: %w", err)
		}
		if got, want := re.Run(evs), pipe.RunNaive(cloneChain(3), evs); !slices.Equal(got, want) {
			return fmt.Errorf("selfcheck: invariant2 seq%d", si)
		}
	}
	// 不变量 3：跨越屏障的移动被拒，状态过滤器不可移动。
	for _, mv := range []Move{{From: 2, To: 0}, {From: 1, To: 0}} {
		if _, err := pipe.Build(cloneChain(0), mv); !errors.Is(err, ErrBarrier) {
			return fmt.Errorf("selfcheck: invariant3 move %+v: %v", mv, err)
		}
	}
	// 不变量 4：失败不留痕——非法谓词/事件被拒后实例行为不变。
	bad := []Op{pipe.Filter(pred.OfField(99, "")), pipe.RecordHigh()}
	if _, err := New(bad); !errors.Is(err, ErrBadPredicate) {
		return fmt.Errorf("selfcheck: invariant4 bad predicate: %v", err)
	}
	pl, err := New(cloneChain(0))
	if err != nil {
		return fmt.Errorf("selfcheck: build: %w", err)
	}
	if _, err := pl.Feed([]Event{{Seq: 1, Val: 10, Kind: "A"}}); err != nil {
		return fmt.Errorf("selfcheck: feed: %w", err)
	}
	before := pl.Output()
	if _, err := pl.Feed([]Event{{Seq: 2, Val: 40, Kind: "A"}, {Seq: 0, Val: 1, Kind: "x"}}); !errors.Is(err, ErrBadEvent) {
		return fmt.Errorf("selfcheck: invariant4 bad event: %v", err)
	}
	if !slices.Equal(pl.Output(), before) {
		return errors.New("selfcheck: invariant4 rejected batch left trace")
	}
	if _, err := pl.Feed([]Event{{Seq: 3, Val: 40, Kind: "A"}}); err != nil || len(pl.Output()) != len(before)+1 {
		return errors.New("selfcheck: invariant4 instance unusable after rejection")
	}
	return nil
}

// cloneChain 重新构造一条全新算子链（状态过滤器回到初始状态）。
func cloneChain(ci int) []Op {
	f1, f2, s := pipe.Filter(pred.Even()), pipe.Filter(pred.KindEq("A")), pipe.RecordHigh()
	return [][]Op{
		{f1, s, f2},
		{f2, f1, s, f1},
		{f1, s, f2, pipe.RecordHigh(), f1},
		{f2, f1, s},
	}[ci]
}

// builtinSeqs 返回内置事件序列：题面六步序列 + 循环生成的确定性序列。
func builtinSeqs() [][]Event {
	seqs := [][]Event{{
		{Seq: 1, Val: 10, Kind: "A"}, {Seq: 2, Val: 25, Kind: "A"}, {Seq: 3, Val: 18, Kind: "A"},
		{Seq: 4, Val: 30, Kind: "B"}, {Seq: 5, Val: 22, Kind: "A"}, {Seq: 6, Val: 40, Kind: "A"},
	}}
	for n := 1; n <= 3; n++ {
		var evs []Event
		for i := 1; i <= 20; i++ {
			kind := "A"
			if (i*n)%3 == 0 {
				kind = "B"
			}
			evs = append(evs, Event{Seq: int64(i), Val: int64((i*7 + n*13) % 50), Kind: kind})
		}
		seqs = append(seqs, evs)
	}
	return seqs
}
