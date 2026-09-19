package ontology

import "fmt"

// BatchWrite 原子地应用一组混合的 Create/Update/Delete。
// 任一条失败（版本冲突、已删除、不存在、主键冲突、约束违反），
// 整批不生效：版本号不前进、最后写入时间不变。
// 同一主键在一批中出现多次直接拒绝。
func (s *Store) BatchWrite(ops []WriteOp) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	seen := make(map[string]int, len(ops))
	for i, op := range ops {
		key := op.ObjectType + "\x00" + op.PrimaryKey
		if first, dup := seen[key]; dup {
			return &BatchError{Index: i, PrimaryKey: op.PrimaryKey, Err: fmt.Errorf(
				"duplicate primary key in batch (first at op %d)", first)}
		}
		seen[key] = i
	}

	// 第一阶段：全部校验。由于主键互不重复，基于当前状态校验
	// 与按顺序应用后的校验结果等价。
	for i, op := range ops {
		if err := s.checkOp(op); err != nil {
			return &BatchError{Index: i, PrimaryKey: op.PrimaryKey, Err: err}
		}
	}

	// 第二阶段：全部应用。
	for _, op := range ops {
		s.applyOp(op)
	}
	return nil
}

// checkOp 校验单条操作在当前状态下是否可执行；调用方须持有锁。
func (s *Store) checkOp(op WriteOp) error {
	switch op.Kind {
	case OpCreate:
		if err := s.checkProps(op.ObjectType, op.PrimaryKey, op.Properties); err != nil {
			return err
		}
		if rec, ok := s.find(op.ObjectType, op.PrimaryKey); ok && !rec.deleted {
			return &AlreadyExistsError{
				ObjectType: op.ObjectType, PrimaryKey: op.PrimaryKey, Version: rec.version,
			}
		}
		return nil
	case OpUpdate:
		if _, err := s.expectLive(op.ObjectType, op.PrimaryKey, op.ExpectedVersion); err != nil {
			return err
		}
		return s.checkProps(op.ObjectType, op.PrimaryKey, op.Properties)
	case OpDelete:
		_, err := s.expectLive(op.ObjectType, op.PrimaryKey, op.ExpectedVersion)
		return err
	default:
		return fmt.Errorf("unknown op kind %d", op.Kind)
	}
}

// applyOp 应用一条已通过校验的操作；调用方须持有锁。
func (s *Store) applyOp(op WriteOp) {
	rec, ok := s.find(op.ObjectType, op.PrimaryKey)
	if !ok {
		rec = &record{}
		s.bucket(op.ObjectType)[op.PrimaryKey] = rec
	}
	rec.version++
	rec.updatedAt = s.now()
	switch op.Kind {
	case OpCreate, OpUpdate:
		rec.deleted = false
		rec.props = cloneProps(op.Properties)
	case OpDelete:
		rec.deleted = true
		rec.props = nil
	}
}
