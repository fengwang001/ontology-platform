// Package naive 是冲突判定的独立朴素参考实现，仅用于测试对照。
//
// 与 ontology.Store 的优化实现不同，本模型：
//   - 保留全部生效写入的完整历史；
//   - 每次判定都从头扫描整个历史（O(历史版本数)）；
//   - 每次取当前值都重新折叠整个历史。
//
// 它与 Store 不共享任何判定代码（仅复用合并规则这一“规格”本身），
// 因此可作为随机并发序列对照测试中的独立预言机（oracle）。
package naive

import (
	"sort"

	"ontology/ontology"
)

// committed 记录一次生效提交：版本号与实际生效的属性变更（合并后值）。
type committed struct {
	version uint64
	changes map[string]ontology.Value
}

// Model 是单实例的朴素判定模型。
type Model struct {
	typ       *ontology.ObjectType
	retention uint64
	initial   map[string]ontology.Value
	version   uint64
	history   []committed
	scans     int64
}

// New 创建朴素模型。retention 语义与 ontology.WithRetentionWindow 相同。
func New(typ *ontology.ObjectType, initial map[string]ontology.Value, retention uint64) *Model {
	vals := make(map[string]ontology.Value, len(initial))
	for k, v := range initial {
		vals[k] = v
	}
	return &Model{typ: typ, retention: retention, initial: vals}
}

// currentValue 通过扫描整个历史折叠出属性当前值（朴素实现）。
func (m *Model) currentValue(name string) ontology.Value {
	v := m.initial[name]
	for _, c := range m.history {
		m.scans++
		if nv, ok := c.changes[name]; ok {
			v = nv
		}
	}
	return v
}

// changedAfterBase 扫描整个历史，判断属性在 base 版本之后是否被实际变更过。
func (m *Model) changedAfterBase(name string, base uint64) bool {
	changed := false
	for _, c := range m.history {
		m.scans++
		if c.version <= base {
			continue
		}
		if _, ok := c.changes[name]; ok {
			changed = true
		}
	}
	return changed
}

// Apply 按朴素方式判定一次写入，返回与 ontology.Store.Apply 相同结构的结果。
func (m *Model) Apply(req ontology.WriteRequest) ontology.WriteResult {
	// 第一步：基线判定。
	minBase := uint64(0)
	if m.retention > 0 && m.version > m.retention {
		minBase = m.version - m.retention
	}
	if req.BaseVersion > m.version || req.BaseVersion < minBase {
		return ontology.WriteResult{Outcome: ontology.OutcomeRejectedStaleBaseline, Version: m.version}
	}

	props := make([]string, 0, len(req.Changes))
	for name := range req.Changes {
		props = append(props, name)
	}
	sort.Strings(props)

	// 第二步：不可合并属性冲突判定（逐一扫描完整历史）。
	for _, name := range props {
		spec, _ := m.typ.Property(name)
		if spec.Mergeable {
			continue
		}
		if m.changedAfterBase(name, req.BaseVersion) &&
			!ontology.Equal(m.currentValue(name), req.Changes[name]) {
			return ontology.WriteResult{
				Outcome:          ontology.OutcomeRejectedConflict,
				Version:          m.version,
				ConflictProperty: name,
			}
		}
	}

	// 第三步：应用。可合并属性按规则与当前值合并，不可合并属性直接覆盖。
	pending := make(map[string]ontology.Value)
	for _, name := range props {
		spec, _ := m.typ.Property(name)
		current := m.currentValue(name)
		declared := req.Changes[name]
		if spec.Mergeable {
			merged := ontology.RuleByName(spec.MergeRule).Join(current, declared)
			if !ontology.Equal(merged, current) {
				pending[name] = merged
			}
		} else if !ontology.Equal(declared, current) {
			pending[name] = declared
		}
	}
	if len(pending) > 0 {
		m.version++
		m.history = append(m.history, committed{version: m.version, changes: pending})
	}
	return ontology.WriteResult{Outcome: ontology.OutcomeCommitted, Version: m.version}
}

// Version 返回当前版本（生效提交数）。
func (m *Model) Version() uint64 {
	return m.version
}

// Value 返回属性当前值（重新折叠整个历史）。
func (m *Model) Value(name string) ontology.Value {
	return m.currentValue(name)
}

// HistoryScans 返回历史扫描次数。该值随历史版本总数增长，
// 与优化实现的 O(1) 判定形成对照。
func (m *Model) HistoryScans() int64 {
	return m.scans
}
