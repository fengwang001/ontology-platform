// Package cagg 维护 (Key,大窗口) 的子窗口累计计数、水位线、触发与清除，依赖 cwin。
package cagg

import (
	"container/heap"
	"errors"
	"math"
	"sync"

	"ontology/cwin"
)

var (
	ErrTooManyOpenWindows = errors.New("cagg: too many open windows")
	ErrEmptyKey           = errors.New("cagg: event key is empty")
)

type Event struct {
	Key string
	TS  int64
}

// Out 是一条输出 (Key,[Start,End),Count)，Count 为累计计数。
type Out struct {
	Key        string
	Start, End int64
	Count      int
}

// Agg 状态全在内存；未见事件时 wm=MinInt64。scan 是非导出计数器：最近一次
// 水位线推进时检查过的未清除大窗口数（堆序定位），绝不出现在公开接口里。
type Agg struct {
	mu      sync.RWMutex
	sp      cwin.Spec
	maxOpen int
	wm      int64
	dropped int
	outs    []Out
	wins    map[winID]*win
	hp      hp
	scan    int
}

func New(sp cwin.Spec, maxOpen int) *Agg {
	return &Agg{sp: sp, maxOpen: maxOpen, wm: math.MinInt64, wins: map[winID]*win{}}
}

// Feed 顺序处理一批事件并返回本批输出；任一条被拒整批不生效（不变量4）。
func (a *Agg) Feed(evs []Event) ([]Out, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	for _, e := range evs { // 预检：非法事件零状态变更
		if e.Key == "" {
			return nil, ErrEmptyKey
		}
	}
	save := a.snapshot()
	for _, e := range evs {
		if nw := e.TS - a.sp.Delay(); nw > a.wm { // 水位线只进不退（不变量3）
			a.wm = nw
		}
		S := a.sp.Start(e.TS)
		id := winID{e.Key, S}
		if a.wm >= a.sp.EMin(S, e.TS) { // wm==Emin 即丢弃；丢弃/接受同样推进 wm
			a.dropped++
		} else {
			w := a.wins[id]
			if w == nil {
				w = &win{bins: make([]int, a.sp.Steps())}
				a.wins[id] = w
				heap.Push(&a.hp, entry{a.sp.End(S, 1), e.Key, S})
			}
			w.bins[int((e.TS-S)/a.sp.Step())]++ // 只入一个分箱，累计=触发时前缀和
		}
		a.drain()
		if len(a.wins) > a.maxOpen { // 容量超限：整批恢复
			save.restore(a)
			return nil, ErrTooManyOpenWindows
		}
	}
	return append([]Out(nil), a.outs[len(save.outs):]...), nil
}

// drain 按「下一待触发终点」堆序触发所有 end<=wm 的子窗口，非整表扫描。
func (a *Agg) drain() {
	seen := map[winID]struct{}{}
	for len(a.hp) > 0 {
		t := a.hp[0] // 堆顶即全局最小的下一终点
		seen[winID{t.key, t.S}] = struct{}{}
		if t.end > a.wm { // 堆序保证其余窗终点更大，到此即停
			break
		}
		id := winID{t.key, t.S}
		w := a.wins[id]
		j := w.fired + 1
		w.acc += w.bins[j-1] // 累计=前缀和（不变量1/2）
		w.fired++
		a.outs = append(a.outs, Out{Key: t.key, Start: t.S, End: a.sp.End(t.S, j), Count: w.acc})
		if j == a.sp.Steps() { // 最后子窗口触发 → 清除
			heap.Pop(&a.hp)
			delete(a.wins, id)
		} else {
			a.hp[0].end = a.sp.End(t.S, j+1)
			heap.Fix(&a.hp, 0)
		}
	}
	a.scan = len(seen)
}

// Flush 把水位线推到正无穷并触发全部剩余子窗口。
func (a *Agg) Flush() []Out {
	a.mu.Lock()
	defer a.mu.Unlock()
	n := len(a.outs)
	a.wm = math.MaxInt64
	a.drain()
	return append([]Out(nil), a.outs[n:]...)
}

func (a *Agg) All() []Out   { a.mu.RLock(); defer a.mu.RUnlock(); return append([]Out(nil), a.outs...) }
func (a *Agg) Dropped() int { a.mu.RLock(); defer a.mu.RUnlock(); return a.dropped }

// scanCount 仅供同包测试读取非导出检查计数器。
func (a *Agg) scanCount() int { a.mu.RLock(); defer a.mu.RUnlock(); return a.scan }
