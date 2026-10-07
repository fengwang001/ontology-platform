package ontology_test

import (
	"sort"

	ontology "ontology/ontology"
)

// naiveModel 是独立实现的朴素继承规则快照模型：
// 每次导出时把完整规则状态深拷贝进导出快照，审计时从自己的快照重新推导。
// 它与被测系统（不可变版本存储）实现路径完全不同，用于差分对照。
type naiveModel struct {
	props   map[string]map[string]bool // 类型 -> 直接属性
	parent  map[string]string          // 类型 -> 父类型
	rules   map[[3]string]naiveRule    // (类型, 主体, 属性) -> 生效规则
	exports map[string]naiveExportSnap // 导出 ID -> 快照
}

type naiveRule struct {
	id       string
	effect   ontology.Effect
	content  string
	declared string
}

type naiveExportSnap struct {
	exclusions map[string]naiveRule // 被排除属性 -> 命中规则的完整快照
}

func newNaiveModel() *naiveModel {
	return &naiveModel{
		props:   map[string]map[string]bool{},
		parent:  map[string]string{},
		rules:   map[[3]string]naiveRule{},
		exports: map[string]naiveExportSnap{},
	}
}

func (m *naiveModel) addType(name, parent string, properties []string) {
	set := map[string]bool{}
	for _, p := range properties {
		set[p] = true
	}
	m.props[name] = set
	m.parent[name] = parent
}

func (m *naiveModel) setParent(name, parent string) { m.parent[name] = parent }

func (m *naiveModel) upsert(typeName, subject, property string, effect ontology.Effect, content, id string) {
	m.rules[[3]string{typeName, subject, property}] = naiveRule{
		id: id, effect: effect, content: content, declared: typeName,
	}
}

func (m *naiveModel) deleteRule(typeName, subject, property string) {
	delete(m.rules, [3]string{typeName, subject, property})
}

func (m *naiveModel) chain(typeName string) []string {
	var out []string
	seen := map[string]bool{}
	for cur := typeName; cur != ""; {
		if seen[cur] {
			break
		}
		seen[cur] = true
		out = append(out, cur)
		cur = m.parent[cur]
	}
	return out
}

// export 在当前状态上计算排除集，并把命中规则完整快照进导出结果。
func (m *naiveModel) export(typeName, subject string) naiveExportSnap {
	set := map[string]bool{}
	for _, tn := range m.chain(typeName) {
		for p := range m.props[tn] {
			set[p] = true
		}
	}
	props := make([]string, 0, len(set))
	for p := range set {
		props = append(props, p)
	}
	sort.Strings(props)
	snap := naiveExportSnap{exclusions: map[string]naiveRule{}}
	for _, prop := range props {
		for _, tn := range m.chain(typeName) {
			if r, ok := m.rules[[3]string{tn, subject, prop}]; ok {
				if r.effect == ontology.EffectDeny {
					snap.exclusions[prop] = r // 深拷贝快照
				}
				break
			}
		}
	}
	return snap
}

// recordExport 计算并保存导出快照。
func (m *naiveModel) recordExport(exportID, typeName, subject string) {
	m.exports[exportID] = m.export(typeName, subject)
}

// audit 从导出时的快照回答历史依据，与当前规则状态完全无关。
func (m *naiveModel) audit(exportID, property string) (naiveRule, bool) {
	snap, ok := m.exports[exportID]
	if !ok {
		return naiveRule{}, false
	}
	r, excluded := snap.exclusions[property]
	return r, excluded
}
