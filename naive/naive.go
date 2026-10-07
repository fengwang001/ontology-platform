// Package naive 提供一个独立维护「全部实例全量快照」、每次查询都全量扫描
// 重新计算聚合值的朴素参照模型，供随机差分测试逐条对照真实子系统的结果。
//
// 它刻意与 ontology.Kernel 共享同一套输入/输出/错误类型（schema.go 与
// errors.go），但不共享任何实现：不读取增量索引，查询时扫描该类型下全部
// 实例记录，因此它同时充当「聚合是源实例纯粹派生物」这一不变量的口径来源，
// 以及查询成本对照（朴素模型扫描数 = 实例总数，真实子系统 = 0 次实例读取）。
package naive

import (
	"sync"

	"ontology"
)

// Model 是朴素参照模型：全量实例快照 + 全表扫描重算。
type Model struct {
	mu     sync.Mutex
	schema map[string]ontology.ObjectTypeSchema
	views  map[string]ontology.AggregateDef
	byType map[string]map[string]*ontology.Record
	sn     int64
}

func New() *Model {
	return &Model{
		schema: map[string]ontology.ObjectTypeSchema{},
		views:  map[string]ontology.AggregateDef{},
		byType: map[string]map[string]*ontology.Record{},
	}
}

func (m *Model) RegisterType(s ontology.ObjectTypeSchema) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.schema[s.Name] = s
	m.byType[s.Name] = map[string]*ontology.Record{}
}

func (m *Model) RegisterView(d ontology.AggregateDef) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	s, ok := m.schema[d.SourceType]
	if !ok {
		return &ontology.Error{Kind: ontology.ErrInvalidArgument, Message: "unknown source type"}
	}
	if d.Name == "" || s.Attrs[d.GroupBy] != ontology.TypeString {
		return &ontology.Error{Kind: ontology.ErrInvalidArgument, Message: "bad group-by"}
	}
	vt := s.Attrs[d.ValueField]
	if vt != ontology.TypeInt && vt != ontology.TypeDouble {
		return &ontology.Error{Kind: ontology.ErrInvalidArgument, Message: "bad value field"}
	}
	m.views[d.Name] = d
	return nil
}

// Write 按与 Kernel 完全一致的语义与拒绝次序处理写入。
func (m *Model) Write(w ontology.Write) (ontology.CommitResult, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	_, errKind := m.validate(w.Type, w.Key, w.Attrs)
	if errKind != "" {
		return ontology.CommitResult{}, &ontology.Error{Kind: errKind, Message: "invalid argument"}
	}
	records := m.byType[w.Type]
	old, existed := records[w.Key]
	var cur int64
	if existed {
		cur = old.Version
	}
	if w.Prev != cur {
		return ontology.CommitResult{}, &ontology.Error{Kind: ontology.ErrVersionConflict, Message: "conflict"}
	}
	m.sn++
	attrs := make(map[string]ontology.Value, len(w.Attrs))
	for k, v := range w.Attrs {
		attrs[k] = v
	}
	records[w.Key] = &ontology.Record{
		Key: w.Key, Version: cur + 1, Attrs: attrs, CommitSN: m.sn,
	}
	return ontology.CommitResult{Key: w.Key, Version: cur + 1, CommitSN: m.sn}, nil
}

// Delete 按与 Kernel 完全一致的语义处理删除，区分「从未提交过」与
// 「已删除后再次删除」两类错误。
func (m *Model) Delete(d ontology.Delete) (ontology.CommitResult, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.schema[d.Type]; !ok || d.Key == "" {
		return ontology.CommitResult{}, &ontology.Error{Kind: ontology.ErrInvalidArgument, Message: "invalid argument"}
	}
	records := m.byType[d.Type]
	old, existed := records[d.Key]
	var cur int64
	if existed {
		cur = old.Version
	}
	if d.Prev != cur {
		return ontology.CommitResult{}, &ontology.Error{Kind: ontology.ErrVersionConflict, Message: "conflict"}
	}
	if !existed {
		return ontology.CommitResult{}, &ontology.Error{Kind: ontology.ErrNotFound, Message: "never committed"}
	}
	if old.Deleted {
		return ontology.CommitResult{}, &ontology.Error{Kind: ontology.ErrAlreadyDeleted, Message: "already deleted"}
	}
	m.sn++
	old.Deleted = true
	old.Version++
	old.CommitSN = m.sn
	old.Attrs = map[string]ontology.Value{}
	return ontology.CommitResult{Key: d.Key, Version: old.Version, Deleted: true, CommitSN: m.sn}, nil
}

// Query 全量扫描该类型下所有实例（含墓碑），逐个用最新可见版本重算指定
// 分组的 SUM。返回 Scanned 供成本对照：它等于该类型实例总数，而真实
// 子系统的对应读数为 0。
func (m *Model) Query(view, group string) (ontology.GroupValue, int, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	def, ok := m.views[view]
	if !ok || group == "" {
		return ontology.GroupValue{}, 0, &ontology.Error{Kind: ontology.ErrInvalidArgument, Message: "bad query"}
	}
	out := ontology.GroupValue{View: view, Group: group}
	scanned := 0
	for _, rec := range m.byType[def.SourceType] {
		scanned++
		if rec.Deleted {
			continue
		}
		if gv, ok := rec.Attrs[def.GroupBy]; ok && gv.Str == group {
			if vv, ok := rec.Attrs[def.ValueField]; ok {
				out.Sum += vv.Num
				out.Members++
			}
		}
	}
	return out, scanned, nil
}

// RecomputeView 重算视图下全部分组，供整视图级别对照。
func (m *Model) RecomputeView(view string) map[string]ontology.GroupValue {
	m.mu.Lock()
	defer m.mu.Unlock()
	def, ok := m.views[view]
	if !ok {
		return nil
	}
	out := map[string]ontology.GroupValue{}
	for _, rec := range m.byType[def.SourceType] {
		if rec.Deleted {
			continue
		}
		gv, ok1 := rec.Attrs[def.GroupBy]
		vv, ok2 := rec.Attrs[def.ValueField]
		if !ok1 || !ok2 {
			continue
		}
		cell := out[gv.Str]
		cell.View, cell.Group = view, gv.Str
		cell.Sum += vv.Num
		cell.Members++
		out[gv.Str] = cell
	}
	return out
}

func (m *Model) validate(typeName, key string, attrs map[string]ontology.Value) (ontology.ObjectTypeSchema, ontology.ErrorKind) {
	s, ok := m.schema[typeName]
	if !ok || key == "" || attrs == nil {
		return ontology.ObjectTypeSchema{}, ontology.ErrInvalidArgument
	}
	groupFields := map[string]bool{}
	for _, v := range m.views {
		if v.SourceType == typeName {
			groupFields[v.GroupBy] = true
		}
	}
	for name, want := range s.Attrs {
		got, present := attrs[name]
		if !present || got.Type != want {
			return s, ontology.ErrInvalidArgument
		}
		if want == ontology.TypeString && groupFields[name] && got.Str == "" {
			return s, ontology.ErrInvalidArgument
		}
	}
	for name := range attrs {
		if _, ok := s.Attrs[name]; !ok {
			return s, ontology.ErrInvalidArgument
		}
	}
	return s, ""
}
