// Package api 是合并水位算子对外的唯一入口，依赖 merge；处理时间由调用方注入。
package api

import (
	"errors"
	"math"
	"math/rand"
	"slices"
	"sync"

	"ontology/merge"
)

var (
	ErrInvalidArgument = merge.ErrBadArgument
	ErrPartitionRange  = merge.ErrPartitionRange
	ErrClockRewind     = merge.ErrClockRewind
	ErrWatermarkRewind = merge.ErrWatermarkRewind
)

type Merger struct {
	mu sync.RWMutex
	m  *merge.Manager
}

func New(n int, idle, t0 int64) (*Merger, error) {
	m, e := merge.New(n, idle, t0)
	if e != nil {
		return nil, e
	}
	return &Merger{m: m}, nil
}
func (g *Merger) Report(p int, w, now int64) (int64, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.m.Report(p, w, now)
}
func (g *Merger) Tick(now int64) (int64, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.m.Tick(now)
}
func (g *Merger) Merged() int64 { g.mu.RLock(); defer g.mu.RUnlock(); return g.m.Merged() }
func (g *Merger) Idle(p int) bool {
	g.mu.RLock()
	defer g.mu.RUnlock()
	return g.m.Idle(p)
}

// naive 是朴素参照：每步全表扫描判空闲、取候选值、与旧合并值取 max。
type naive struct {
	idle, clock, merged int64
	water, last         []int64
}

func newNaive(n int, idle, t0 int64) *naive {
	return &naive{idle: idle, clock: t0, merged: math.MinInt64,
		water: slices.Repeat([]int64{math.MinInt64}, n), last: slices.Repeat([]int64{t0}, n)}
}
func (x *naive) step(p int, w, now int64, rep bool) (int64, error) {
	if rep && (p < 0 || p >= len(x.water)) {
		return x.merged, merge.ErrPartitionRange
	}
	if now < x.clock {
		return x.merged, merge.ErrClockRewind
	}
	if rep && w < x.water[p] {
		return x.merged, merge.ErrWatermarkRewind
	}
	x.clock = now
	if rep {
		x.water[p], x.last[p] = w, now
	}
	cand, have := int64(0), false
	for q := range x.water {
		if now-x.last[q] < x.idle && (!have || x.water[q] < cand) {
			cand, have = x.water[q], true
		}
	}
	if have && cand > x.merged {
		x.merged = cand
	}
	return x.merged, nil
}
func errBad(s string) error { return errors.New("api: " + s) }

// SelfCheck 用内置序列核验四条不变量：随机合法序列对拍朴素参照（含单调、
// 空闲集合）、空闲边界精确、被拒不留痕；全过返回 nil，且不改动接收者。
func (g *Merger) SelfCheck() error {
	for _, c := range [][2]int64{{0, 1}, {3, 0}} {
		if _, e := New(int(c[0]), c[1], 0); !errors.Is(e, ErrInvalidArgument) {
			return errBad("n<=0 or idle<=0 not rejected")
		}
	}
	return errors.Join(checkBoundary(), checkRandom())
}
func checkBoundary() error {
	g, _ := New(1, 10, 0)
	g.Report(0, 5, 0)
	_, e9 := g.Tick(9)
	a9 := g.Idle(0)
	_, e10 := g.Tick(10)
	a10 := g.Idle(0)
	_, er := g.Report(0, 4, 10) // 水位回退，必须被拒
	ar := g.Idle(0)
	_, e19 := g.Tick(19) // last 若被刷成 10，时差 9 会误判活跃
	a19 := g.Idle(0)
	v, eok := g.Report(0, 6, 19)
	if !(e9 == nil && !a9 && e10 == nil && a10 &&
		errors.Is(er, ErrWatermarkRewind) && ar && e19 == nil && a19 &&
		eok == nil && v == 6 && !g.Idle(0)) {
		return errBad("boundary or no-trace failed")
	}
	return nil
}
func checkRandom() error {
	rng := rand.New(rand.NewSource(273))
	for range 80 {
		n := 1 + rng.Intn(6)
		idle := int64(1 + rng.Intn(12))
		g, _ := New(n, idle, 0)
		r := newNaive(n, idle, 0)
		now, prev := int64(0), int64(math.MinInt64)
		for range 60 {
			p, rep := rng.Intn(n), rng.Intn(2) == 0
			w := r.water[p] + rng.Int63n(5) // 初始 MinInt64 加小正数仍合法且单调
			if !rep {
				now += int64(rng.Intn(int(idle) + 4))
			}
			var v1, v2 int64
			var e1, e2 error
			if rep {
				v1, e1 = g.Report(p, w, now)
			} else {
				v1, e1 = g.Tick(now)
			}
			v2, e2 = r.step(p, w, now, rep)
			if e1 != e2 || v1 != v2 || v1 < prev {
				return errBad("mismatch vs naive or merged regressed")
			}
			prev = v1
			for q := range n {
				if g.Idle(q) != (now-r.last[q] >= idle) {
					return errBad("idle set mismatch vs naive")
				}
			}
		}
	}
	return nil
}
