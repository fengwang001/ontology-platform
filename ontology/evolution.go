package ontology

import (
	"fmt"
	"sort"
	"sync"
)

// ObjectType 持有某对象类型当前已接受的字段定义，并串行化
// 字段定义提交与实例写入，使其等价于某个全序下的串行执行。
type ObjectType struct {
	name    string
	mu      sync.Mutex
	fields  map[string]FieldDef
	version int64
	store   *InstanceStore
	refs    *ReferenceRegistry
	auditor AuditSink
}

func NewObjectType(name string, store *InstanceStore, refs *ReferenceRegistry, auditor AuditSink) *ObjectType {
	return &ObjectType{
		name:    name,
		fields:  make(map[string]FieldDef),
		store:   store,
		refs:    refs,
		auditor: auditor,
	}
}

// Submit 原子地校验并应用一次打包提交：任一项不兼容则整体拒绝，
// 字段定义与实例数据均保持提交前状态（无部分生效的中间状态）。
//
// 串行化：t.mu 是“提交”与“写入”两类操作共享的唯一临界区，因此
// Submit 与 WriteInstance 的任意并发交织都等价于它们按某个全序逐个
// 执行；每次 WriteInstance 看到的字段定义必为该全序上某一个已提交
// 版本，绝不会是两次提交之间的混合状态。
func (t *ObjectType) Submit(changes []FieldChange) (BatchReport, error) {
	return t.SubmitCAS(-1, changes)
}

// SubmitCAS 是 Submit 的乐观并发形式：仅当当前字段定义版本恰好等于
// expectedVersion 时才尝试提交，否则返回 ErrVersionConflict 且不产生
// 任何状态变化。expectedVersion<0 表示不校验版本。调用方在冲突时重读
// 定义并重试，可以杜绝“持过期 Old 快照把字段改回旧定义”的回退。
func (t *ObjectType) SubmitCAS(expectedVersion int64, changes []FieldChange) (BatchReport, error) {
	t.mu.Lock()
	defer t.mu.Unlock()

	if expectedVersion >= 0 && t.version != expectedVersion {
		return BatchReport{ObjectType: t.name, Compatible: false}, ErrVersionConflict
	}

	// 先把变更绑定到当前已提交的字段定义，保证分类与判定基于确定快照。
	bound := make([]FieldChange, len(changes))
	for i, fc := range changes {
		bound[i] = t.bind(fc)
	}

	// CAS 前置条件的第二部分：调用方显式给出的 Old 必须与当前已提交
	// 定义精确一致。仅核对版本号不足以防止“版本号相同但 Old 快照错误”
	// 的提交把一次收紧偷偷变成放宽（例如并发链中乱序到达的旧上界）。
	if expectedVersion >= 0 {
		for _, fc := range bound {
			cur, exists := t.fields[fc.Name]
			switch {
			case fc.Old == nil && exists:
				return BatchReport{ObjectType: t.name, Compatible: false}, ErrVersionConflict
			case fc.Old != nil && (!exists || !sameDef(fc.Old, &cur)):
				return BatchReport{ObjectType: t.name, Compatible: false}, ErrVersionConflict
			}
		}
	}

	checker := NewChecker(t.name, t.store, t.refs, t.auditor)
	report := checker.CheckBatch(bound)
	if !report.Compatible {
		// 未触碰 fields，也未写任何实例数据：零状态变化。
		return report, &RejectError{Report: report}
	}

	// 全部通过后才一次性应用字段定义（同一个临界区内，外部观察不到中间态）。
	for _, fc := range bound {
		switch {
		case fc.New == nil:
			delete(t.fields, fc.Name)
		default:
			d := *fc.New
			d.Name = fc.Name
			t.fields[fc.Name] = d
		}
	}
	t.version++

	// 唯一需要物化到实例数据的情形：新增必填且无默认值、但携带了
	// 覆盖全部存活实例的回填规则。回填与定义应用在同一临界区完成。
	for _, fc := range bound {
		if Classify(fc) != CatAddWithoutDefault || fc.Backfill == nil ||
			fc.New.HasDefault || fc.New.AllowMissing {
			continue
		}
		t.store.Scan(func(inst Instance) bool {
			if _, has := inst.Fields[fc.Name]; has {
				return true
			}
			val, ok := fc.Backfill.Fill(inst.ID, inst.Fields)
			if !ok || !val.Present() {
				return true // 判定阶段已保证不会发生
			}
			inst.Fields[fc.Name] = val
			t.store.Put(inst.ID, inst.Fields)
			return true
		})
	}
	return report, nil
}

// bind 用当前已提交版本的字段定义补全变更中缺省的 Old 指针，
// 并拒绝针对不存在字段的修改/删除（整体拒绝，归入该批判定）。
func (t *ObjectType) bind(fc FieldChange) FieldChange {
	if fc.Old == nil && fc.New != nil {
		if cur, ok := t.fields[fc.Name]; ok {
			fc.Old = &cur
		}
	}
	if fc.New == nil && fc.Old == nil {
		if cur, ok := t.fields[fc.Name]; ok {
			fc.Old = &cur
		}
	}
	return fc
}

// WriteInstance 按当前已提交版本的字段定义校验并写入一个实例，
// 返回该写入所对应的字段定义版本号。
func (t *ObjectType) WriteInstance(id InstanceID, fields map[string]Value) (int64, error) {
	t.mu.Lock()
	defer t.mu.Unlock()

	names := make([]string, 0, len(t.fields))
	for name := range t.fields {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		def := t.fields[name]
		v, present := fields[name]
		if !present || !v.Present() {
			if def.HasDefault {
				continue
			}
			if def.AllowMissing {
				continue
			}
			return t.version, fmt.Errorf("ontology: instance %s missing required field %q", id, name)
		}
		if !def.Type.Validate(v) {
			return t.version, fmt.Errorf("ontology: instance %s field %q fails type %s validation", id, name, def.Type.Name())
		}
		if def.Constraint != nil && !def.Constraint.Satisfies(v) {
			return t.version, fmt.Errorf("ontology: instance %s field %q violates constraint", id, name)
		}
	}
	t.store.Put(id, fields)
	return t.version, nil
}

func (t *ObjectType) Field(name string) (FieldDef, bool) {
	t.mu.Lock()
	defer t.mu.Unlock()
	d, ok := t.fields[name]
	// 返回副本，调用方在锁外读取/取址都不会与后续提交产生数据竞争。
	return d, ok
}

func (t *ObjectType) Version() int64 {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.version
}

type RejectError struct{ Report BatchReport }

func (e *RejectError) Error() string {
	return fmt.Sprintf("ontology: schema evolution rejected for %d reason(s): %v",
		e.Report.Reasons, e.Report.Reasons.Strings())
}

// ErrVersionConflict 表示 SubmitCAS 时字段定义版本已被其他提交推进。
var ErrVersionConflict = fmt.Errorf("ontology: schema version conflict; retry with fresh definitions")
