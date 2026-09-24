// Package join 维护 L、R 两张表与物化宽表，keyed 操作产出变更日志。
package join

import (
	"errors"
	"sort"
	"sync"

	"ontology/jrow"
)

// 四类可判定哨兵错误，互不相同。
var (
	ErrEmptyKey   = errors.New("join: 空 key")
	ErrNotFound   = errors.New("join: 删除不存在的 key")
	ErrCapacity   = errors.New("join: 超过容量上限")
	ErrBadMaxKeys = errors.New("join: maxKeys 必须为正数")
)

// J 是双流 join 物化器，并发安全。
type J struct {
	mu      sync.Mutex
	l, r    map[string]int
	wide    map[string][2]int
	maxKeys int
}

// New 创建物化器；maxKeys <= 0 时整体失败。
func New(maxKeys int) (*J, error) {
	if maxKeys <= 0 {
		return nil, ErrBadMaxKeys
	}
	return &J{l: map[string]int{}, r: map[string]int{}, wide: map[string][2]int{}, maxKeys: maxKeys}, nil
}

// PutL 写入 L[key]=lv，返回本次产出的变更日志；重复更新幂等无输出。
func (j *J) PutL(key string, lv int) ([]jrow.Change, error) {
	j.mu.Lock()
	defer j.mu.Unlock()
	if key == "" {
		return nil, ErrEmptyKey
	}
	row := jrow.Locate(j.l, j.r, key)
	if row.InL && row.LV == lv {
		return nil, nil // 重复更新，幂等
	}
	if !row.InL && len(j.l) >= j.maxKeys {
		return nil, ErrCapacity
	}
	nr, chs := row.PutL(key, lv)
	j.l[key] = lv
	j.apply(nr, chs)
	return chs, nil
}

// PutR 与 PutL 对称。
func (j *J) PutR(key string, rv int) ([]jrow.Change, error) {
	j.mu.Lock()
	defer j.mu.Unlock()
	if key == "" {
		return nil, ErrEmptyKey
	}
	row := jrow.Locate(j.l, j.r, key)
	if row.InR && row.RV == rv {
		return nil, nil
	}
	if !row.InR && len(j.r) >= j.maxKeys {
		return nil, ErrCapacity
	}
	nr, chs := row.PutR(key, rv)
	j.r[key] = rv
	j.apply(nr, chs)
	return chs, nil
}

// DelL 删除 L[key]；key 不存在时整体失败。
func (j *J) DelL(key string) ([]jrow.Change, error) {
	j.mu.Lock()
	defer j.mu.Unlock()
	if key == "" {
		return nil, ErrEmptyKey
	}
	row := jrow.Locate(j.l, j.r, key)
	if !row.InL {
		return nil, ErrNotFound
	}
	nr, chs := row.DelL(key)
	delete(j.l, key)
	j.apply(nr, chs)
	return chs, nil
}

// DelR 与 DelL 对称。
func (j *J) DelR(key string) ([]jrow.Change, error) {
	j.mu.Lock()
	defer j.mu.Unlock()
	if key == "" {
		return nil, ErrEmptyKey
	}
	row := jrow.Locate(j.l, j.r, key)
	if !row.InR {
		return nil, ErrNotFound
	}
	nr, chs := row.DelR(key)
	delete(j.r, key)
	j.apply(nr, chs)
	return chs, nil
}

// apply 按变更日志同步维护物化宽表，保证与朴素重算逐行一致。
func (j *J) apply(_ jrow.Row, chs []jrow.Change) {
	for _, c := range chs {
		if c.Op == '+' {
			j.wide[c.Key] = [2]int{c.LV, c.RV}
		} else {
			delete(j.wide, c.Key)
		}
	}
}

// WideTable 按 key 升序返回当前全部宽表行。
func (j *J) WideTable() []jrow.WideRow {
	j.mu.Lock()
	defer j.mu.Unlock()
	rows := make([]jrow.WideRow, 0, len(j.wide))
	for k, v := range j.wide {
		rows = append(rows, jrow.WideRow{Key: k, LV: v[0], RV: v[1]})
	}
	sort.Slice(rows, func(a, b int) bool { return rows[a].Key < rows[b].Key })
	return rows
}
