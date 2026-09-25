// Package table 是行存储（id → {Name, Card, Phone}），并按角色组装脱敏视图。
// 依赖 mask，反向依赖不允许。
package table

import (
	"errors"
	"sort"
	"strings"
	"sync"

	"ontology/mask"
)

// ErrInvalidID 是 ID 非法（ID < 0）的哨兵错误，与 mask 包两类错误互不相同。
var ErrInvalidID = errors.New("table: id must be >= 0")

// Row 是对外可见的行：写入时承载原始值，读出时承载该角色下的掩码值。
type Row struct {
	ID                int
	Name, Card, Phone string
}

// row 是内部存储，原始值只存在这里。
type row struct{ name, card, phone string }

// Table 进程内存存储，RWMutex 保证并发 Read / 写安全。
type Table struct {
	mu   sync.RWMutex
	rows map[int]row
}

// New 创建空表。
func New() *Table { return &Table{rows: map[int]row{}} }

// Insert 插入（或按 ID 覆盖）一行。ID < 0 整体失败且不留痕：先校验后写入。
func (t *Table) Insert(r Row) error {
	if r.ID < 0 {
		return ErrInvalidID
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	// Clone 切断与调用方子串可能共享的底层数组。
	t.rows[r.ID] = row{strings.Clone(r.Name), strings.Clone(r.Card), strings.Clone(r.Phone)}
	return nil
}

// Read 返回该角色下所有行（ID 升序）的脱敏视图。
// 每行都是新构造的结构；未知角色整体失败且不改状态。
func (t *Table) Read(role string) ([]Row, error) {
	// 锁外先判定角色：任何错误都发生在组装之前，状态不可能被改动。
	probe, err := mask.MaskColumn(role, "name", "")
	if err != nil {
		return nil, err
	}
	_ = probe

	t.mu.RLock()
	defer t.mu.RUnlock()

	ids := make([]int, 0, len(t.rows))
	for id := range t.rows {
		ids = append(ids, id)
	}
	sort.Ints(ids)

	out := make([]Row, 0, len(ids))
	for _, id := range ids {
		r := t.rows[id]
		n, err := mask.MaskColumn(role, "name", r.name)
		if err != nil {
			return nil, err
		}
		c, err := mask.MaskColumn(role, "card", r.card)
		if err != nil {
			return nil, err
		}
		p, err := mask.MaskColumn(role, "phone", r.phone)
		if err != nil {
			return nil, err
		}
		out = append(out, Row{ID: id, Name: n, Card: c, Phone: p})
	}
	return out, nil
}
