// Package join 维护左外连接宽表：左记录集（按 K 有序）连接右记录集，
// 对 PutL/PutR/DelL/DelR 生成「先撤回再插入」的变更日志。依赖 rstore。
package join

import (
	"errors"

	"ontology/rstore"
)

// 可判定的哨兵错误，三者互不相同。
var (
	ErrEmptyKey = errors.New("join: empty key")
	ErrNoLeft   = errors.New("join: no such left record")
	ErrNoRight  = errors.New("join: no such right record")
)

// Row 是宽表的一行；RV 为 nil 表示右值缺席。
type Row struct {
	K  string
	LV int64
	RV *int64
}

// Log 是一条变更日志：Insert=true 为 +(插入)，false 为 -(撤回)。
type Log struct {
	Insert bool
	Row    Row
}

// Join 是连接状态。非并发安全，由调用方（api 包）串行化。
type Join struct {
	keys    []string         // 左记录键，字典序升序
	lv      map[string]int64 // 左记录值
	rs      *rstore.Store    // 右记录集
	checked int              // 最近一次 PutR 定位时检查过的左记录个数
}

// New 基于右记录集 rs 创建空连接。
func New(rs *rstore.Store) *Join {
	return &Join{lv: make(map[string]int64), rs: rs}
}

// find 二分定位 k，返回插入位与是否命中；检查个数记入 checked。
func (j *Join) find(k string) (int, bool) {
	lo, hi, n := 0, len(j.keys), 0
	for lo < hi {
		n++
		mid := int(uint(lo+hi) >> 1)
		if j.keys[mid] < k {
			lo = mid + 1
		} else {
			hi = mid
		}
	}
	j.checked = n
	if lo < len(j.keys) && j.keys[lo] == k {
		return lo, true
	}
	return lo, false
}

func (j *Join) rvOf(k string) *int64 {
	if v, ok := j.rs.Get(k); ok {
		return &v
	}
	return nil
}

// insert 在已定位的插入位 i 放入新左记录 (k, lv)。
func (j *Join) insert(i int, k string, lv int64) {
	j.keys = append(j.keys, "")
	copy(j.keys[i+1:], j.keys[i:])
	j.keys[i] = k
	j.lv[k] = lv
}

// PutL 左记录到达：未物化则插入；已物化则先撤回旧行再插入新行（RV 不变）。
func (j *Join) PutL(k string, lv int64) ([]Log, error) {
	if k == "" {
		return nil, ErrEmptyKey
	}
	i, ok := j.find(k)
	rv := j.rvOf(k)
	if !ok {
		j.insert(i, k, lv)
		return []Log{{Insert: true, Row: Row{K: k, LV: lv, RV: rv}}}, nil
	}
	old := Row{K: k, LV: j.lv[k], RV: rv}
	j.lv[k] = lv
	return []Log{{Insert: false, Row: old}, {Insert: true, Row: Row{K: k, LV: lv, RV: rv}}}, nil
}

// PutR 右记录到达：无左记录则仅存 R 不输出；有左记录则先撤回旧行再插入
// （此前 RV 缺席为补发，已有 RV 为更新）。
func (j *Join) PutR(k string, rv int64) ([]Log, error) {
	if k == "" {
		return nil, ErrEmptyKey
	}
	_, ok := j.find(k) // checked 记录本次定位检查的左记录个数
	if !ok {
		j.rs.Put(k, rv)
		return nil, nil
	}
	old := Row{K: k, LV: j.lv[k], RV: j.rvOf(k)}
	j.rs.Put(k, rv)
	newRV := rv
	return []Log{{Insert: false, Row: old}, {Insert: true, Row: Row{K: k, LV: old.LV, RV: &newRV}}}, nil
}

// DelL 左记录删除：撤回当前行；右记录保留，供将来同 K 左记录复用。
func (j *Join) DelL(k string) ([]Log, error) {
	i, ok := j.find(k)
	if !ok {
		return nil, ErrNoLeft
	}
	row := Row{K: k, LV: j.lv[k], RV: j.rvOf(k)}
	j.keys = append(j.keys[:i], j.keys[i+1:]...)
	delete(j.lv, k)
	return []Log{{Insert: false, Row: row}}, nil
}

// DelR 右记录删除：右记录不存在（含 RV 已缺席）则报错；有左记录则先撤回
// 旧行再插入 RV 缺席的新行；无左记录则仅删 R 不输出。
func (j *Join) DelR(k string) ([]Log, error) {
	_, hasL := j.find(k)
	rv, hasR := j.rs.Get(k)
	if !hasR {
		return nil, ErrNoRight
	}
	j.rs.Del(k)
	if !hasL {
		return nil, nil
	}
	lv := j.lv[k]
	return []Log{{Insert: false, Row: Row{K: k, LV: lv, RV: &rv}}, {Insert: true, Row: Row{K: k, LV: lv}}}, nil
}

// View 返回宽表全量快照，按 K 字典序升序。
func (j *Join) View() []Row {
	out := make([]Row, 0, len(j.keys))
	for _, k := range j.keys {
		out = append(out, Row{K: k, LV: j.lv[k], RV: j.rvOf(k)})
	}
	return out
}

// Lefts 返回左记录集副本（K -> LV），供批量重算比对。
func (j *Join) Lefts() map[string]int64 {
	out := make(map[string]int64, len(j.lv))
	for k, v := range j.lv {
		out[k] = v
	}
	return out
}

// Batch 批量重算：左记录集与右记录集做左外连接，按 K 升序。
func Batch(lefts, rights map[string]int64) []Row {
	j := New(rstore.New())
	for k, v := range rights {
		j.rs.Put(k, v)
	}
	ks := make([]string, 0, len(lefts))
	for k := range lefts {
		ks = append(ks, k)
	}
	for i := 1; i < len(ks); i++ { // 插入排序，保持 keys 有序
		for p := i; p > 0 && ks[p] < ks[p-1]; p-- {
			ks[p], ks[p-1] = ks[p-1], ks[p]
		}
	}
	j.keys = ks
	j.lv = lefts
	return j.View()
}

// LastCheckLogarithmic 报告最近一次 PutR 定位的检查个数是否为对数量级
// （只暴露判定结论，不暴露计数器数值本身）。
func (j *Join) LastCheckLogarithmic() bool {
	bound, p := 3, 1
	for p < len(j.keys) {
		p <<= 1
		bound += 2
	}
	return j.checked <= bound
}
