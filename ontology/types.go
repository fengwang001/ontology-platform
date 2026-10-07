package ontology

// TzDefVersion 是某对象类型“默认时区定义”的一个版本。
// 版本一旦创建即不可变，迁移只能追加更高版本。
type TzDefVersion struct {
	Version      int
	ZoneID       string
	EffectiveSeq uint64 // 生效的逻辑序号：写入序号 >= 该值的写操作锚定到本版本
}

// ObjectType 是对象类型及其全部版本化定义。
type ObjectType struct {
	ID           string
	GroupingProp string // 视图分组依据的时间类属性名
	Deleted      bool
	TzVersions   []TzDefVersion // 按 Version 与 EffectiveSeq 严格递增
	// DeprecatedGroupingProp 为 true 表示该类型当前版本已废弃分组属性（单向，不可恢复）。
	DeprecatedGroupingProp bool
	TypeVersion            int
}

// tzVersionAt 返回在逻辑序号 seq 时刻生效的默认时区定义版本；
// 若该时刻尚未定义任何默认时区，返回 ok=false。
func (t *ObjectType) tzVersionAt(seq uint64) (TzDefVersion, bool) {
	best := TzDefVersion{}
	found := false
	for _, v := range t.TzVersions {
		if v.EffectiveSeq <= seq && (!found || v.Version > best.Version) {
			best = v
			found = true
		}
	}
	return best, found
}

// LinkDef 描述参与聚合的链接关系。
type LinkDef struct {
	ID        string
	LeftType  string
	RightType string
}
