// Package cwagg 按 Key 维护各窗口的 cnt/sum、水位、触发与关闭、输出聚合与丢弃计数。
// 依赖 cntwin；不做参数校验（由上层 api 负责），不保证并发安全（由上层加锁）。
package cwagg

import "ontology/cntwin"

// Fire 是一次窗口触发的输出：(Key, 窗口号, 聚合和)。
type Fire struct {
	Key string
	Win int64
	Sum int64
}

type window struct {
	cnt, sum int64
	fired    bool
}

type keyState struct {
	wm   int64
	wins map[int64]*window
	seen map[int64]struct{} // 已投递的 Pos，重复投递幂等忽略
}

// Agg 是计数翻滚窗口聚合器。
type Agg struct {
	size, lateness int64
	keys           map[string]*keyState
	fired          []Fire
	dropped        int64
	checked        int64 // 最近一次元素到达时为判定触发而检查的窗口个数（恒为 1）
}

// New 创建聚合器；调用方保证 size>0、lateness>=0。
func New(size, lateness int64) *Agg {
	return &Agg{size: size, lateness: lateness, keys: map[string]*keyState{}}
}

// Add 应用一个元素；若因此触发窗口则返回该 Fire 与 true。重复 (Key,Pos) 幂等忽略。
func (a *Agg) Add(key string, pos, val int64) (Fire, bool) {
	ks := a.keys[key]
	if ks == nil {
		ks = &keyState{wm: -1, wins: map[int64]*window{}, seen: map[int64]struct{}{}}
		a.keys[key] = ks
	}
	if _, dup := ks.seen[pos]; dup {
		return Fire{}, false // 不计数、不推进水位
	}
	ks.seen[pos] = struct{}{}
	k := cntwin.Window(pos, a.size) // O(1) 直接定位，不扫描开放窗口
	w := ks.wins[k]
	if w == nil {
		w = &window{}
		ks.wins[k] = w
	}
	a.checked = 1         // 只检查这 1 个窗口
	accept := !w.fired && // 已触发关闭的窗口不再接受
		(!cntwin.Late(pos, ks.wm) || cntwin.Acceptable(pos, ks.wm, a.lateness))
	var f Fire
	fired := false
	if accept {
		w.cnt++
		w.sum += val
		if cntwin.Triggered(w.cnt, a.size) {
			w.fired = true
			f, fired = Fire{Key: key, Win: k, Sum: w.sum}, true
			a.fired = append(a.fired, f)
		}
	} else {
		a.dropped++
	}
	if pos > ks.wm { // 无论接受还是丢弃都推进水位，且单调不回退
		ks.wm = pos
	}
	return f, fired
}

// Fired 返回迄今全部触发输出的副本。
func (a *Agg) Fired() []Fire { return append([]Fire(nil), a.fired...) }

// Dropped 返回迄今被丢弃的元素个数。
func (a *Agg) Dropped() int64 { return a.dropped }
