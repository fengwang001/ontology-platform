package replay

// 本文件实现覆盖层（copy-on-write）工作状态。
//
// 重放绝不修改原始良好快照：所有变更只写入覆盖层 map，读取时先查
// 覆盖层再回退到快照。快照索引（SnapshotIndexes）也以同样方式维护
// 增量。因此校验开销只与差异记录规模相关，与快照中未被触及的对象
// 与链接总数无关；stats 计数器提供可复核的证明（见 engine.go）。

// Stats 统计一次校验中对原始快照的读取次数，用于复核
// “开销只与差异记录规模相关”这一性质。
type Stats struct {
	BaseObjectTypeReads int // 对快照对象类型表的读取次数
	BaseLinkTypeReads   int // 对快照链接类型表的读取次数
	BaseObjectReads     int // 对快照对象表的读取次数
	BaseLinkReads       int // 对快照链接表的读取次数
	BaseIndexReads      int // 对快照索引的读取次数
}

// overlay 是基于快照的覆盖层工作状态。
type overlay struct {
	base *Snapshot

	objectTypeMods map[string]*ObjectTypeDef // nil 值表示删除
	linkTypeMods   map[string]*LinkTypeDef   // nil 值表示删除
	objectMods     map[string]*ObjectInstance
	linkMods       map[LinkKey]*LinkInstance

	objectCount     int // 当前有效对象总数
	linkCount       int // 当前有效链接总数
	objectTypeCount int // 当前有效对象类型总数
	linkTypeCount   int // 当前有效链接类型总数

	objectTypeCountDelta    map[string]int
	linkTypeCountDelta      map[string]int
	linkEndpointCountDelta  map[string]int
	linkSourceCountDelta    map[LinkSourceKey]int
	propertyUsageCountDelta map[TypePropertyKey]int

	stats *Stats // 可为 nil
}

// newOverlay 在快照之上创建一个空覆盖层。
func newOverlay(base *Snapshot, stats *Stats) *overlay {
	return &overlay{
		base:                    base,
		objectTypeMods:          map[string]*ObjectTypeDef{},
		linkTypeMods:            map[string]*LinkTypeDef{},
		objectMods:              map[string]*ObjectInstance{},
		linkMods:                map[LinkKey]*LinkInstance{},
		objectCount:             len(base.Objects),
		linkCount:               len(base.Links),
		objectTypeCount:         len(base.Schema.ObjectTypes),
		linkTypeCount:           len(base.Schema.LinkTypes),
		objectTypeCountDelta:    map[string]int{},
		linkTypeCountDelta:      map[string]int{},
		linkEndpointCountDelta:  map[string]int{},
		linkSourceCountDelta:    map[LinkSourceKey]int{},
		propertyUsageCountDelta: map[TypePropertyKey]int{},
		stats:                   stats,
	}
}

// getObjectType 读取当前有效的对象类型定义。
func (o *overlay) getObjectType(id string) (ObjectTypeDef, bool) {
	if mod, ok := o.objectTypeMods[id]; ok {
		if mod == nil {
			return ObjectTypeDef{}, false
		}
		return *mod, true
	}
	if o.stats != nil {
		o.stats.BaseObjectTypeReads++
	}
	def, ok := o.base.Schema.ObjectTypes[id]
	return def, ok
}

// getLinkType 读取当前有效的链接类型定义。
func (o *overlay) getLinkType(id string) (LinkTypeDef, bool) {
	if mod, ok := o.linkTypeMods[id]; ok {
		if mod == nil {
			return LinkTypeDef{}, false
		}
		return *mod, true
	}
	if o.stats != nil {
		o.stats.BaseLinkTypeReads++
	}
	def, ok := o.base.Schema.LinkTypes[id]
	return def, ok
}

// getObject 读取当前有效的对象实例。返回值的 Properties 可能
// 与快照共享，调用方不得修改。
func (o *overlay) getObject(id string) (ObjectInstance, bool) {
	if mod, ok := o.objectMods[id]; ok {
		if mod == nil {
			return ObjectInstance{}, false
		}
		return *mod, true
	}
	if o.stats != nil {
		o.stats.BaseObjectReads++
	}
	obj, ok := o.base.Objects[id]
	return obj, ok
}

// getLink 读取当前有效的链接实例。
func (o *overlay) getLink(key LinkKey) (LinkInstance, bool) {
	if mod, ok := o.linkMods[key]; ok {
		if mod == nil {
			return LinkInstance{}, false
		}
		return *mod, true
	}
	if o.stats != nil {
		o.stats.BaseLinkReads++
	}
	l, ok := o.base.Links[key]
	return l, ok
}

// mutableObjectType 返回对象类型定义的可写克隆（写时复制）。
func (o *overlay) mutableObjectType(id string) *ObjectTypeDef {
	if mod, ok := o.objectTypeMods[id]; ok && mod != nil {
		return mod
	}
	def, _ := o.getObjectType(id)
	cloned := cloneObjectTypeDef(def)
	o.objectTypeMods[id] = &cloned
	return &cloned
}

// mutableLinkType 返回链接类型定义的可写克隆（写时复制）。
func (o *overlay) mutableLinkType(id string) *LinkTypeDef {
	if mod, ok := o.linkTypeMods[id]; ok && mod != nil {
		return mod
	}
	def, _ := o.getLinkType(id)
	cloned := cloneLinkTypeDef(def)
	o.linkTypeMods[id] = &cloned
	return &cloned
}

// --- 索引有效值读取（快照索引 + 覆盖层增量） ---

func (o *overlay) objectTypeUsage(typeID string) int {
	if o.stats != nil {
		o.stats.BaseIndexReads++
	}
	return o.base.Indexes.ObjectTypeCounts[typeID] + o.objectTypeCountDelta[typeID]
}

func (o *overlay) linkTypeUsage(linkTypeID string) int {
	if o.stats != nil {
		o.stats.BaseIndexReads++
	}
	return o.base.Indexes.LinkTypeCounts[linkTypeID] + o.linkTypeCountDelta[linkTypeID]
}

func (o *overlay) linkEndpointUsage(objectID string) int {
	if o.stats != nil {
		o.stats.BaseIndexReads++
	}
	return o.base.Indexes.LinkEndpointCounts[objectID] + o.linkEndpointCountDelta[objectID]
}

func (o *overlay) linkSourceUsage(key LinkSourceKey) int {
	if o.stats != nil {
		o.stats.BaseIndexReads++
	}
	return o.base.Indexes.LinkSourceCounts[key] + o.linkSourceCountDelta[key]
}

func (o *overlay) propertyUsage(key TypePropertyKey) int {
	if o.stats != nil {
		o.stats.BaseIndexReads++
	}
	return o.base.Indexes.PropertyUsageCounts[key] + o.propertyUsageCountDelta[key]
}

// --- 变更写入 ---

// putObject 写入一个对象实例（克隆），并维护计数与索引增量。
// before 为 nil 表示新增；否则 before 为变化前状态。
func (o *overlay) putObject(after ObjectInstance, before *ObjectInstance) {
	stored := cloneObject(after)
	o.objectMods[after.ObjectID] = &stored
	if before == nil {
		o.objectCount++
		o.objectTypeCountDelta[after.ObjectType]++
		for p := range after.Properties {
			o.propertyUsageCountDelta[TypePropertyKey{TypeID: after.ObjectType, Property: p}]++
		}
		return
	}
	// 类型不可变（自洽性校验已保证），只需维护属性使用计数。
	for p := range before.Properties {
		if _, ok := after.Properties[p]; !ok {
			o.propertyUsageCountDelta[TypePropertyKey{TypeID: before.ObjectType, Property: p}]--
		}
	}
	for p := range after.Properties {
		if _, ok := before.Properties[p]; !ok {
			o.propertyUsageCountDelta[TypePropertyKey{TypeID: after.ObjectType, Property: p}]++
		}
	}
}

// removeObject 删除一个对象实例，并维护计数与索引增量。
func (o *overlay) removeObject(obj ObjectInstance) {
	o.objectMods[obj.ObjectID] = nil
	o.objectCount--
	o.objectTypeCountDelta[obj.ObjectType]--
	for p := range obj.Properties {
		o.propertyUsageCountDelta[TypePropertyKey{TypeID: obj.ObjectType, Property: p}]--
	}
}

// putLink 写入一条链接实例，并维护计数与索引增量。
func (o *overlay) putLink(l LinkInstance) {
	stored := l
	o.linkMods[l.Key()] = &stored
	o.linkCount++
	o.linkTypeCountDelta[l.LinkTypeID]++
	o.linkEndpointCountDelta[l.SourceID]++
	o.linkEndpointCountDelta[l.TargetID]++
	o.linkSourceCountDelta[LinkSourceKey{LinkTypeID: l.LinkTypeID, SourceID: l.SourceID}]++
}

// removeLink 删除一条链接实例，并维护计数与索引增量。
func (o *overlay) removeLink(l LinkInstance) {
	o.linkMods[l.Key()] = nil
	o.linkCount--
	o.linkTypeCountDelta[l.LinkTypeID]--
	o.linkEndpointCountDelta[l.SourceID]--
	o.linkEndpointCountDelta[l.TargetID]--
	o.linkSourceCountDelta[LinkSourceKey{LinkTypeID: l.LinkTypeID, SourceID: l.SourceID}]--
}

// addObjectType 写入新对象类型定义。
func (o *overlay) addObjectType(def ObjectTypeDef) {
	cloned := cloneObjectTypeDef(def)
	o.objectTypeMods[def.TypeID] = &cloned
	o.objectTypeCount++
}

// removeObjectType 删除对象类型定义。
func (o *overlay) removeObjectType(typeID string) {
	o.objectTypeMods[typeID] = nil
	o.objectTypeCount--
}

// addLinkType 写入新链接类型定义。
func (o *overlay) addLinkType(def LinkTypeDef) {
	cloned := cloneLinkTypeDef(def)
	o.linkTypeMods[def.TypeID] = &cloned
	o.linkTypeCount++
}

// removeLinkType 删除链接类型定义。
func (o *overlay) removeLinkType(linkTypeID string) {
	o.linkTypeMods[linkTypeID] = nil
	o.linkTypeCount--
}
