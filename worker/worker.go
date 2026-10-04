// Package worker 维护工作者的属性与槽位占用。
package worker

import (
	"sort"

	"ontology/action"
)

// Worker 是一个可注册的远程工作者。
type Worker struct {
	Name  string
	Props []action.KV
	Slots int
	used  int

	holding map[*action.Op]struct{}
}

// New 创建未注册的工作者对象。
func New(name string, props []action.KV, slots int) *Worker {
	return &Worker{
		Name:    name,
		Props:   append([]action.KV(nil), props...),
		Slots:   slots,
		holding: make(map[*action.Op]struct{}),
	}
}

// Free 返回剩余空槽。
func (w *Worker) Free() int { return w.Slots - w.used }

// Used 返回已占用槽数。
func (w *Worker) Used() int { return w.used }

// Assign 占用一个槽并登记持有操作。
func (w *Worker) Assign(o *action.Op) bool {
	if w.used >= w.Slots {
		return false
	}
	if _, ok := w.holding[o]; ok {
		return false
	}
	w.holding[o] = struct{}{}
	w.used++
	return true
}

// Release 释放一个槽；未持有时返回 false。
func (w *Worker) Release(o *action.Op) bool {
	if _, ok := w.holding[o]; !ok {
		return false
	}
	delete(w.holding, o)
	w.used--
	return true
}

// Holds 报告是否持有某操作。
func (w *Worker) Holds(o *action.Op) bool {
	_, ok := w.holding[o]
	return ok
}

// Held 按稳定顺序返回当前持有的全部操作。
func (w *Worker) Held() []*action.Op {
	out := make([]*action.Op, 0, len(w.holding))
	for o := range w.holding {
		out = append(out, o)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Seq < out[j].Seq })
	return out
}

// Pool 是工作者注册表。
type Pool struct {
	workers map[string]*Worker
}

// NewPool 创建空注册表。
func NewPool() *Pool { return &Pool{workers: make(map[string]*Worker)} }

// Add 注册工作者；重名返回 false（调用方负责区分 ErrExists）。
func (p *Pool) Add(w *Worker) bool {
	if _, ok := p.workers[w.Name]; ok {
		return false
	}
	p.workers[w.Name] = w
	return true
}

// Remove 注销并返回该工作者；不存在返回 nil。
func (p *Pool) Remove(name string) *Worker {
	w := p.workers[name]
	delete(p.workers, name)
	return w
}

// Get 读取工作者。
func (p *Pool) Get(name string) (*Worker, bool) {
	w, ok := p.workers[name]
	return w, ok
}

// Names 返回所有已注册工作者名（排序）。
func (p *Pool) Names() []string {
	out := make([]string, 0, len(p.workers))
	for n := range p.workers {
		out = append(out, n)
	}
	sort.Strings(out)
	return out
}
