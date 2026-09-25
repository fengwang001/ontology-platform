// Package api 是对外面孔：双写、单侧写、对账、视图与自检。依赖 dwr。
package api

import (
	"errors"
	"fmt"

	"ontology/dwr"
	"ontology/rec"
)

// ErrSelfCheck 是可判定的自检失败哨兵错误。
var ErrSelfCheck = errors.New("api: self-check failed")

// API 包装一个对账引擎，方法均可并发调用。
type API struct{ eng *dwr.Engine }

// New 创建空实例。
func New() *API { return &API{eng: dwr.New()} }

// Put 双写活值；Del 双写墓碑；PutOne/DelOne 只写一侧（side=0 为 A，1 为 B）。
func (a *API) Put(k, v string, ver int64) error           { return a.eng.Put(k, v, ver) }
func (a *API) Del(k string, ver int64) error              { return a.eng.Del(k, ver) }
func (a *API) PutOne(s int, k, v string, ver int64) error { return a.eng.PutOne(s, k, v, ver) }
func (a *API) DelOne(s int, k string, ver int64) error    { return a.eng.DelOne(s, k, ver) }

// Reconcile 对账：逐分歧键选胜者修平两侧。
func (a *API) Reconcile() { a.eng.Reconcile() }

// View 返回每个键当前胜者的活值；墓碑键不出现。
func (a *API) View() map[string]string { return a.eng.View() }

// op 是内置写序列里的一条写。
type op struct {
	kind     int // 0=Put 1=Del 2=PutOne 3=DelOne
	side     int
	key, val string
	ver      int64
}

// batchModel 批量重算：按 Ver 升序作用到权威表，逐键取最大 Ver，墓碑则键不存在。
func batchModel(ops []op) map[string]string {
	tab := map[string]rec.Record{}
	for _, o := range ops {
		if o.kind%2 == 0 {
			tab[o.key] = rec.Live(o.val, o.ver)
		} else {
			tab[o.key] = rec.Tomb(o.ver)
		}
	}
	out := map[string]string{}
	for k, r := range tab {
		if r.IsLive() {
			out[k] = r.Val
		}
	}
	return out
}

func apply(a *API, o op) error {
	switch o.kind {
	case 0:
		return a.Put(o.key, o.val, o.ver)
	case 1:
		return a.Del(o.key, o.ver)
	case 2:
		return a.PutOne(o.side, o.key, o.val, o.ver)
	default:
		return a.DelOne(o.side, o.key, o.ver)
	}
}

func equalView(x, y map[string]string) bool {
	if len(x) != len(y) {
		return false
	}
	for k, v := range x {
		if y[k] != v {
			return false
		}
	}
	return true
}

// SelfCheck 对内置写序列核验四条不变量：与批量重算一致、最终一致、幂等、失败不留痕。
// 只使用局部新建实例，可并发调用。
func (a *API) SelfCheck() error {
	seqs := [][]op{
		{ // 第三节的七个写
			{0, -1, "a", "a1", 1}, {0, -1, "b", "b1", 2}, {0, -1, "c", "c1", 3},
			{2, 0, "a", "a2", 4}, {3, 1, "c", "", 5}, {3, 0, "b", "", 6}, {2, 0, "d", "d1", 7},
		},
		{ // 混合：先单侧后双写修平、墓碑胜、活值胜
			{2, 1, "x", "x1", 1}, {0, -1, "x", "x2", 2}, {3, 1, "y", "", 3},
			{2, 0, "y", "y1", 4}, {1, -1, "x", "", 5}, {2, 1, "z", "z1", 6},
		},
	}
	for i, seq := range seqs {
		fresh := New()
		for _, o := range seq {
			if err := apply(fresh, o); err != nil {
				return fmt.Errorf("%w: seq %d apply: %v", ErrSelfCheck, i, err)
			}
		}
		fresh.Reconcile()
		if !equalView(fresh.View(), batchModel(seq)) { // 不变量1
			return fmt.Errorf("%w: seq %d invariant-1 batch model", ErrSelfCheck, i)
		}
		if !fresh.eng.Consistent() { // 不变量2
			return fmt.Errorf("%w: seq %d invariant-2 convergence", ErrSelfCheck, i)
		}
		before := fresh.View()
		fresh.Reconcile()
		if !equalView(fresh.View(), before) { // 不变量3
			return fmt.Errorf("%w: seq %d invariant-3 idempotence", ErrSelfCheck, i)
		}
		if err := checkNoTrace(fresh, int64(len(seq))); err != nil { // 不变量4
			return fmt.Errorf("%w: seq %d invariant-4: %v", ErrSelfCheck, i, err)
		}
	}
	return nil
}

// checkNoTrace 核验三类被拒写不改变状态，且之后仍可正常使用。
func checkNoTrace(a *API, maxVer int64) error {
	before := a.View()
	bads := []error{
		a.Put("", "v", maxVer+1),   // Key 非法
		a.Put("k", "", maxVer+1),   // 活值非法
		a.Put("k", "v", maxVer),    // Ver 非严格递增
		a.Put("k", "v", 0),         // Ver 非正
		a.DelOne(2, "k", maxVer+1), // side 非法
	}
	for _, err := range bads {
		if err == nil {
			return errors.New("rejected write returned nil error")
		}
	}
	if !equalView(a.View(), before) {
		return errors.New("rejected write changed state")
	}
	if err := a.Put("ok", "v", maxVer+1); err != nil { // 全局最大版本未被拒写污染
		return errors.New("engine unusable after rejected writes")
	}
	return nil
}
