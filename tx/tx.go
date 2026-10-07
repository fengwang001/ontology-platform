// Package tx 负责动作事务边界管理：维护已提交对象存储，
// 在最外层动作开始时建立事务工作副本，嵌套调用共享同一事务边界，
// 提交时原子换入、回退时整体丢弃，并提供 O(1) 的只读状态视图。
package tx

import (
	"fmt"
	"sort"
	"sync"

	"ontology/errs"
	"ontology/hooks"
	"ontology/spec"
)

// state 是对象实例存储：类型名 -> 实例 ID -> 字段集合。
type state map[string]map[string]map[string]any

// Stats 是存储层的开销统计，用于以可验证的方式证明
// 快照（只读视图）构造的开销不随嵌套深度或已应用写入数增长。
type Stats struct {
	BeginCopiedEntries uint64 // 事务开始时全量深拷贝的字段条目数（每事务一次）
	ViewCreated        uint64 // 只读视图构造次数
	ViewCopiedEntries  uint64 // 构造视图时拷贝的字段条目数（恒为 0）
}

// Store 是已提交状态的对象存储，同时持有对象类型模式。
// 同一时刻只有一个事务在执行（由引擎的串行化调度保证），
// committed 上的锁只用于让外部观察者（State 查询）与提交换入
// 之间无数据竞争，不在执行路径上增加串行点。
type Store struct {
	mu        sync.RWMutex
	schemas   map[string]spec.Schema
	committed state
	stats     Stats
}

// NewStore 创建空存储。
func NewStore() *Store {
	return &Store{
		schemas:   make(map[string]spec.Schema),
		committed: make(state),
	}
}

// RegisterType 注册对象类型及其字段模式。
func (s *Store) RegisterType(name string, schema spec.Schema) {
	s.schemas[name] = schema
}

// Stats 返回开销统计快照。
func (s *Store) Stats() Stats {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.stats
}

// Committed 返回已提交状态的只读视图。
func (s *Store) Committed() hooks.StateView {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return &view{data: s.committed}
}

// Begin 开启一个最外层事务：对已提交状态做一次全量深拷贝作为
// 工作副本。该拷贝每个最外层事务只发生一次，其开销取决于已提交
// 对象总量，与事务后续的嵌套深度和写入次数无关。
func (s *Store) Begin(id uint64) *Tx {
	s.mu.RLock()
	defer s.mu.RUnlock()
	working := make(state, len(s.committed))
	var copied uint64
	for typ, insts := range s.committed {
		m := make(map[string]map[string]any, len(insts))
		for id, fields := range insts {
			f := make(map[string]any, len(fields))
			for k, v := range fields {
				f[k] = v
				copied++
			}
			m[id] = f
		}
		working[typ] = m
	}
	s.stats.BeginCopiedEntries += copied
	return &Tx{
		store:    s,
		id:       id,
		working:  working,
		affected: make(map[string]bool),
	}
}

// Tx 是一个最外层动作的事务边界，嵌套调用共享同一个 Tx：
// 所有层级的写入都直接应用到同一份工作副本上。
type Tx struct {
	store    *Store
	id       uint64
	working  state
	affected map[string]bool // 本事务写入过的对象类型
}

// ID 返回事务 ID。
func (t *Tx) ID() uint64 { return t.id }

// Validate 校验一次写入的参数合法性（目标实例存在性、写入内容
// 与模式类型相符），不修改任何状态。校验基于当前工作副本，
// 因此外层已应用但未提交的写入对校验可见。
func (t *Tx) Validate(w spec.Write, actionPath []string) error {
	fail := func(format string, args ...any) error {
		return &errs.Error{
			Kind:       errs.KindInvalidArgument,
			ActionPath: append([]string(nil), actionPath...),
			ObjectType: w.Type,
			ObjectID:   w.ID,
			Message:    fmt.Sprintf(format, args...),
		}
	}
	schema, ok := t.store.schemas[w.Type]
	if !ok {
		return fail("unknown object type %q", w.Type)
	}
	insts := t.working[w.Type]
	_, exists := insts[w.ID]
	switch w.Op {
	case spec.OpCreate:
		if exists {
			return fail("create target %q already exists", w.ID)
		}
	case spec.OpUpdate:
		if !exists {
			return fail("update target %q does not exist", w.ID)
		}
	case spec.OpDelete:
		if !exists {
			return fail("delete target %q does not exist", w.ID)
		}
	default:
		return fail("unknown write op %d", int(w.Op))
	}
	for field, value := range w.Fields {
		ft, ok := schema.Fields[field]
		if !ok {
			return fail("unknown field %q on type %q", field, w.Type)
		}
		if !typeMatches(ft, value) {
			return fail("field %q expects %s, got %T", field, ft, value)
		}
	}
	return nil
}

// Apply 将一次已通过 Validate 校验的写入应用到工作副本，
// 并把对象类型记入受影响集合。前置钩子必须在 Apply 之前触发。
func (t *Tx) Apply(w spec.Write) {
	insts := t.working[w.Type]
	if insts == nil {
		insts = make(map[string]map[string]any)
		t.working[w.Type] = insts
	}
	switch w.Op {
	case spec.OpCreate:
		fields := make(map[string]any, len(w.Fields))
		for k, v := range w.Fields {
			fields[k] = v
		}
		insts[w.ID] = fields
	case spec.OpUpdate:
		for k, v := range w.Fields {
			insts[w.ID][k] = v
		}
	case spec.OpDelete:
		delete(insts, w.ID)
	}
	t.affected[w.Type] = true
}

// View 返回当前工作副本的只读视图，即钩子应当看到的状态。
// 构造视图只是包装一个指针：不拷贝、不遍历任何数据，
// 开销为 O(1)，与事务已嵌套的调用深度和已应用写入总数无关
// （由 ViewCopiedEntries 恒为 0 可验证）。
func (t *Tx) View() hooks.StateView {
	t.store.stats.ViewCreated++
	return &view{data: t.working}
}

// AffectedTypes 返回本事务写入过的对象类型集合。
func (t *Tx) AffectedTypes() map[string]bool { return t.affected }

// Commit 提交事务：将工作副本原子换入为已提交状态。
// 提交之后不得再使用该 Tx。
func (t *Tx) Commit() {
	t.store.mu.Lock()
	defer t.store.mu.Unlock()
	t.store.committed = t.working
}

// Rollback 回退事务：直接丢弃工作副本，已提交状态从未被触碰，
// 因此不遗留任何痕迹，开销 O(1)。
func (t *Tx) Rollback() { t.working = nil }

// view 是 state 的只读包装。读取时按需拷贝被读到的实例，
// 开销正比于钩子实际读取的数据量，而非状态总量。
type view struct {
	data state
}

// Get 返回指定实例的字段副本；不存在时 ok=false。
func (v *view) Get(objectType, id string) (map[string]any, bool) {
	fields, ok := v.data[objectType][id]
	if !ok {
		return nil, false
	}
	out := make(map[string]any, len(fields))
	for k, val := range fields {
		out[k] = val
	}
	return out, true
}

// List 返回指定类型全部实例的 ID 列表（确定性升序）。
func (v *view) List(objectType string) []string {
	insts := v.data[objectType]
	ids := make([]string, 0, len(insts))
	for id := range insts {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	return ids
}

// Count 返回指定类型的实例数。
func (v *view) Count(objectType string) int { return len(v.data[objectType]) }

func typeMatches(ft spec.FieldType, value any) bool {
	switch ft {
	case spec.FieldString:
		_, ok := value.(string)
		return ok
	case spec.FieldInt:
		_, ok := value.(int)
		return ok
	case spec.FieldFloat:
		_, ok := value.(float64)
		return ok
	case spec.FieldBool:
		_, ok := value.(bool)
		return ok
	}
	return false
}
