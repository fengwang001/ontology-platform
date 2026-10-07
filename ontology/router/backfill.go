package router

import "ontology/ontology/migration"

// BackfillOutcome 描述一次回填应用的结果。
type BackfillOutcome int

const (
	// BackfillApplied 回填已应用,实例转换为新版本结构。
	BackfillApplied BackfillOutcome = iota
	// BackfillSkippedStale 回填被跳过:实例已被一次并发写入抢先回填
	// (或被删除后重建),不得覆盖已有的新版本数据,这不是错误。
	BackfillSkippedStale
	// BackfillSkippedDeleted 回填被跳过:实例已被删除,不得复活。
	BackfillSkippedDeleted
)

func (o BackfillOutcome) String() string {
	switch o {
	case BackfillApplied:
		return "applied"
	case BackfillSkippedStale:
		return "skipped-stale"
	case BackfillSkippedDeleted:
		return "skipped-deleted"
	default:
		return "unknown"
	}
}

// BackfillTask 是一次两阶段回填:Prepare 阶段在锁内快照实例数据与代际号,
// 并按当时的迁移声明现算出新版本数据;Apply 阶段重新加锁做
// compare-and-swap,识别并跳过竞争,保证回填不会覆盖并发写入、
// 也不会复活已删除的实例。
type BackfillTask struct {
	svc        *Service
	id         string
	found      bool
	generation uint64
	oldData    map[string]any
	newData    map[string]any
}

// PrepareBackfill 为指定实例准备一次回填。实例不存在或已回填时,
// 返回的 Task 在 Apply 时会以相应的跳过结果收场。
func (s *Service) PrepareBackfill(id string) *BackfillTask {
	s.mu.Lock()
	defer s.mu.Unlock()
	t := &BackfillTask{svc: s, id: id}
	inst, ok := s.instances[id]
	if !ok {
		return t
	}
	t.found = true
	t.generation = inst.generation
	if inst.migrated {
		// 已回填:无需再次转换,Apply 时按代际号识别后直接跳过。
		return t
	}
	t.oldData = inst.data
	t.newData = s.decl.ForwardView(inst.data)
	return t
}

// Apply 应用一次回填。回填本身视为一次特殊写入,与正常写入、删除
// 在同一个仲裁锁下串行化。
func (t *BackfillTask) Apply() BackfillOutcome {
	s := t.svc
	s.mu.Lock()
	defer s.mu.Unlock()
	inst, ok := s.instances[t.id]
	if !ok {
		return BackfillSkippedDeleted
	}
	if !t.found || inst.generation != t.generation || inst.migrated {
		return BackfillSkippedStale
	}
	inst.data = t.newData
	inst.migrated = true
	inst.generation++
	s.decl.MarkEffective(t.oldData)
	return BackfillApplied
}

// Declaration 返回服务持有的迁移声明,供回填与测试观测(只读使用)。
func (s *Service) Declaration() *migration.Declaration {
	return s.decl
}
