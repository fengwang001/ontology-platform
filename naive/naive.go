// Package naive 是一个独立实现的朴素对照模型（测试 oracle）：
// 它独立维护事务日志、逐步应用写入并在固定时点调用钩子，
// 用于与正式引擎在大量随机嵌套动作序列下逐条对照结果。
//
// 与正式引擎的实现策略刻意不同：
//   - 每次前置钩子触发前都对整个状态做全量深拷贝作为快照（O(n)），
//     而正式引擎的视图构造是 O(1)；
//   - 通过显式的事务日志（journal）逐步记录写入的逆操作，
//     回退时逐条反向回放，而正式引擎直接丢弃工作副本。
//
// 该模型不引用 tx / engine 包，也不共享 hooks 包的注册表与调度器，
// 只共享 spec（数据模型）、errs（错误类别）与 hooks 包的
// StateView / Context / Record 等接口类型。
package naive

import (
	"errors"
	"fmt"
	"sort"
	"strings"

	"ontology/errs"
	"ontology/hooks"
	"ontology/spec"
)

const maxDepth = 64

type objectState map[string]map[string]map[string]any

type hookEntry struct {
	name string
	fn   hooks.Func
}

// journalEntry 是事务日志中的一条记录：一次已应用写入的逆操作。
type journalEntry struct {
	typ        string
	id         string
	existed    bool
	beforeData map[string]any // 写入前的字段副本（不存在时为 nil）
}

// Model 是朴素对照模型：独立维护事务日志、逐步应用写入、
// 在固定时点（每次写入前 / 最外层事务提交前）调用钩子。
type Model struct {
	schemas   map[string]spec.Schema
	defs      map[string]spec.ActionDef
	pre       map[string][]hookEntry
	post      map[string][]hookEntry
	postOrder []struct {
		typ string
		hookEntry
	}
	state   objectState
	txSeq   uint64
	hookLog []hooks.Record

	// SnapshotCopiedEntries 统计为构造钩子快照而深拷贝的字段
	// 条目总数，用于与正式引擎的 O(1) 视图构造形成对照。
	SnapshotCopiedEntries uint64
}

// New 创建空模型。
func New() *Model {
	return &Model{
		schemas: make(map[string]spec.Schema),
		defs:    make(map[string]spec.ActionDef),
		pre:     make(map[string][]hookEntry),
		post:    make(map[string][]hookEntry),
		state:   make(objectState),
	}
}

// RegisterType 注册对象类型及字段模式。
func (m *Model) RegisterType(name string, schema spec.Schema) { m.schemas[name] = schema }

// RegisterAction 注册动作定义（与引擎相同的注册期校验）。
func (m *Model) RegisterAction(def spec.ActionDef) error {
	if def.Name == "" {
		return errors.New("naive: action name must not be empty")
	}
	for _, op := range def.Body {
		if op.Call != "" {
			if _, ok := m.defs[op.Call]; !ok && op.Call != def.Name {
				return fmt.Errorf("naive: action %q calls unknown action %q", def.Name, op.Call)
			}
		}
	}
	m.defs[def.Name] = def
	return nil
}

// RegisterPreHook 注册前置钩子。
func (m *Model) RegisterPreHook(objectType, name string, fn hooks.Func) {
	m.pre[objectType] = append(m.pre[objectType], hookEntry{name: name, fn: fn})
}

// RegisterPostHook 注册后置钩子（保留全局注册顺序）。
func (m *Model) RegisterPostHook(objectType, name string, fn hooks.Func) {
	he := hookEntry{name: name, fn: fn}
	m.post[objectType] = append(m.post[objectType], he)
	m.postOrder = append(m.postOrder, struct {
		typ string
		hookEntry
	}{typ: objectType, hookEntry: he})
}

// Execute 顺序执行一个最外层动作调用（朴素模型本身不并发）。
func (m *Model) Execute(action string) error {
	m.txSeq++
	txID := m.txSeq
	var journal []journalEntry
	var records []hooks.Record
	affected := make(map[string]bool)

	err := m.run(txID, action, nil, 0, &journal, &records, affected)
	if err == nil {
		err = m.firePost(txID, action, affected, &records)
	}
	if err != nil {
		// 回退：逐条反向回放事务日志。
		for i := len(journal) - 1; i >= 0; i-- {
			je := journal[i]
			if je.existed {
				m.state[je.typ][je.id] = je.beforeData
			} else {
				delete(m.state[je.typ], je.id)
			}
		}
		m.flush(&records, txID, "rolled-back")
		return err
	}
	m.flush(&records, txID, "committed")
	return nil
}

// run 递归解释动作体，逐步应用写入并记录事务日志。
func (m *Model) run(txID uint64, name string, parentPath []string, depth int,
	journal *[]journalEntry, records *[]hooks.Record, affected map[string]bool) error {
	if depth > maxDepth {
		return errors.New("naive: nesting depth limit exceeded")
	}
	def, ok := m.defs[name]
	if !ok {
		return fmt.Errorf("naive: unknown action %q", name)
	}
	path := append(append([]string(nil), parentPath...), name)
	for _, op := range def.Body {
		if op.Write != nil {
			w := *op.Write
			if err := m.validate(w, path); err != nil {
				return err
			}
			if err := m.firePre(txID, w, path, depth, records); err != nil {
				return err
			}
			m.apply(w, journal, affected)
			continue
		}
		if err := m.run(txID, op.Call, path, depth+1, journal, records, affected); err != nil {
			return err
		}
	}
	return nil
}

// validate 校验写入参数（独立实现的同一套规则）。
func (m *Model) validate(w spec.Write, path []string) error {
	fail := func(format string, args ...any) error {
		return &errs.Error{
			Kind:       errs.KindInvalidArgument,
			ActionPath: append([]string(nil), path...),
			ObjectType: w.Type,
			ObjectID:   w.ID,
			Message:    fmt.Sprintf(format, args...),
		}
	}
	schema, ok := m.schemas[w.Type]
	if !ok {
		return fail("unknown object type %q", w.Type)
	}
	_, exists := m.state[w.Type][w.ID]
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

// firePre 在写入生效前触发前置钩子。朴素做法：先对当前整个状态
// 做全量深拷贝作为「写入生效前状态」的快照，再逐个调用钩子。
func (m *Model) firePre(txID uint64, w spec.Write, path []string, depth int,
	records *[]hooks.Record) error {
	snapshot := m.snapshot()
	for _, he := range m.pre[w.Type] {
		hookErr := he.fn(hooks.Context{
			Kind:       hooks.Pre,
			ObjectType: w.Type,
			Write:      &w,
			State:      snapshot,
			TxID:       txID,
			Depth:      depth,
			ActionPath: path,
		})
		*records = append(*records, hooks.Record{
			Seq:        len(*records),
			TxID:       txID,
			Kind:       hooks.Pre,
			Hook:       he.name,
			ObjectType: w.Type,
			WriteID:    w.ID,
			Depth:      depth,
			ActionPath: strings.Join(path, " -> "),
			Err:        errText(hookErr),
		})
		if hookErr != nil {
			return &errs.Error{
				Kind:       errs.KindPreHook,
				ActionPath: append([]string(nil), path...),
				ObjectType: w.Type,
				ObjectID:   w.ID,
				HookName:   he.name,
				Message:    hookErr.Error(),
			}
		}
	}
	return nil
}

// firePost 在最外层事务提交前触发一次后置钩子，
// 按全局注册顺序收集全部失败并聚合报告。
func (m *Model) firePost(txID uint64, action string, affected map[string]bool,
	records *[]hooks.Record) error {
	snapshot := m.snapshot()
	var failures []errs.HookFailure
	for _, po := range m.postOrder {
		if !affected[po.typ] {
			continue
		}
		hookErr := po.fn(hooks.Context{
			Kind:       hooks.Post,
			ObjectType: po.typ,
			State:      snapshot,
			TxID:       txID,
			Depth:      0,
			ActionPath: []string{action},
		})
		*records = append(*records, hooks.Record{
			Seq:        len(*records),
			TxID:       txID,
			Kind:       hooks.Post,
			Hook:       po.name,
			ObjectType: po.typ,
			Depth:      0,
			ActionPath: action,
			Err:        errText(hookErr),
		})
		if hookErr != nil {
			failures = append(failures, errs.HookFailure{
				HookName:   po.name,
				ObjectType: po.typ,
				Message:    hookErr.Error(),
			})
		}
	}
	if len(failures) > 0 {
		return &errs.PostHookError{ActionPath: []string{action}, Failures: failures}
	}
	return nil
}

// apply 应用一次写入并把逆操作记入事务日志。
func (m *Model) apply(w spec.Write, journal *[]journalEntry, affected map[string]bool) {
	insts := m.state[w.Type]
	if insts == nil {
		insts = make(map[string]map[string]any)
		m.state[w.Type] = insts
	}
	before, existed := insts[w.ID]
	var beforeCopy map[string]any
	if existed {
		beforeCopy = make(map[string]any, len(before))
		for k, v := range before {
			beforeCopy[k] = v
		}
	}
	*journal = append(*journal, journalEntry{
		typ: w.Type, id: w.ID, existed: existed, beforeData: beforeCopy,
	})
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
	affected[w.Type] = true
}

// snapshot 对整个状态做全量深拷贝（朴素快照，开销随状态总量增长）。
func (m *Model) snapshot() hooks.StateView {
	cp := make(objectState, len(m.state))
	for typ, insts := range m.state {
		m2 := make(map[string]map[string]any, len(insts))
		for id, fields := range insts {
			f := make(map[string]any, len(fields))
			for k, v := range fields {
				f[k] = v
				m.SnapshotCopiedEntries++
			}
			m2[id] = f
		}
		cp[typ] = m2
	}
	return snapshotView{cp}
}

func (m *Model) flush(records *[]hooks.Record, txID uint64, outcome string) {
	for _, r := range *records {
		r.TxOutcome = outcome
		m.hookLog = append(m.hookLog, r)
	}
}

// HookLog 返回全局钩子触发记录。
func (m *Model) HookLog() []hooks.Record { return append([]hooks.Record(nil), m.hookLog...) }

// State 返回当前已提交状态的只读视图。
func (m *Model) State() hooks.StateView { return snapshotView{m.state} }

// snapshotView 是朴素模型的只读视图实现。
type snapshotView struct {
	data objectState
}

func (v snapshotView) Get(objectType, id string) (map[string]any, bool) {
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

func (v snapshotView) List(objectType string) []string {
	ids := make([]string, 0, len(v.data[objectType]))
	for id := range v.data[objectType] {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	return ids
}

func (v snapshotView) Count(objectType string) int { return len(v.data[objectType]) }

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

func errText(err error) string {
	if err == nil {
		return ""
	}
	return fmt.Sprint(err)
}
