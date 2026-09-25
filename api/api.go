// Package api 是外部排序溢写 + 排序归并连接的对外接口，依赖方向 api → mrg → run。
package api

import (
	"errors"
	"sort"
	"sync"

	"ontology/mrg"
	"ontology/run"
)

type Key = run.Key
type Pair = mrg.Pair

// 三类可判定、互不相同的哨兵错误。
var ErrBadThreshold = run.ErrBadThreshold
var ErrBadFanIn = mrg.ErrBadFanIn
var ErrFanInExceeded = mrg.ErrFanInExceeded
var ErrNegativeKey = run.ErrNegativeKey

type Engine struct {
	mu    sync.RWMutex
	m     int
	fanIn int
	rRuns []run.Run
	sRuns []run.Run
}

// New：M ≥ 1 且 maxFanIn ≥ 2，否则整体失败（不返回实例）。
func New(m, maxFanIn int) (*Engine, error) {
	if m < 1 {
		return nil, ErrBadThreshold
	}
	if maxFanIn < 2 {
		return nil, ErrBadFanIn
	}
	return &Engine{m: m, fanIn: maxFanIn}, nil
}

// build 先在锁外全量校验并构造新 run，再一次性整体换写，故任一条被拒旧状态不变。
func (e *Engine) build(keys []Key, into *[]run.Run) error {
	runs, err := run.MakeRuns(keys, e.m)
	if err != nil {
		return err
	}
	e.mu.Lock()
	*into = runs
	e.mu.Unlock()
	return nil
}

func (e *Engine) BuildR(keys []Key) error { return e.build(keys, &e.rRuns) }
func (e *Engine) BuildS(keys []Key) error { return e.build(keys, &e.sRuns) }

// Join 每次返回全新切片，可并发只读调用。
func (e *Engine) Join() ([]Pair, error) {
	e.mu.RLock()
	defer e.mu.RUnlock()
	pairs, _, err := mrg.Join(e.rRuns, e.sRuns, e.fanIn)
	if err != nil {
		return nil, err
	}
	return pairs, nil
}

func sortPairs(ps []Pair) {
	sort.Slice(ps, func(i, j int) bool {
		if ps[i].RKey != ps[j].RKey {
			return ps[i].RKey < ps[j].RKey
		}
		return ps[i].SKey < ps[j].SKey
	})
}

// naivePairs 朴素嵌套循环参照：R 每条 × S 每条，Key 相等则配对，返回排序多重集。
func naivePairs(rr, ss []run.Run) []Pair {
	var out []Pair
	for _, r := range rr {
		for _, a := range r.Keys {
			for _, s := range ss {
				for _, b := range s.Keys {
					if a == b {
						out = append(out, Pair{RKey: a, SKey: b})
					}
				}
			}
		}
	}
	sortPairs(out)
	return out
}

// SelfCheck 用内置当前状态核验四条不变量；任一不成立返回非空错误。
func (e *Engine) SelfCheck() error {
	e.mu.RLock()
	m, fanIn := e.m, e.fanIn
	rr := append([]run.Run(nil), e.rRuns...)
	ss := append([]run.Run(nil), e.sRuns...)
	e.mu.RUnlock()
	for _, g := range [][]run.Run{rr, ss} {
		for i, r := range g { // 不变量 3：非末 run 恰好 M 条，末 run ≤ M，块内升序
			if (i < len(g)-1 && len(r.Keys) != m) || len(r.Keys) > m {
				return errors.New("selfcheck: run size violates M")
			}
			for j := 1; j < len(r.Keys); j++ {
				if r.Keys[j] < r.Keys[j-1] {
					return errors.New("selfcheck: run not sorted")
				}
			}
		}
		got, err := mrg.Merge(g, fanIn) // 不变量 2：归并序列非降
		if err != nil {
			return err
		}
		for j := 1; j < len(got); j++ {
			if got[j] < got[j-1] {
				return errors.New("selfcheck: merged not sorted")
			}
		}
	}
	got, _, err := mrg.Join(rr, ss, fanIn) // 不变量 1：与朴素参照多重集一致
	if err != nil {
		return err
	}
	sortPairs(got)
	want := naivePairs(rr, ss)
	if len(got) != len(want) {
		return errors.New("selfcheck: pair multiset size mismatch")
	}
	for i := range got {
		if got[i] != want[i] {
			return errors.New("selfcheck: pair multiset mismatch")
		}
	}
	return nil
}
