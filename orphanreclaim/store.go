package orphanreclaim

// objectState 是单个对象实例的完整运行时状态。
type objectState struct {
	id string

	// 入边维护：按链接类型计数，判定只核对类型桶而非逐条入边。
	inCounts map[string]int
	// 出边：source -> 该 source 指向本对象的链接类型集合（清理时级联用）。
	inLinks map[string]map[string]struct{}
	// 本对象指向他人的边：target -> 类型集合。
	outLinks map[string]map[string]struct{}

	gen      Generation // GenNone/Gen1/Gen2
	since    int64      // 进入当前代队列的判定时刻（时钟刻度）
	deadline int64      // since + 当前代宽限期
}

func newObjectState(id string) *objectState {
	return &objectState{
		id:       id,
		inCounts: map[string]int{},
		inLinks:  map[string]map[string]struct{}{},
		outLinks: map[string]map[string]struct{}{},
	}
}

// addInEdge 维护入边索引与类型计数器。重复登记同一条边时幂等返回 false。
func (o *objectState) addInEdge(source, linkType string) bool {
	types, ok := o.inLinks[source]
	if !ok {
		types = map[string]struct{}{}
		o.inLinks[source] = types
	}
	if _, dup := types[linkType]; dup {
		return false
	}
	types[linkType] = struct{}{}
	o.inCounts[linkType]++
	return true
}

// removeInEdge 删除入边并在类型桶归零时删除键，保证判定看不到陈旧类型。
func (o *objectState) removeInEdge(source, linkType string) bool {
	types, ok := o.inLinks[source]
	if !ok {
		return false
	}
	if _, ok := types[linkType]; !ok {
		return false
	}
	delete(types, linkType)
	if len(types) == 0 {
		delete(o.inLinks, source)
	}
	o.inCounts[linkType]--
	if o.inCounts[linkType] == 0 {
		delete(o.inCounts, linkType)
	}
	return true
}
