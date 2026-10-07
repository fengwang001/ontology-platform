package compat

// ChangeKind 是属性级结构变更的类别。
type ChangeKind int

const (
	// ChangeAddProperty 新增属性（Type 为新属性的类型，Required 为其必填性）。
	ChangeAddProperty ChangeKind = iota
	// ChangeRemoveProperty 删除属性。
	ChangeRemoveProperty
	// ChangeRetypeProperty 修改属性取值类型（OldType -> Type）。
	ChangeRetypeProperty
	// ChangeRequireProperty 必填性由非必填改为必填。
	ChangeRequireProperty
	// ChangeUnrequireProperty 必填性由必填改为非必填。
	ChangeUnrequireProperty
)

// Change 是一条属性级结构变更记录，相对于前一格式版本。
type Change struct {
	ObjectType string
	Property   string
	Kind       ChangeKind
	Type       TypeSpec // Add/Retype 的新类型
	Required   bool     // Add 的必填性
	OldType    TypeSpec // Retype 的旧类型（用于判定依据）
}

// Format 是一个格式版本：完整物化的 Schema 加上相对前驱版本的变更日志。
//
// Changes 是兼容性判定开销只与“发生变化的属性”相关的关键：
// 判定过程只消费变更记录，不扫描全量属性。
type Format struct {
	Version Version
	Schema  Schema
	Changes []Change // 相对前驱版本；创世版本为空
}

// Snapshot 是一份待校验的快照：格式版本号 + 实际对象数据。
//
// Data 的组织为 对象类型名 -> 对象列表 -> 属性名 -> 取值。
type Snapshot struct {
	Version Version
	Data    map[string][]map[string]any
}

// Mode 是消费方场景：读取或写入。
type Mode int

const (
	ModeRead Mode = iota
	ModeWrite
)

// Profile 描述一个消费方的兼容性判定上下文。
type Profile struct {
	Name string
	// At 是消费方编写时所依据的格式版本。
	At Version
	// Accepts 是消费方声明可识别的格式版本闭区间。
	Accepts Range
	// Mode 是读取或写入场景。
	Mode Mode
	// Independent 声明消费方不依赖的属性：对象类型名 -> 属性名集合。
	// 被删除的属性若在此声明中，则不因删除而判定不兼容。
	Independent map[string]map[string]bool
}

// dependsOn 报告消费方是否依赖某属性。
func (p Profile) dependsOn(objectType, property string) bool {
	if p.Independent == nil {
		return true
	}
	return !p.Independent[objectType][property]
}

// netProp 是一个属性在“消费方侧（old）”与“数据侧（new）”之间的净变化。
type netProp struct {
	oldPresent, newPresent bool
	oldProp, newProp       Property
}

// invert 返回变更的逆向形式，用于沿演化链向历史版本方向行走。
// Remove 记录被删属性的类型与必填性，因此 Add/Remove 可互逆。
func invert(c Change) Change {
	switch c.Kind {
	case ChangeAddProperty:
		return Change{ObjectType: c.ObjectType, Property: c.Property, Kind: ChangeRemoveProperty, Type: c.Type, Required: c.Required}
	case ChangeRemoveProperty:
		return Change{ObjectType: c.ObjectType, Property: c.Property, Kind: ChangeAddProperty, Type: c.Type, Required: c.Required}
	case ChangeRetypeProperty:
		return Change{ObjectType: c.ObjectType, Property: c.Property, Kind: ChangeRetypeProperty, Type: c.OldType, OldType: c.Type}
	case ChangeRequireProperty:
		return Change{ObjectType: c.ObjectType, Property: c.Property, Kind: ChangeUnrequireProperty}
	case ChangeUnrequireProperty:
		return Change{ObjectType: c.ObjectType, Property: c.Property, Kind: ChangeRequireProperty}
	}
	return c
}

// foldNetChanges 沿 chain 从 fromIdx 版本行走到 toIdx 版本（可正向或反向），
// 把途经的变更记录折叠为每个属性的净变化。old 侧为消费方编写版本（fromIdx），
// new 侧为数据版本（toIdx）。
//
// 开销只与变更记录条数相关：只对被变更触及的属性做 map 查找，
// 从不扫描全量 Schema。
func foldNetChanges(chain []Format, fromIdx, toIdx int) map[string]map[string]netProp {
	net := map[string]map[string]netProp{}
	if fromIdx <= toIdx {
		for i := fromIdx + 1; i <= toIdx; i++ {
			applyChanges(net, chain[fromIdx].Schema, chain[i].Changes)
		}
	} else {
		for i := fromIdx; i > toIdx; i-- {
			applyChanges(net, chain[fromIdx].Schema, invertAll(chain[i].Changes))
		}
	}
	return net
}

func invertAll(changes []Change) []Change {
	out := make([]Change, len(changes))
	// 逆向行走时不仅要逐条取逆，还要反转顺序：
	// 正向 [A, B] 的逆过程是 [B⁻¹, A⁻¹]。
	for i, c := range changes {
		out[len(changes)-1-i] = invert(c)
	}
	return out
}

// applyChanges 把一组变更记录合并进净变化表。fromSchema 是消费方编写版本的
// Schema，仅在某属性首次被变更触及时做一次 map 查找以确定 old 侧状态。
func applyChanges(net map[string]map[string]netProp, fromSchema Schema, changes []Change) {
	entry := func(objectType, property string) netProp {
		bucket, ok := net[objectType]
		if !ok {
			bucket = map[string]netProp{}
			net[objectType] = bucket
		}
		np, ok := bucket[property]
		if ok {
			return np
		}
		np.oldProp, np.oldPresent = lookupProp(fromSchema, objectType, property)
		np.newPresent, np.newProp = np.oldPresent, np.oldProp
		bucket[property] = np
		return np
	}
	put := func(objectType, property string, np netProp) {
		net[objectType][property] = np
	}

	for _, c := range changes {
		np := entry(c.ObjectType, c.Property)
		switch c.Kind {
		case ChangeAddProperty:
			np.newPresent = true
			np.newProp = Property{Type: c.Type, Required: c.Required}
		case ChangeRemoveProperty:
			np.newPresent = false
			np.newProp = Property{}
		case ChangeRetypeProperty:
			np.newProp.Type = c.Type
		case ChangeRequireProperty:
			np.newProp.Required = true
		case ChangeUnrequireProperty:
			np.newProp.Required = false
		}
		put(c.ObjectType, c.Property, np)
	}
}

func lookupProp(s Schema, objectType, property string) (Property, bool) {
	ot, ok := s[objectType]
	if !ok {
		return Property{}, false
	}
	p, ok := ot[property]
	return p, ok
}

// indexOfVersion 返回版本 v 在 chain 中的下标，未找到返回 -1。
func indexOfVersion(chain []Format, v Version) int {
	for i := range chain {
		if Compare(chain[i].Version, v) == 0 {
			return i
		}
	}
	return -1
}
