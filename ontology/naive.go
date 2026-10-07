package ontologyindex

// NaiveModel 是独立实现的朴素批量重建参照模型（oracle）。
//
// 它刻意不做任何增量维护：保存全部去重后的原始事件，每次对照都从空索引
// 出发，按 (EffectiveAt, EventID) 的合法串行顺序排序后逐条重放，筛出
// 生效于目标版本区间的事件，取每个对象的最后取值。Engine 的正确性以
// “任意操作序列后与 NaiveModel 的批量重建结果逐条相等”为准。
type NaiveModel struct {
	schema     *Schema
	logs       map[string][]ChangeEvent // index id -> 去重事件
	seen       map[string]map[string]struct{}
	specs      map[string]indexSpec
	deprecated map[string]struct{}
}

// NewNaiveModel 创建朴素模型。
func NewNaiveModel(s *Schema) *NaiveModel {
	return &NaiveModel{
		schema:     s,
		logs:       map[string][]ChangeEvent{},
		seen:       map[string]map[string]struct{}{},
		specs:      map[string]indexSpec{},
		deprecated: map[string]struct{}{},
	}
}

// CreateIndex 与 Engine.CreateIndex 对应。
func (m *NaiveModel) CreateIndex(id, objectType, propertyID string, c IndexConstraint) {
	m.specs[id] = indexSpec{id: id, objectType: objectType, propertyID: propertyID, constraint: c}
	m.logs[id] = nil
	m.seen[id] = map[string]struct{}{}
}

// Ingest 按与引擎相同的血缘路由与去重规则保存原始事件，但不做增量计算。
// 返回的首个错误与 Engine.Ingest 的校验语义保持一致（供差分测试同时喂入）。
func (m *NaiveModel) Ingest(ev ChangeEvent) error {
	if !ev.valid() {
		return errUndefined("事件字段不完备: %+v", ev)
	}
	var firstErr error
	for id, spec := range m.specs {
		if spec.objectType != ev.ObjectType {
			continue
		}
		lineage := m.schema.lineageOf(spec.objectType, spec.propertyID)
		if _, ok := lineage[ev.PropertyID]; !ok {
			continue
		}
		if _, dup := m.seen[id][ev.EventID]; dup {
			continue
		}
		if _, dep := m.deprecated[id]; dep {
			err := errDeprecated("索引 %q 的依据属性已废弃且无替代字段", id)
			if firstErr == nil {
				firstErr = err
			}
			continue
		}
		// 与 Engine.validateEvent 相同的 E1/E3 判定（但此处不影响后续重建，
		// 仅用于双方返回错误一致；无效事件不进入事实日志）。
		if err := m.validate(spec, ev); err != nil {
			if firstErr == nil {
				firstErr = err
			}
			continue
		}
		m.seen[id][ev.EventID] = struct{}{}
		m.logs[id] = append(m.logs[id], ev)
	}
	return firstErr
}

func (m *NaiveModel) validate(spec indexSpec, ev ChangeEvent) error {
	var errs []error
	if _, err := m.schema.resolveProperty(spec.objectType, ev.PropertyID, ev.EffectiveAt); err != nil {
		errs = append(errs, err)
	}
	if _, v, ok := m.schema.resolveField(spec.objectType, ev.PropertyID, ev.EffectiveAt); !ok {
		if v != nil {
			errs = append(errs, errUndefined("属性 %q 在逻辑时刻 %d 的版本中尚未定义",
				ev.PropertyID, ev.EffectiveAt))
		} else {
			errs = append(errs, errUndefined("类型 %q 在逻辑时刻 %d 尚未定义",
				spec.objectType, ev.EffectiveAt))
		}
	}
	return highestPriorityError(errs)
}

// DeprecateIndex 标记废弃。
func (m *NaiveModel) DeprecateIndex(id string) { m.deprecated[id] = struct{}{} }

// Rebuild 全量排序重放，重建指定索引在某个版本区间上的期望内容：
// switched=false 时覆盖所有事件（旧版本）；switched=true 时只覆盖
// EffectiveAt >= cutoverAt 的事件（新版本）。
func (m *NaiveModel) Rebuild(id string, switched bool, cutoverAt LogicalClock) (map[Value][]string, error) {
	spec, ok := m.specs[id]
	if !ok {
		return nil, errValidation("索引 %q 不存在", id)
	}
	events := append([]ChangeEvent(nil), m.logs[id]...)
	sortEvents(events)

	winner := map[string]ChangeEvent{}
	for _, ev := range events {
		if switched && ev.EffectiveAt < cutoverAt {
			continue
		}
		winner[ev.ObjectID] = ev // 已按合法顺序排序，后者覆盖前者
	}

	out := map[Value][]string{}
	for _, ev := range winner {
		out[ev.NewValue] = append(out[ev.NewValue], ev.ObjectID)
	}
	for v := range out {
		sortStrings(out[v])
	}

	// 新版本额外返回约束校验结论，使差分测试能对照 E2 回滚行为。
	if switched && spec.constraint == ConstraintUnique {
		for v, objs := range out {
			if len(objs) > 1 {
				return out, errValidation("朴素模型：取值 %v 对应 %d 个对象，违反唯一约束", v, len(objs))
			}
		}
	}
	return out, nil
}
