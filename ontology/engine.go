package ontology

import (
	"fmt"
	"sync"
)

// OpKind 标识全序日志中的操作种类。
type OpKind int

const (
	OpCommit OpKind = iota // 一次字段变更打包提交
	OpWrite                // 一次实例写入
)

// LogEntry 是全序日志中的一条记录。日志顺序即全部提交与写入的全序。
type LogEntry struct {
	Seq      int64
	Op       OpKind
	Batch    []FieldChange // OpCommit 时的变更集
	Write    *Instance     // OpWrite 时的写入内容
	Accepted bool
	// Categories 仅对 OpCommit 有意义：被拒绝时命中的全部不兼容类别。
	Categories IncompatCategory
}

// BatchResult 是一次打包提交的结果。
type BatchResult struct {
	Accepted bool
	// Decisions 逐项判定的完整记录，与提交的变更一一对应。
	Decisions []Decision
	// Categories 全部变更命中的不兼容类别汇总（位掩码）。
	Categories IncompatCategory
}

// Engine 是字段定义演进与实例写入的串行化执行器。
// 所有提交与写入在同一把互斥锁下进入全序日志，
// 因此外部观察到的结果必然等价于某个全序串行执行。
type Engine struct {
	mu        sync.Mutex
	store     *InstanceStore
	types     map[string]*ObjectType
	refs      *RefRegistry
	checker   *Checker
	seq       int64
	log       []LogEntry
	decisions []Decision
}

func NewEngine() *Engine {
	store := NewInstanceStore()
	refs := NewRefRegistry()
	return &Engine{
		store:   store,
		types:   make(map[string]*ObjectType),
		refs:    refs,
		checker: &Checker{Store: store, Refs: refs},
	}
}

// RegisterObjectType 注册一个对象类型。
func (e *Engine) RegisterObjectType(ot *ObjectType) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.types[ot.Name] = ot
}

// RegisterRef 登记一个外部引用方，Captured 快照取登记时的字段语义。
func (e *Engine) RegisterRef(ref FieldReference) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if ot, ok := e.types[ref.ObjectType]; ok {
		ref.Captured = SignatureOf(ot.field(ref.Field))
	}
	e.refs.Register(ref)
}

// Refs 暴露引用注册表（只读用途）。
func (e *Engine) Refs() *RefRegistry { return e.refs }

// Store 暴露存活实例存储（只读用途）。
func (e *Engine) Store() *InstanceStore { return e.store }

// Commit 原子地提交一组字段变更：逐项独立判定后汇总，
// 任意一项不兼容则整组拒绝，状态零变化；全部兼容则整组生效。
func (e *Engine) Commit(batch []FieldChange) BatchResult {
	e.mu.Lock()
	defer e.mu.Unlock()
	res := e.checkBatch(batch)
	e.seq++
	entry := LogEntry{Seq: e.seq, Op: OpCommit, Batch: batch, Accepted: res.Accepted, Categories: res.Categories}
	if res.Accepted {
		for _, ch := range batch {
			e.applyChange(ch)
		}
	}
	for i := range res.Decisions {
		res.Decisions[i].Seq = e.seq
	}
	e.decisions = append(e.decisions, res.Decisions...)
	e.log = append(e.log, entry)
	return res
}

// checkBatch 逐项独立判定并汇总类别，不修改任何状态。
// 每项变更先校验其 Old 与当前字段定义一致（CAS 语义），
// 基于过期定义的变更会被拒绝，避免并发提交间静默互相覆盖。
func (e *Engine) checkBatch(batch []FieldChange) BatchResult {
	res := BatchResult{Accepted: true}
	for _, ch := range batch {
		if d, stale := e.checkStaleBase(ch); stale {
			res.Accepted = false
			res.Decisions = append(res.Decisions, d)
			continue
		}
		d, err := e.checker.Check(ch)
		if err != nil {
			d = Decision{
				Change:     ch,
				Compatible: false,
				Reason:     fmt.Sprintf("无效变更：%v", err),
			}
		}
		if !d.Compatible {
			res.Accepted = false
			res.Categories |= d.Categories
		}
		res.Decisions = append(res.Decisions, d)
	}
	return res
}

// checkStaleBase 校验变更的 Old 与当前字段定义一致。调用方必须持有锁。
func (e *Engine) checkStaleBase(ch FieldChange) (Decision, bool) {
	ot := e.types[ch.ObjectType]
	var cur *FieldDef
	if ot != nil {
		cur = ot.field(ch.Field)
	}
	if signatureEqual(SignatureOf(cur), SignatureOf(ch.Old)) {
		return Decision{}, false
	}
	return Decision{
		Change:     ch,
		Compatible: false,
		Reason:     "变更基于过期的字段定义（与当前版本不一致），拒绝以避免并发提交互相覆盖",
	}, true
}

// applyChange 应用单项已判定兼容的变更。调用方必须持有锁。
func (e *Engine) applyChange(ch FieldChange) {
	ot := e.types[ch.ObjectType]
	kind, err := Classify(ch)
	if err == ErrNoChange {
		// 定义微调：直接以新定义替换（默认值/可空标志可能变化）。
		if ch.New != nil {
			def := *ch.New
			ot.Fields[ch.Field] = &def
		}
		e.alignRefs(ch)
		return
	}
	if err != nil {
		return
	}
	switch kind {
	case AddFieldWithDefault, AddFieldWithoutDefault:
		def := *ch.New
		ot.Fields[ch.Field] = &def
		if ch.Backfill != nil {
			e.store.EachLive(ch.ObjectType, func(in *Instance) bool {
				if v, ok := ch.Backfill(in.ID); ok {
					in.Values[ch.Field] = v
				}
				return true
			})
		}
	case TightenConstraint, LoosenConstraint:
		def := *ch.New
		ot.Fields[ch.Field] = &def
	case ChangeType:
		def := *ch.New
		ot.Fields[ch.Field] = &def
		e.store.EachLive(ch.ObjectType, func(in *Instance) bool {
			if v, ok := in.Values[ch.Field]; ok {
				if nv, ok := ch.Old.Type.ReinterpretTo(ch.New.Type, v); ok {
					in.Values[ch.Field] = nv
				}
			}
			return true
		})
	case RemoveField:
		delete(ot.Fields, ch.Field)
		e.store.EachLive(ch.ObjectType, func(in *Instance) bool {
			delete(in.Values, ch.Field)
			return true
		})
	}
	e.alignRefs(ch)
}

// alignRefs 在变更生效后把引用方快照对齐到新版本语义。调用方必须持有锁。
func (e *Engine) alignRefs(ch FieldChange) {
	newSig := SignatureOf(ch.New)
	for _, ref := range e.refs.OnField(ch.ObjectType, ch.Field) {
		ref.Captured = newSig
		e.refs.Register(ref)
	}
}

// Write 以当前时刻的字段定义校验并写入一个实例。
// 校验与写入在同一临界区内完成，因此一次写入必然对应
// 全序中某个确定时刻的字段定义。
func (e *Engine) Write(in Instance) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	err := e.validateWrite(in)
	e.seq++
	accepted := err == nil
	if accepted {
		e.store.Put(in)
	}
	e.log = append(e.log, LogEntry{Seq: e.seq, Op: OpWrite, Write: &in, Accepted: accepted})
	return err
}

// validateWrite 用当前字段定义校验写入。调用方必须持有锁。
func (e *Engine) validateWrite(in Instance) error {
	ot, ok := e.types[in.Type]
	if !ok {
		return fmt.Errorf("ontology: unknown object type %q", in.Type)
	}
	for name := range in.Values {
		if ot.field(name) == nil {
			return fmt.Errorf("ontology: unknown field %q on %q", name, in.Type)
		}
	}
	for name, def := range ot.Fields {
		v, present := in.Values[name]
		if !present {
			if !def.Nullable && !def.HasDefault {
				return fmt.Errorf("ontology: field %q on %q is required", name, in.Type)
			}
			continue
		}
		if v.Kind != def.Type.Kind() {
			return fmt.Errorf("ontology: field %q expects %s, got %s", name, def.Type.Kind(), v.Kind)
		}
		if !def.Constraint.SatisfiedBy(v) {
			return fmt.Errorf("ontology: field %q value violates constraint", name)
		}
	}
	return nil
}

// Log 返回全序日志的拷贝。
func (e *Engine) Log() []LogEntry {
	e.mu.Lock()
	defer e.mu.Unlock()
	out := make([]LogEntry, len(e.log))
	copy(out, e.log)
	return out
}

// Decisions 返回全部判定记录的拷贝。
func (e *Engine) Decisions() []Decision {
	e.mu.Lock()
	defer e.mu.Unlock()
	out := make([]Decision, len(e.decisions))
	copy(out, e.decisions)
	return out
}

// ObjectTypeDef 返回对象类型当前字段定义的深拷贝快照。
func (e *Engine) ObjectTypeDef(name string) map[string]FieldDef {
	e.mu.Lock()
	defer e.mu.Unlock()
	return snapshotFields(e.types[name])
}

// LiveInstances 返回某对象类型全部存活实例的拷贝。
func (e *Engine) LiveInstances(objectType string) []Instance {
	e.mu.Lock()
	defer e.mu.Unlock()
	var out []Instance
	e.store.EachLive(objectType, func(in *Instance) bool {
		out = append(out, in.Clone())
		return true
	})
	return out
}

func snapshotFields(ot *ObjectType) map[string]FieldDef {
	out := make(map[string]FieldDef)
	if ot == nil {
		return out
	}
	for name, def := range ot.Fields {
		out[name] = *def
	}
	return out
}
