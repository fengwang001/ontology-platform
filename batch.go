package ontology

// OpKind 是批量操作的种类。
type OpKind int

const (
	// OpInsert 插入一条记录。
	OpInsert OpKind = iota
	// OpDelete 删除一条记录。
	OpDelete
)

// Op 是批量写入中的一条操作。
type Op struct {
	Kind  OpKind
	PK    string
	Props map[string]Value // 仅 OpInsert 使用
}

// InsertOp 构造一个插入操作。
func InsertOp(pk string, props map[string]Value) Op {
	return Op{Kind: OpInsert, PK: pk, Props: props}
}

// DeleteOp 构造一个删除操作。
func DeleteOp(pk string) Op {
	return Op{Kind: OpDelete, PK: pk}
}

// Apply 以延迟检查的方式原子地应用一批操作：
// 先在状态副本上按序执行全部操作（因此批内先删后插同一规范化键
// 可以成功），全部通过后才一次性提交；任一操作冲突则整批不生效，
// 已有记录不受任何影响。
func (c *Checker) Apply(ops []Op) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	records := make(map[string]map[string]Value, len(c.records)+len(ops))
	for pk, props := range c.records {
		records[pk] = props
	}
	index := make(map[string]map[string]string, len(c.index))
	for name, m := range c.index {
		dup := make(map[string]string, len(m))
		for k, v := range m {
			dup[k] = v
		}
		index[name] = dup
	}

	batchOf := make(map[string]int, len(ops))
	for i, op := range ops {
		var err error
		switch op.Kind {
		case OpInsert:
			err = insertInto(records, index, c.opts, c.cons, op.PK, op.Props, i, batchOf)
		case OpDelete:
			err = deleteFrom(records, index, c.opts, c.cons, op.PK)
		}
		if err != nil {
			return err
		}
		if op.Kind == OpInsert {
			batchOf[op.PK] = i
		}
	}

	c.records = records
	c.index = index
	return nil
}
