// Package dwr 维护两个副本 A、B，提供双写/单侧写与逐键选胜者的对账。依赖 rec。
package dwr

import (
	"sync"

	"ontology/rec"
)

// Engine 是双写对账引擎。零值不可用，请用 New。
type Engine struct {
	mu      sync.RWMutex
	a, b    map[string]rec.Record
	maxVer  int64
	dirty   map[string]struct{} // 两侧可能不一致的键；对账只遍历它而非整表
	checked int                 // 非导出：最近一次 Reconcile 检查过的键个数
}

// New 创建空引擎。
func New() *Engine {
	return &Engine{a: map[string]rec.Record{}, b: map[string]rec.Record{}, dirty: map[string]struct{}{}}
}

// write 是所有写操作的唯一入口：先完成全部校验（失败不留痕），再落一侧或两侧。
// 校验全部先于任何状态修改，被拒的写不改变副本、脏集合与全局最大版本。
func (e *Engine) write(side int, k string, r rec.Record, both bool) error {
	if err := rec.CheckKey(k); err != nil {
		return err
	}
	if !r.Del {
		if err := rec.CheckVal(r.Val); err != nil {
			return err
		}
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	if err := rec.CheckVer(r.Ver, e.maxVer); err != nil {
		return err
	}
	if both {
		e.a[k], e.b[k] = r, r
	} else if side == 0 {
		e.a[k] = r
	} else {
		e.b[k] = r
	}
	e.maxVer = r.Ver
	if e.a[k] == e.b[k] {
		delete(e.dirty, k)
	} else {
		e.dirty[k] = struct{}{}
	}
	return nil
}

// Put 双写活值。
func (e *Engine) Put(k, val string, ver int64) error {
	return e.write(-1, k, rec.Live(val, ver), true)
}

// Del 双写墓碑。
func (e *Engine) Del(k string, ver int64) error {
	return e.write(-1, k, rec.Tomb(ver), true)
}

// PutOne 只写一侧活值（side=0 写 A，side=1 写 B），模拟另一半写入失败。
func (e *Engine) PutOne(side int, k, val string, ver int64) error {
	if err := rec.CheckSide(side); err != nil {
		return err
	}
	return e.write(side, k, rec.Live(val, ver), false)
}

// DelOne 只写一侧墓碑。
func (e *Engine) DelOne(side int, k string, ver int64) error {
	if err := rec.CheckSide(side); err != nil {
		return err
	}
	return e.write(side, k, rec.Tomb(ver), false)
}

// Reconcile 只遍历脏集合：逐键取 Ver 大者（从未写过视作版本 0）写回两侧。
// 修平后键移出脏集合，因此幂等；检查个数与总键数无关。
func (e *Engine) Reconcile() {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.checked = 0
	for k := range e.dirty {
		e.checked++
		// 未写过的一侧为零值 Record{Ver:0}，天然输给任何真实版本。
		win := rec.Winner(e.a[k], e.b[k])
		e.a[k], e.b[k] = win, win
		delete(e.dirty, k)
	}
}

// View 返回每个键当前胜者中的活值；墓碑键与不存在的键不出现。
func (e *Engine) View() map[string]string {
	e.mu.RLock()
	defer e.mu.RUnlock()
	out := map[string]string{}
	for k, ra := range e.a {
		win := ra
		if rb, ok := e.b[k]; ok && rb.Ver > ra.Ver {
			win = rb
		}
		if win.IsLive() {
			out[k] = win.Val
		}
	}
	for k, rb := range e.b {
		if _, ok := e.a[k]; !ok && rb.IsLive() {
			out[k] = rb.Val
		}
	}
	return out
}

// Consistent 报告两侧副本是否逐键完全相同（值、版本、是否墓碑）。
func (e *Engine) Consistent() bool {
	e.mu.RLock()
	defer e.mu.RUnlock()
	if len(e.a) != len(e.b) {
		return false
	}
	for k, ra := range e.a {
		if rb, ok := e.b[k]; !ok || rb != ra {
			return false
		}
	}
	return true
}
