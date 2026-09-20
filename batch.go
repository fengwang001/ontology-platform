package ontology

import "sort"

// ApplyBatch 原子地应用一批操作：约束检查是延迟的，
// 先按顺序把整批操作应用到影子副本上，再对结果状态做整体约束检查。
// 任一冲突都会使整批不生效，已有记录不受影响。
func (c *Checker) ApplyBatch(ops []Op) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	shadow := make(map[string]Record, len(c.records)+len(ops))
	for pk, rec := range c.records {
		shadow[pk] = rec
	}
	// pos 记录本批插入的主键对应的操作下标，用于报告批内冲突位置。
	pos := map[string]int{}
	for i, op := range ops {
		if op.Delete {
			delete(shadow, op.PK)
			continue
		}
		if _, dup := shadow[op.Rec.PK]; dup {
			return &BatchOpError{OpIndex: i, Err: &ConflictError{
				Constraint: "(primary key)",
				Key:        op.Rec.PK,
				ExistingPK: op.Rec.PK,
				OpIndex:    i,
			}}
		}
		shadow[op.Rec.PK] = op.Rec
		pos[op.Rec.PK] = i
	}

	if err := c.checkState(shadow, pos); err != nil {
		return err
	}
	c.records = shadow
	c.rebuildIndexLocked()
	return nil
}

// checkState 对一份完整状态做整体唯一性检查。
// 发现冲突时：两条都是本批插入则报 BatchConflictError，
// 否则报指向已有记录的 ConflictError。
func (c *Checker) checkState(state map[string]Record, pos map[string]int) error {
	pks := make([]string, 0, len(state))
	for pk := range state {
		pks = append(pks, pk)
	}
	sort.Strings(pks)

	for _, con := range c.constraints {
		seen := map[string]string{} // 规范化键 -> 主键
		for _, pk := range pks {
			rec := state[pk]
			key, ok := c.keyOf(con, rec)
			if !ok {
				continue
			}
			other, dup := seen[key]
			if !dup {
				seen[key] = pk
				continue
			}
			i, iOK := pos[other]
			j, jOK := pos[pk]
			if iOK && jOK {
				if j < i {
					i, j = j, i
					other, pk = pk, other
				}
				return &BatchConflictError{
					First: i, Second: j,
					Constraint: con.Name,
					Key:        c.displayKey(con, state[pk]),
					FirstPK:    other, SecondPK: pk,
				}
			}
			existingPK, newPK, opIndex := other, pk, j
			if iOK {
				existingPK, newPK, opIndex = pk, other, i
			}
			existing := state[existingPK]
			return &ConflictError{
				Constraint: con.Name,
				Key:        c.displayKey(con, state[newPK]),
				ExistingPK: existingPK,
				Incoming:   propValues(con, state[newPK]),
				Existing:   propValues(con, existing),
				OpIndex:    opIndex,
			}
		}
	}
	return nil
}

// rebuildIndexLocked 从当前记录整体重建索引，调用方须持有锁。
func (c *Checker) rebuildIndexLocked() {
	for _, con := range c.constraints {
		c.index[con.Name] = map[string]string{}
	}
	for _, rec := range c.records {
		c.indexAddLocked(rec)
	}
}
