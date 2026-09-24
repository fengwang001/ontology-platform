// Package join 维护左外连接物化视图：连接状态与「先撤回再插入」的变更日志。依赖 rstore。
package join

import (
	"errors"
	"sync"

	"ontology/rstore"
)

// 可判定的哨兵错误，三者互不相同。
var (
	ErrEmptyKey = errors.New("join: empty key")
	ErrNoLeft   = errors.New("join: no such left record")
	ErrNoRight  = errors.New("join: no such right record")
)

// Op 是一条变更日志：Insert 为 true 即插入 +(K,LV,RV)，否则撤回 -(K,LV,RV)。
type Op struct {
	Insert bool
	K      string
	LV     int64
	RV     *int64 // nil 表示右值缺席
}

// Row 是宽表的一行。
type Row struct {
	K  string
	LV int64
	RV *int64 // nil 表示右值缺席
}

// View 是左外连接物化视图。左键按字典序升序保存，查找走二分定位（对数级），不整表扫描。
type View struct {
	mu     sync.RWMutex
	keys   []string         // 左键，升序
	lv     map[string]int64 // 左值
	right  *rstore.Store    // 右记录集
	checks int              // 最近一次 PutR 补发/更新时检查过的左记录个数
}

func New() *View { return &View{lv: make(map[string]int64), right: rstore.New()} }

// find 二分定位左键 k，返回插入位与是否命中；每次比较计入 checks。
func (v *View) find(k string) (int, bool) {
	lo, hi := 0, len(v.keys)
	for lo < hi {
		v.checks++
		mid := (lo + hi) / 2
		if v.keys[mid] < k {
			lo = mid + 1
		} else {
			hi = mid
		}
	}
	return lo, lo < len(v.keys) && v.keys[lo] == k
}

func rvOf(v int64, ok bool) *int64 {
	if !ok {
		return nil
	}
	return &v
}

// PutL 左记录到达：新键插入一行；已有键先撤回旧行再插入新行（只 LV 变）。
func (v *View) PutL(k string, lv int64) ([]Op, error) {
	if k == "" {
		return nil, ErrEmptyKey
	}
	v.mu.Lock()
	defer v.mu.Unlock()
	rv := rvOf(v.right.Get(k))
	i, found := v.find(k)
	if found {
		old := Op{false, k, v.lv[k], rv}
		v.lv[k] = lv
		return []Op{old, {true, k, lv, rv}}, nil
	}
	v.keys = append(v.keys, "")
	copy(v.keys[i+1:], v.keys[i:])
	v.keys[i] = k
	v.lv[k] = lv
	return []Op{{true, k, lv, rv}}, nil
}

// PutR 右记录到达：无左则仅存不输出；有左且 RV 缺席则补发；已有 RV 则更新。
func (v *View) PutR(k string, rv int64) ([]Op, error) {
	if k == "" {
		return nil, ErrEmptyKey
	}
	v.mu.Lock()
	defer v.mu.Unlock()
	v.checks = 0
	_, found := v.find(k)
	old, had := v.right.Get(k)
	v.right.Put(k, rv)
	if !found {
		return nil, nil // 仅保存待用，不输出日志
	}
	lv := v.lv[k]
	return []Op{{false, k, lv, rvOf(old, had)}, {true, k, lv, &rv}}, nil
}

// DelL 左记录删除：撤回当前行；右记录保留。K 未物化则报错。
func (v *View) DelL(k string) ([]Op, error) {
	v.mu.Lock()
	defer v.mu.Unlock()
	i, found := v.find(k)
	if !found {
		return nil, ErrNoLeft
	}
	op := Op{false, k, v.lv[k], rvOf(v.right.Get(k))}
	v.keys = append(v.keys[:i], v.keys[i+1:]...)
	delete(v.lv, k)
	return []Op{op}, nil
}

// DelR 右记录删除：有左且 RV 非缺席则先撤回再插入 nil 行；无左则仅删 R。右记录不存在则报错。
func (v *View) DelR(k string) ([]Op, error) {
	v.mu.Lock()
	defer v.mu.Unlock()
	old, had := v.right.Get(k)
	if !had {
		return nil, ErrNoRight
	}
	v.right.Del(k)
	if _, found := v.find(k); !found {
		return nil, nil // 仅删 R，无日志
	}
	lv := v.lv[k]
	return []Op{{false, k, lv, &old}, {true, k, lv, nil}}, nil
}

// View 返回宽表全量快照，按 K 升序。
func (v *View) View() []Row {
	v.mu.RLock()
	defer v.mu.RUnlock()
	rows := make([]Row, 0, len(v.keys))
	for _, k := range v.keys {
		rows = append(rows, Row{k, v.lv[k], rvOf(v.right.Get(k))})
	}
	return rows
}

func (v *View) Rights() map[string]int64 {
	v.mu.RLock()
	defer v.mu.RUnlock()
	return v.right.Snapshot()
}
