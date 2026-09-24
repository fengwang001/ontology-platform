// Package api 对外接口。依赖 fk。所有方法可并发调用。
package api

import (
	"fmt"
	"sort"
	"sync"

	"ontology/fk"
	"ontology/sch"
)

// Engine 是变更流外键约束物化器，状态全在进程内存。
type Engine struct {
	mu sync.RWMutex
	st *sch.State
}

func New() *Engine { return &Engine{st: sch.New()} }

func (e *Engine) PIns(pk string) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	return fk.PIns(e.st, pk)
}

func (e *Engine) PDel(pk string) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	return fk.PDel(e.st, pk)
}

func (e *Engine) CIns(ck, pk string) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	return fk.CIns(e.st, ck, pk)
}

func (e *Engine) CDel(ck string) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	return fk.CDel(e.st, ck)
}

// ViewP 返回父行主键，升序。
func (e *Engine) ViewP() []string {
	e.mu.RLock()
	defer e.mu.RUnlock()
	set := e.st.ParentSet()
	out := make([]string, 0, len(set))
	for pk := range set {
		out = append(out, pk)
	}
	sort.Strings(out)
	return out
}

// ViewC 返回子表副本 ck->parent。
func (e *Engine) ViewC() map[string]string {
	e.mu.RLock()
	defer e.mu.RUnlock()
	return e.st.ChildMap()
}

// SelfCheck 先核对当前实例内部一致性（不变量 2、3），再在全新实例上
// 重放内置操作序列，逐步核验四条不变量（含与朴素重放逐行对拍、
// 被拒操作前后视图不变）。全部通过返回 nil。
func (e *Engine) SelfCheck() error {
	e.mu.RLock()
	err := fk.Check(e.st)
	e.mu.RUnlock()
	if err != nil {
		return err
	}
	for i, seq := range sequences() {
		if err := replay(seq); err != nil {
			return fmt.Errorf("selfcheck seq %d: %w", i, err)
		}
	}
	return nil
}

type op struct {
	kind   byte // 'P' 父 'C' 子；大写插 小写删
	a, b   string
	reject bool
}

func sequences() [][]op {
	return [][]op{
		{ // 第三节十三步
			{'C', "k1", "p1", true}, {'P', "p1", "", false}, {'C', "k1", "p1", false},
			{'C', "k2", "p2", true}, {'p', "p1", "", true}, {'C', "k3", "p1", false},
			{'c', "k1", "", false}, {'c', "k2", "", true}, {'c', "k3", "", false},
			{'p', "p1", "", false}, {'C', "k3", "p1", true}, {'P', "p1", "", false},
			{'C', "k3", "p1", false},
		},
		{ // 幂等与重投
			{'P', "p1", "", false}, {'P', "p1", "", false}, {'C', "k1", "p2", true},
			{'P', "p2", "", false}, {'C', "k1", "p2", false}, {'p', "p2", "", true},
			{'c', "k1", "", false}, {'p', "p2", "", false}, {'p', "p1", "", false},
		},
	}
}

// replay 在全新实例上逐步执行 seq 并核验四条不变量。
func replay(seq []op) error {
	e := New()
	var done []op // 仅判定为成功的操作
	for i, o := range seq {
		beforeP, beforeC := e.ViewP(), e.ViewC()
		err := e.apply(o)
		if (err != nil) != o.reject {
			return fmt.Errorf("step %d: reject=%v err=%v", i, o.reject, err)
		}
		if err == nil {
			done = append(done, o)
		} else if !eqViews(beforeP, beforeC, e.ViewP(), e.ViewC()) {
			return fmt.Errorf("step %d: rejected op changed state", i) // 不变量4
		}
		if cerr := fk.Check(e.st); cerr != nil {
			return fmt.Errorf("step %d: %w", i, cerr) // 不变量2、3
		}
		np, nc := naive(done)
		if !eqViews(np, nc, e.ViewP(), e.ViewC()) {
			return fmt.Errorf("step %d: view != naive replay", i) // 不变量1
		}
	}
	return nil
}

func (e *Engine) apply(o op) error {
	switch o.kind {
	case 'P':
		return e.PIns(o.a)
	case 'p':
		return e.PDel(o.a)
	case 'C':
		return e.CIns(o.a, o.b)
	default:
		return e.CDel(o.a)
	}
}

// naive 从头重放成功操作，得到朴素参照视图。
func naive(done []op) ([]string, map[string]string) {
	p := map[string]bool{}
	c := map[string]string{}
	for _, o := range done {
		switch o.kind {
		case 'P':
			p[o.a] = true
		case 'p':
			delete(p, o.a)
		case 'C':
			c[o.a] = o.b
		default:
			delete(c, o.a)
		}
	}
	out := make([]string, 0, len(p))
	for pk := range p {
		out = append(out, pk)
	}
	sort.Strings(out)
	return out, c
}

func eqViews(p1 []string, c1 map[string]string, p2 []string, c2 map[string]string) bool {
	if len(p1) != len(p2) || len(c1) != len(c2) {
		return false
	}
	for i := range p1 {
		if p1[i] != p2[i] {
			return false
		}
	}
	for k, v := range c1 {
		if c2[k] != v {
			return false
		}
	}
	return true
}
