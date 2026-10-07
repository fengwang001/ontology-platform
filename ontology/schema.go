package ontology

import (
	"fmt"
	"sort"
	"sync"
)

// TypeDef 描述某个规则版本下一个对象类型的定义。
type TypeDef struct {
	ID string
	// Parent 为父类型 ID，根类型为空串。
	Parent string
	// LocalProps 是本类型新引入的属性（属性名 -> 取值范围）。
	LocalProps map[string]ValueRange
	// Overrides 是对继承属性的取值范围覆盖（属性名 -> 新取值范围）。
	Overrides map[string]ValueRange
}

// RuleVersion 是一份不可变的继承与覆盖规则版本。
// EffectiveFrom 为该版本生效的起始逻辑时刻（含该时刻，
// 即切换时点归属新版本一侧）。
type RuleVersion struct {
	ID            int
	EffectiveFrom int64
	Types         map[string]TypeDef
}

// SchemaRegistry 保存按时间追加的规则版本，只允许追加
// EffectiveFrom 严格递增的版本，已追加版本不可修改。
type SchemaRegistry struct {
	mu       sync.RWMutex
	versions []RuleVersion
}

// NewSchemaRegistry 以初始版本创建注册表。
func NewSchemaRegistry(initial RuleVersion) *SchemaRegistry {
	initial.ID = 0
	return &SchemaRegistry{versions: []RuleVersion{initial}}
}

// AppendVersion 追加新版本。EffectiveFrom 必须大于所有已有版本。
func (r *SchemaRegistry) AppendVersion(v RuleVersion) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	last := r.versions[len(r.versions)-1]
	if v.EffectiveFrom <= last.EffectiveFrom {
		return fmt.Errorf("ontology: rule version EffectiveFrom %d not after %d", v.EffectiveFrom, last.EffectiveFrom)
	}
	v.ID = len(r.versions)
	r.versions = append(r.versions, v)
	return nil
}

// VersionCount 返回当前版本数（用于检查点失效判断）。
func (r *SchemaRegistry) VersionCount() int {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return len(r.versions)
}

// VersionAt 返回逻辑时刻 t 生效的规则版本。
// 归属规则：最后一个满足 EffectiveFrom <= t 的版本；
// 该归属只取决于 t 与已追加版本，后续追加不改变 t 之前时刻的归属。
// 若 t 早于所有版本（仅可能出现在首个版本生效前的退化情形），
// 钳制到最早的版本，保证函数对所有 t 有定义。
func (r *SchemaRegistry) VersionAt(t int64) RuleVersion {
	r.mu.RLock()
	defer r.mu.RUnlock()
	i := sort.Search(len(r.versions), func(i int) bool {
		return r.versions[i].EffectiveFrom > t
	})
	if i == 0 {
		return r.versions[0]
	}
	return r.versions[i-1]
}

// Resolve 在版本 v 下解析类型 typeID 的属性 prop 的有效取值范围，
// 沿继承链自底向上查找，子类型的覆盖与新引入属性优先。
func Resolve(v RuleVersion, typeID, prop string) (ValueRange, bool) {
	for id := typeID; id != ""; {
		def, ok := v.Types[id]
		if !ok {
			return ValueRange{}, false
		}
		if rng, ok := def.Overrides[prop]; ok {
			return rng, true
		}
		if rng, ok := def.LocalProps[prop]; ok {
			return rng, true
		}
		id = def.Parent
	}
	return ValueRange{}, false
}

// IsDirectChild 报告 child 在版本 v 下是否为 parent 的直接子类型。
func IsDirectChild(v RuleVersion, parent, child string) bool {
	def, ok := v.Types[child]
	return ok && def.Parent == parent
}
