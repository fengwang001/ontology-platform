package ontology

import (
	"fmt"
	"sync"
)

// Checker 在写入前检查复合唯一约束。
// 它保存记录的原始值，并维护一份仅用于比较的规范化索引。
// 所有方法均可并发调用。
type Checker struct {
	mu          sync.Mutex
	norm        Normalizer
	constraints []Constraint
	records     map[string]Record
	// index[约束名][规范化键] = 主键，只存参与判断的键。
	index map[string]map[string]string
}

// NewChecker 创建检查器。约束名重复、约束无属性或属性名为空时 panic。
func NewChecker(norm Normalizer, constraints ...Constraint) *Checker {
	seen := map[string]bool{}
	for _, con := range constraints {
		if con.Name == "" || seen[con.Name] {
			panic(fmt.Sprintf("ontology: invalid or duplicate constraint name %q", con.Name))
		}
		seen[con.Name] = true
		if len(con.Props) == 0 {
			panic(fmt.Sprintf("ontology: constraint %q has no properties", con.Name))
		}
	}
	c := &Checker{
		norm:        norm,
		constraints: append([]Constraint(nil), constraints...),
		records:     map[string]Record{},
		index:       map[string]map[string]string{},
	}
	for _, con := range constraints {
		c.index[con.Name] = map[string]string{}
	}
	return c
}

// Add 插入一条记录；与已有记录冲突或主键重复时返回错误，状态不变。
func (c *Checker) Add(rec Record) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if _, dup := c.records[rec.PK]; dup {
		return fmt.Errorf("duplicate primary key %q", rec.PK)
	}
	if err := c.checkLocked(rec, -1); err != nil {
		return err
	}
	c.records[rec.PK] = rec
	c.indexAddLocked(rec)
	return nil
}

// Remove 删除主键对应的记录，返回是否存在。
func (c *Checker) Remove(pk string) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	rec, ok := c.records[pk]
	if !ok {
		return false
	}
	delete(c.records, pk)
	c.indexRemoveLocked(rec)
	return true
}

// Get 返回主键对应记录的副本，值保持写入时的原始字节。
func (c *Checker) Get(pk string) (Record, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	rec, ok := c.records[pk]
	if !ok {
		return Record{}, false
	}
	vals := make(map[string]*string, len(rec.Values))
	for k, v := range rec.Values {
		vals[k] = v
	}
	return Record{PK: rec.PK, Values: vals}, true
}

// Len 返回当前存活记录数。
func (c *Checker) Len() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.records)
}

// checkLocked 检查 rec 是否与任一已有记录冲突，调用方须持有锁。
// opIndex 为批量写入中的操作下标，非批量传 -1。
func (c *Checker) checkLocked(rec Record, opIndex int) error {
	for _, con := range c.constraints {
		key, ok := c.keyOf(con, rec)
		if !ok {
			continue
		}
		if existingPK, dup := c.index[con.Name][key]; dup {
			existing := c.records[existingPK]
			return &ConflictError{
				Constraint: con.Name,
				Key:        c.displayKey(con, rec),
				ExistingPK: existingPK,
				Incoming:   propValues(con, rec),
				Existing:   propValues(con, existing),
				OpIndex:    opIndex,
			}
		}
	}
	return nil
}

// indexAddLocked 把 rec 的各约束键加入索引，调用方须持有锁。
func (c *Checker) indexAddLocked(rec Record) {
	for _, con := range c.constraints {
		if key, ok := c.keyOf(con, rec); ok {
			c.index[con.Name][key] = rec.PK
		}
	}
}

// indexRemoveLocked 把 rec 的各约束键移出索引，调用方须持有锁。
func (c *Checker) indexRemoveLocked(rec Record) {
	for _, con := range c.constraints {
		if key, ok := c.keyOf(con, rec); ok {
			delete(c.index[con.Name], key)
		}
	}
}
