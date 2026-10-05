// Package binding 维护转发等价类（fec）到标签的绑定及其属主集合。
package binding

import "errors"

// Placeholder 是 Restore 后占位属主使用的保留客户端号（真实客户端 ≥ 1）。
const Placeholder uint32 = 0

const MaxQ = 1_000_000

var ErrInvalid = errors.New("binding: invalid argument")

// Owner 描述一条属主关系；Stale 为真时 Deadline 为陈旧截止时刻。
type Owner struct {
	Stale    bool
	Deadline int64
}

// Entry 是一条 fec 绑定：一个标签加属主集合。
type Entry struct {
	Label  int
	Owners map[uint32]Owner
}

// Table 是 fec→Entry 的绑定表，并维护每客户端属主计数。
type Table struct {
	q       int
	byFec   map[string]*Entry
	fecsOf  map[uint32]map[string]struct{}
	countOf map[uint32]int
}

func New(q int) (*Table, error) {
	if q < 1 || q > MaxQ {
		return nil, ErrInvalid
	}
	return &Table{
		q:       q,
		byFec:   make(map[string]*Entry),
		fecsOf:  make(map[uint32]map[string]struct{}),
		countOf: make(map[uint32]int),
	}, nil
}

// Limit 返回每客户端属主关系上限 Q。
func (t *Table) Limit() int { return t.q }

// Get 返回 fec 的绑定。
func (t *Table) Get(fec string) (*Entry, bool) {
	e, ok := t.byFec[fec]
	return e, ok
}

// Count 返回 client 当前的属主关系数（陈旧也计入，占位属主不计）。
func (t *Table) Count(client uint32) int { return t.countOf[client] }

// Create 建立 fec→label 的空属主绑定（属主随后由 AddOwner/AddPlaceholder 挂上）。
func (t *Table) Create(fec string, label int) {
	t.byFec[fec] = &Entry{Label: label, Owners: make(map[uint32]Owner)}
}

// Release 删除整条绑定，返回其标签；调用方须先处理属主计数。
func (t *Table) Release(fec string) (int, bool) {
	e, ok := t.byFec[fec]
	if !ok {
		return 0, false
	}
	delete(t.byFec, fec)
	return e.Label, true
}

// AddOwner 为真实客户端挂上属主关系并计数。
func (t *Table) AddOwner(fec string, client uint32, o Owner) {
	e := t.byFec[fec]
	e.Owners[client] = o
	t.countOf[client]++
	set := t.fecsOf[client]
	if set == nil {
		set = make(map[string]struct{})
		t.fecsOf[client] = set
	}
	set[fec] = struct{}{}
}

// AddPlaceholder 挂上占位属主（client=0，不计数、不索引）。
func (t *Table) AddPlaceholder(fec string, deadline int64) {
	t.byFec[fec].Owners[Placeholder] = Owner{Stale: true, Deadline: deadline}
}

// RemoveOwner 移除一条属主关系；返回标签与是否已删空整条绑定。
func (t *Table) RemoveOwner(fec string, client uint32) (label int, last bool, ok bool) {
	e, ok := t.byFec[fec]
	if !ok {
		return 0, false, false
	}
	if _, ok := e.Owners[client]; !ok {
		return 0, false, false
	}
	delete(e.Owners, client)
	if client != Placeholder {
		t.countOf[client]--
		delete(t.fecsOf[client], fec)
	}
	if len(e.Owners) == 0 {
		delete(t.byFec, fec)
		return e.Label, true, true
	}
	return e.Label, false, true
}

// SetNormal 把 client 在 fec 上的陈旧属主关系转为正常。
func (t *Table) SetNormal(fec string, client uint32) {
	if e, ok := t.byFec[fec]; ok {
		if _, ok := e.Owners[client]; ok {
			e.Owners[client] = Owner{}
		}
	}
}

// MarkStale 把 client 在 fec 上的正常属主关系标为截止 deadline 的陈旧关系；
// 已陈旧的保持原截止时刻。返回是否新标记。
func (t *Table) MarkStale(fec string, client uint32, deadline int64) bool {
	e, ok := t.byFec[fec]
	if !ok {
		return false
	}
	o, ok := e.Owners[client]
	if !ok || o.Stale {
		return false
	}
	e.Owners[client] = Owner{Stale: true, Deadline: deadline}
	return true
}

// FecsOf 返回 client 的全部属主关系所在 fec（快照）。
func (t *Table) FecsOf(client uint32) []string {
	set := t.fecsOf[client]
	out := make([]string, 0, len(set))
	for f := range set {
		out = append(out, f)
	}
	return out
}

// Fecs 返回当前全部已绑定 fec（快照）。
func (t *Table) Fecs() []string {
	out := make([]string, 0, len(t.byFec))
	for f := range t.byFec {
		out = append(out, f)
	}
	return out
}
