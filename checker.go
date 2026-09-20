package ontology

import (
	"errors"
	"fmt"
	"sync"
)

// 哨兵错误，可用 errors.Is 判断。
var (
	ErrDuplicatePK    = errors.New("ontology: duplicate primary key")
	ErrRecordNotFound = errors.New("ontology: record not found")
)

// Checker 是带键规范化的唯一约束检查器。
// 它保存记录的原始值，并维护"约束 -> 规范化键 -> 主键"的索引。
// 所有方法都可并发调用。
type Checker struct {
	mu      sync.Mutex
	opts    NormOptions
	cons    []Constraint
	records map[string]map[string]Value
	index   map[string]map[string]string
}

// NewChecker 用给定规范化选项与约束集合创建检查器。
func NewChecker(opts NormOptions, cons ...Constraint) *Checker {
	return &Checker{
		opts:    opts,
		cons:    append([]Constraint(nil), cons...),
		records: make(map[string]map[string]Value),
		index:   make(map[string]map[string]string),
	}
}

// Insert 写入一条记录；与已有记录冲突时返回 *ConflictError。
func (c *Checker) Insert(pk string, props map[string]Value) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	return insertInto(c.records, c.index, c.opts, c.cons, pk, props, -1, nil)
}

// Delete 删除一条记录及其全部索引项。
func (c *Checker) Delete(pk string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	return deleteFrom(c.records, c.index, c.opts, c.cons, pk)
}

// Get 按主键读回记录；返回的正是调用方写入的原始值。
func (c *Checker) Get(pk string) (map[string]Value, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	props, ok := c.records[pk]
	if !ok {
		return nil, false
	}
	out := make(map[string]Value, len(props))
	for k, v := range props {
		out[k] = v
	}
	return out, true
}

// insertInto 在给定状态上执行插入。opIndex 为批量操作序号（非批量传 -1），
// batchOf 记录批内主键 -> 操作序号，用于报告批内冲突。
func insertInto(records map[string]map[string]Value, index map[string]map[string]string,
	opts NormOptions, cons []Constraint, pk string, props map[string]Value,
	opIndex int, batchOf map[string]int) error {
	if _, dup := records[pk]; dup {
		return fmt.Errorf("%w: %q", ErrDuplicatePK, pk)
	}
	type pending struct {
		con string
		key string
	}
	var adds []pending
	for _, con := range cons {
		key, norm, raw, ok := con.normKey(props, opts)
		if !ok {
			continue
		}
		if existingPK, clash := index[con.Name][key]; clash {
			existingRaw := rawColumns(records[existingPK], con.Columns)
			return &ConflictError{
				Constraint:      con.Name,
				Columns:         append([]string(nil), con.Columns...),
				ExistingPK:      existingPK,
				IncomingPK:      pk,
				NormKey:         displayKey(norm),
				IncomingValues:  raw,
				ExistingValues:  existingRaw,
				IncomingOpIndex: opIndex,
				ExistingOpIndex: batchIndex(batchOf, existingPK),
			}
		}
		adds = append(adds, pending{con: con.Name, key: key})
	}
	stored := make(map[string]Value, len(props))
	for k, v := range props {
		stored[k] = v
	}
	records[pk] = stored
	for _, a := range adds {
		if index[a.con] == nil {
			index[a.con] = make(map[string]string)
		}
		index[a.con][a.key] = pk
	}
	return nil
}

// deleteFrom 从给定状态删除记录及其索引项。
func deleteFrom(records map[string]map[string]Value, index map[string]map[string]string,
	opts NormOptions, cons []Constraint, pk string) error {
	props, ok := records[pk]
	if !ok {
		return fmt.Errorf("%w: %q", ErrRecordNotFound, pk)
	}
	for _, con := range cons {
		key, _, _, participates := con.normKey(props, opts)
		if participates {
			delete(index[con.Name], key)
		}
	}
	delete(records, pk)
	return nil
}

// rawColumns 取出记录在各列上的原始值（缺失列记为 NULL）。
func rawColumns(props map[string]Value, cols []string) []Value {
	out := make([]Value, len(cols))
	for i, col := range cols {
		if v, ok := props[col]; ok {
			out[i] = v
		} else {
			out[i] = Null()
		}
	}
	return out
}

// batchIndex 返回主键在批内的操作序号，不在批内返回 -1。
func batchIndex(batchOf map[string]int, pk string) int {
	if batchOf == nil {
		return -1
	}
	if i, ok := batchOf[pk]; ok {
		return i
	}
	return -1
}
