// Package api 是列式物化视图对外的唯一入口：所有读写经单一 RWMutex
// 串行化，Get/Project/Snapshot 持读锁可并发；错误全部是可判定哨兵。
package api

import (
	"errors"
	"sync"

	"ontology/view"
)

// 对外哨兵错误；前三者必须互不相同。
var (
	ErrNoSuchRow = view.ErrNoSuchRow
	ErrDeleted   = view.ErrDeleted
	ErrNoColumns = view.ErrNoColumns
	ErrBadColumn = errors.New("api: column name must be one of A, B, C")
)

// API 是并发安全的物化视图句柄。
type API struct {
	mu sync.RWMutex
	v  *view.View
}

// New 返回空视图。
func New() *API { return &API{v: view.New()} }

func colOf(name string) (view.Col, error) {
	switch name {
	case "A":
		return view.A, nil
	case "B":
		return view.B, nil
	case "C":
		return view.C, nil
	default:
		return 0, ErrBadColumn
	}
}

// Insert 追加一行，返回稳定 rowID。
func (x *API) Insert(a, b, c int64) int {
	x.mu.Lock()
	defer x.mu.Unlock()
	return x.v.Insert(a, b, c)
}

// Update 修改一行的一列；不存在/已删/列名非法整体失败，不留痕。
func (x *API) Update(id int, col string, v int64) error {
	k, err := colOf(col)
	if err != nil {
		return err
	}
	x.mu.Lock()
	defer x.mu.Unlock()
	return x.v.Update(id, k, v)
}

// Delete 对一行置墓碑。
func (x *API) Delete(id int) error {
	x.mu.Lock()
	defer x.mu.Unlock()
	return x.v.Delete(id)
}

// Get 读一行三列，持读锁可与其他只读操作并发。
func (x *API) Get(id int) (int64, int64, int64, error) {
	x.mu.RLock()
	defer x.mu.RUnlock()
	return x.v.Get(id)
}

// Project 按列名子集投影存活行，返回 列名→该列值（行序、列对齐）。
func (x *API) Project(cols []string) (map[string][]int64, error) {
	sel := make([]view.Col, 0, len(cols))
	for _, name := range cols {
		k, err := colOf(name)
		if err != nil {
			return nil, err
		}
		sel = append(sel, k)
	}
	x.mu.RLock()
	defer x.mu.RUnlock()
	raw, err := x.v.Project(sel)
	if err != nil {
		return nil, err
	}
	names := [...]string{"A", "B", "C"}
	out := make(map[string][]int64, len(raw))
	for _, k := range sel {
		out[names[k]] = raw[k]
	}
	return out, nil
}

// Compact 物理压缩死槽。
func (x *API) Compact() {
	x.mu.Lock()
	defer x.mu.Unlock()
	x.v.Compact()
}

// Snapshot 返回内部逐格状态拷贝（三列、存活位、slotOf），供演示/诊断。
func (x *API) Snapshot() (a, b, c []int64, alive []bool, slotOf []int) {
	x.mu.RLock()
	defer x.mu.RUnlock()
	return x.v.Snapshot()
}

// LookupConstantTime 只回布尔判定：各档规模下 Get 经 slotOf 直接下标定位，
// 检查槽数恒为小常数、不随表长线性增长；不暴露计数器数值。
func (x *API) LookupConstantTime(sizes []int) bool {
	x.mu.RLock()
	defer x.mu.RUnlock()
	return x.v.VerifyLookupO1(sizes)
}

// SelfCheck 重放内置操作序列（第三节八步场景 + 随机混合序列），逐条对照
// 行式朴素模型核验四条不变量；操作隔离视图，不改接收者，可并发调用。
func (x *API) SelfCheck() error {
	if e := eightStep(); e != nil {
		return e
	}
	return replayRandom(300, 471)
}
