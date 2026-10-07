package idx

import "sort"

// PropStatus 是属性在其对象类型定义中的生命周期状态。
type PropStatus int

const (
	// PropActive 属性当前有效，可作为索引依据。
	PropActive PropStatus = iota
	// PropDeprecated 属性已被版本迁移废弃；若 ReplacedBy 为空表示
	// 未指定替代字段。
	PropDeprecated
)

// PropDef 是单个属性在某个类型版本中的定义。
type PropDef struct {
	Status     PropStatus
	ReplacedBy string // 仅当 Status == PropDeprecated 时有意义
}

// TypeVersion 是自某个源版本号起生效的一组属性定义快照。
type TypeVersion struct {
	FromVersion uint64
	Props       map[string]PropDef
}

// TypeRegistry 保存对象类型属性定义的版本迁移历史，
// 支持按事件生效版本查询"当时"的属性定义。
type TypeRegistry struct {
	versions []TypeVersion // 按 FromVersion 升序
}

// NewTypeRegistry 以初始属性定义创建注册表（自版本 0 起生效）。
func NewTypeRegistry(initial map[string]PropDef) *TypeRegistry {
	return &TypeRegistry{versions: []TypeVersion{{FromVersion: 0, Props: initial}}}
}

// Migrate 登记一次类型定义迁移，新定义自 fromVersion 起生效。
func (r *TypeRegistry) Migrate(fromVersion uint64, props map[string]PropDef) {
	r.versions = append(r.versions, TypeVersion{FromVersion: fromVersion, Props: props})
	sort.Slice(r.versions, func(i, j int) bool {
		return r.versions[i].FromVersion < r.versions[j].FromVersion
	})
}

// defAt 返回属性在指定版本时刻的定义；第二个返回值表示当时是否已定义。
func (r *TypeRegistry) defAt(prop string, version uint64) (PropDef, bool) {
	tv := r.versionAt(version)
	def, ok := tv.Props[prop]
	return def, ok
}

// latest 返回属性在最新类型版本中的定义。
func (r *TypeRegistry) latest(prop string) (PropDef, bool) {
	def, ok := r.versions[len(r.versions)-1].Props[prop]
	return def, ok
}

func (r *TypeRegistry) versionAt(version uint64) TypeVersion {
	idx := 0
	for i, tv := range r.versions {
		if tv.FromVersion <= version {
			idx = i
		} else {
			break
		}
	}
	return r.versions[idx]
}
