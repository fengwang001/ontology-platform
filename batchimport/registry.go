package batchimport

import (
	"reflect"
	"sync"
)

// Instance 是某个对象类型下的一个实例，按主键索引，字段为类型化的值。
type Instance struct {
	Type   string
	ID     string
	Fields map[string]any
}

// Record 是批量导入有序列表中的一条待创建/更新记录（upsert 语义）。
type Record struct {
	Type   string
	ID     string
	Fields map[string]any
}

// PreHook 是对象类型注册的前置校验钩子。
type PreHook func(ctx *HookContext, rec Record) error

// PostHook 是对象类型注册的批次级后置校验钩子。
type PostHook func(ctx *PostHookContext) error

// ObjectType 描述一个对象类型：字段 -> Go 具体类型名，以及钩子注册。
type ObjectType struct {
	Name       string
	FieldTypes map[string]string
	PreHooks   []PreHook
	PostHooks  []PostHook
}

// HookContext 提供前置钩子运行时所需的只读批内视图与可变派生暂存。
//
// 视图严格遵循列表顺序：钩子只能看到全局存储 + 本批次中排在当前记录之前
// 且已通过前置钩子校验的记录效果。Scratch 是整批共享的派生值暂存区，
// 钩子按列表顺序确定性地增量维护其中的派生值（例如聚合统计），
// 从而把可见性解析开销控制在「实际访问的记录数」以内。
type HookContext struct {
	index      int
	reg        *Registry
	st         *staging
	scratch    map[string]any
	visibleIDs func(typeName string) []string
}

// enterHook / leaveHook 包围一次前置钩子调用，统计本次可见性解析步数。
func (c *HookContext) enterHook() int { return c.st.steps }

func (c *HookContext) leaveHook(start int) int { return c.st.steps - start }

// Index 返回当前记录在输入列表中的下标。
func (c *HookContext) Index() int { return c.index }

// Get 按主键读取当前钩子可见范围内的实例。
func (c *HookContext) Get(typeName, id string) (Instance, bool) {
	return c.st.get(c.reg, typeName, id)
}

// VisibleIDs 枚举某类型下当前钩子可见的全部主键。
// 可见性解析由执行环境（生产覆盖层 / 朴素全量状态）提供，钩子本身不依赖
// 任何具体存储结构，从而保证两类环境下钩子行为严格一致。
func (c *HookContext) VisibleIDs(typeName string) []string {
	return c.visibleIDs(typeName)
}

// Scratch 返回整批共享的派生值暂存区。
func (c *HookContext) Scratch() map[string]any { return c.scratch }

// PostHookContext 提供后置钩子运行时所需的最终状态视图。
type PostHookContext struct {
	reg     *Registry
	st      *staging
	scratch map[string]any
}

// Get 按主键读取「整批已应用、尚未提交」状态下的实例。
func (c *PostHookContext) Get(typeName, id string) (Instance, bool) {
	return c.st.get(c.reg, typeName, id)
}

// Scratch 返回整批共享的派生值暂存区。
func (c *PostHookContext) Scratch() map[string]any { return c.scratch }

// Registry 是对象类型注册表与全局实例存储。
// 全局互斥锁保证并发批次的最终效果等价于某个全局串行顺序。
type Registry struct {
	mu    sync.Mutex
	types map[string]*ObjectType
	data  map[string]map[string]Instance
}

// NewRegistry 创建空注册表。
func NewRegistry() *Registry {
	return &Registry{
		types: map[string]*ObjectType{},
		data:  map[string]map[string]Instance{},
	}
}

// RegisterObjectType 注册一个对象类型。
func (r *Registry) RegisterObjectType(t *ObjectType) {
	r.mu.Lock()
	defer r.mu.Unlock()
	cp := &ObjectType{
		Name:       t.Name,
		FieldTypes: map[string]string{},
	}
	for k, v := range t.FieldTypes {
		cp.FieldTypes[k] = v
	}
	cp.PreHooks = append(cp.PreHooks, t.PreHooks...)
	cp.PostHooks = append(cp.PostHooks, t.PostHooks...)
	r.types[cp.Name] = cp
	if r.data[cp.Name] == nil {
		r.data[cp.Name] = map[string]Instance{}
	}
}

// objectType 在调用方已持锁时返回类型定义。
func (r *Registry) objectType(name string) *ObjectType { return r.types[name] }

// validateRecord 做单条记录的参数合法性校验：未知类型、空主键、未知字段、字段类型不符。
// 主键重复属于跨记录的列表级校验，由 runner 统一处理。
func (r *Registry) validateRecord(rec Record, index int) error {
	t := r.types[rec.Type]
	if t == nil {
		return invalidParam(index, "unknown object type: "+rec.Type)
	}
	if rec.ID == "" {
		return invalidParam(index, "empty primary key for type "+rec.Type)
	}
	for name, val := range rec.Fields {
		want, ok := t.FieldTypes[name]
		if !ok {
			return invalidParam(index, "unknown field "+rec.Type+"."+name)
		}
		if val == nil {
			return invalidParam(index, "field "+rec.Type+"."+name+" must not be null")
		}
		if got := reflect.TypeOf(val).String(); got != want {
			return invalidParam(index, "field "+rec.Type+"."+name+
				" expects "+want+", got "+got)
		}
	}
	return nil
}

// snapshotType 在持锁状态下导出某类型的全量实例（供朴素模型/测试使用）。
func (r *Registry) snapshotType(typeName string) map[string]Instance {
	out := make(map[string]Instance, len(r.data[typeName]))
	for k, v := range r.data[typeName] {
		out[k] = v.clone()
	}
	return out
}

// SnapshotType 导出某类型的全量实例副本（线程安全）。
func (r *Registry) SnapshotType(typeName string) map[string]Instance {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.snapshotType(typeName)
}

// snapshotTypeLocked 在调用方已持锁时导出全量实例副本。
func (r *Registry) snapshotTypeLocked(typeName string) map[string]Instance {
	return r.snapshotType(typeName)
}

// clone 返回实例的深拷贝，避免调用方持有内部 map 的可变引用。
func (in Instance) clone() Instance {
	fields := make(map[string]any, len(in.Fields))
	for k, v := range in.Fields {
		fields[k] = v
	}
	return Instance{Type: in.Type, ID: in.ID, Fields: fields}
}

// Semantics 表示批量导入的整体语义。
type Semantics int

const (
	// AllOrNothing 全有或全无。
	AllOrNothing Semantics = iota + 1
	// BestEffort 尽力而为。
	BestEffort
)

// RecordResult 是单条记录的结果。
type RecordResult struct {
	Index int
	OK    bool
	// VisibilitySteps 是该记录前置钩子解析批内可见范围时实际命中覆盖层的次数，
	// 即实际访问到的已暂存记录数；用于可验证地证明开销与批次总长度无关。
	VisibilitySteps int
	Reason          string
}

// Report 是整批导入的聚合报告。
type Report struct {
	Semantics Semantics
	Committed bool
	Records   []RecordResult
	Err       error
}
