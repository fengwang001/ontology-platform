package ontology

import "fmt"

// Migrate 原子地执行一次属性定义迁移。
//
// 语义：以 m.NewProps 构建新版本，生效区间 [EffectiveFrom, +inf)；
// 生效区间被完全遮蔽的旧版本作废，被部分遮蔽的旧版本截断。
// 记录时刻落在 [EffectiveFrom, +inf) 内的全部既有事实必须满足
// 新定义约束（必填属性存在、取值可强制转换），否则迁移整体失败。
//
// 原子性：校验与新版本列表构建均在原状态副本上进行，提交只是
// 一次切片头交换；注入故障或校验失败时原状态完全不变，
// 外部不可能观察到部分迁移的中间状态。
func (s *Store) Migrate(m Migration) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	ts, ok := s.types[m.TypeID]
	if !ok {
		return fmt.Errorf("unknown object type %q", m.TypeID)
	}

	// 校验：记录时刻 >= EffectiveFrom 的既有事实必须满足新定义。
	if err := s.validateMigrationLocked(m); err != nil {
		s.recordAudit(DecisionRecord{
			Op:      OpMigrate,
			Input:   migrateInputSummary(m),
			Verdict: VerdictError,
			Detail:  err.Error(),
		})
		return err
	}

	if s.failpoint == FailAfterValidation {
		return ErrInjectedFault
	}

	// 在副本上构建新版本序列。
	newVersion := SchemaVersion{
		ID:    ts.nextID,
		Props: cloneProps(m.NewProps),
		From:  m.EffectiveFrom,
		To:    openEnd,
	}
	kept := make([]SchemaVersion, 0, len(ts.versions)+1)
	for _, v := range ts.versions {
		if v.From >= m.EffectiveFrom {
			// 完全被遮蔽：作废，不进入新序列。
			continue
		}
		if v.To > m.EffectiveFrom {
			// 部分遮蔽：截断右端点。
			v.To = m.EffectiveFrom
		}
		kept = append(kept, v)
	}
	kept = append(kept, newVersion)

	if s.failpoint == FailBeforeCommit {
		return ErrInjectedFault
	}

	// 原子提交：单次赋值，立即可见于所有后续写入与展开。
	ts.versions = kept
	ts.nextID++
	ts.allPropsCache = unionPropNames(ts.versions)

	s.recordAudit(DecisionRecord{
		Op:       OpMigrate,
		Input:    migrateInputSummary(m),
		Verdict:  VerdictOK,
		Versions: []int64{newVersion.ID},
	})
	return nil
}

// validateMigrationLocked 校验既有数据是否满足新定义约束。
func (s *Store) validateMigrationLocked(m Migration) error {
	for _, h := range s.objects {
		if h.typeID != m.TypeID {
			continue
		}
		for _, cl := range h.byValid {
			for _, f := range cl.facts {
				if f.RecordTime < m.EffectiveFrom {
					continue
				}
				if err := validateFactAgainst(f, m.NewProps); err != nil {
					return newError(ErrCodeMigrationValidation,
						"object %q fact(valid=%d, record=%d): %s",
						f.ObjectID, f.ValidTime, f.RecordTime, err)
				}
			}
		}
	}
	return nil
}

func validateFactAgainst(f Fact, props map[string]PropertyDef) error {
	for name, pd := range props {
		v, present := f.Values[name]
		if !present {
			if pd.Required {
				return fmt.Errorf("required property %q missing", name)
			}
			continue
		}
		if _, ok := v.CoerceTo(pd.Type); !ok {
			return fmt.Errorf("property %q value not coercible to %s", name, pd.Type)
		}
	}
	return nil
}
