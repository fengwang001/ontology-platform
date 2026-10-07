package ontology

// 本文件实现两种只读状态视图，是前后置校验职责分离的结构载体：
//
//   - StateView（前置阶段）：只暴露执行开始前已持久化的对象与链接状态，
//     类型上不可能观察到本次执行的任何未提交写入；
//   - PlannedView（后置阶段）：只暴露“已提交快照 + 最终写入计划”叠加出的
//     统一计划视图，全部对象使用同一快照基线，不存在先后生成的不一致。
//
// StateView 同时承担读集登记：每一次读取都记录对象版本（或“不存在”），
// 供提交时的乐观并发校验使用。

// StateView 是前置校验与计划生成阶段的只读已提交状态视图。
type StateView struct {
	snap *snapshot
	// reads 记录读取过的对象及其快照版本；-1 表示读取时对象不存在。
	reads map[ObjectID]int64
	// linksRead/linkGen 记录链接读集（链接以代际号整体跟踪）。
	linksRead bool
	linkGen   int64
}

func newStateView(snap *snapshot) *StateView {
	return &StateView{snap: snap, reads: make(map[ObjectID]int64)}
}

// GetObject 读取一个对象的已提交状态；不存在时登记“不存在”读。
func (v *StateView) GetObject(id ObjectID) (Object, bool) {
	obj, ok := v.snap.objects[id]
	if !ok {
		if _, recorded := v.reads[id]; !recorded {
			v.reads[id] = -1
		}
		return Object{}, false
	}
	v.reads[id] = obj.Version
	return obj, true
}

// GetLink 读取一条链接的已提交状态。
func (v *StateView) GetLink(id LinkID) (Link, bool) {
	v.markLinksRead()
	l, ok := v.snap.links[id]
	return l, ok
}

// LinksFrom 返回从指定对象出发的全部链接。
func (v *StateView) LinksFrom(id ObjectID) []Link {
	v.markLinksRead()
	var out []Link
	for _, l := range v.snap.links {
		if l.From == id {
			out = append(out, l)
		}
	}
	return out
}

// LinksTo 返回指向指定对象的全部链接。
func (v *StateView) LinksTo(id ObjectID) []Link {
	v.markLinksRead()
	var out []Link
	for _, l := range v.snap.links {
		if l.To == id {
			out = append(out, l)
		}
	}
	return out
}

func (v *StateView) markLinksRead() {
	if !v.linksRead {
		v.linksRead = true
		v.linkGen = v.snap.linkGen
	}
}

// basis 导出当前读集作为校验依据。
func (v *StateView) basis() StateBasis {
	versions := make(map[ObjectID]int64, len(v.reads))
	for id, ver := range v.reads {
		versions[id] = ver
	}
	return StateBasis{ObjectVersions: versions, LinksRead: v.linksRead, LinkGen: v.linkGen}
}

// PlannedView 是后置校验阶段的只读视图：
// 已提交快照叠加最终写入计划后的统一结果状态。
//
// 它不携带任何读集登记职责——后置校验只允许基于计划结果作出判断，
// 其结论不影响提交时的并发校验（读集在前置/计划阶段已经固定）。
type PlannedView struct {
	snap *snapshot
	plan *WritePlan
}

func newPlannedView(snap *snapshot, plan *WritePlan) *PlannedView {
	return &PlannedView{snap: snap, plan: plan}
}

// GetObject 返回对象在最终计划下的状态。
func (v *PlannedView) GetObject(id ObjectID) (Object, bool) {
	if w, ok := v.plan.objects[id]; ok {
		switch w.kind {
		case writeCreate:
			return Object{ID: id, Type: w.objType, Props: copyProps(w.props), Version: 1}, true
		case writeUpdate:
			base := v.snap.objects[id]
			return Object{ID: id, Type: w.objType, Props: copyProps(w.props), Version: base.Version + 1}, true
		case writeDelete:
			return Object{}, false
		}
	}
	obj, ok := v.snap.objects[id]
	return obj, ok
}

// GetLink 返回链接在最终计划下的状态。
func (v *PlannedView) GetLink(id LinkID) (Link, bool) {
	if w, ok := v.plan.links[id]; ok {
		if w.kind == writeDelete {
			return Link{}, false
		}
		return Link{ID: id, Type: w.linkType, From: w.from, To: w.to, Version: 1}, true
	}
	l, ok := v.snap.links[id]
	return l, ok
}

// LinksFrom 返回最终计划下从指定对象出发的全部链接。
func (v *PlannedView) LinksFrom(id ObjectID) []Link {
	var out []Link
	for lid, l := range v.snap.links {
		if l.From == id {
			if w, ok := v.plan.links[lid]; ok && w.kind == writeDelete {
				continue
			}
			out = append(out, l)
		}
	}
	for lid, w := range v.plan.links {
		if w.kind == writeCreate && w.from == id {
			out = append(out, Link{ID: lid, Type: w.linkType, From: w.from, To: w.to, Version: 1})
		}
	}
	return out
}

// LinksTo 返回最终计划下指向指定对象的全部链接。
func (v *PlannedView) LinksTo(id ObjectID) []Link {
	var out []Link
	for lid, l := range v.snap.links {
		if l.To == id {
			if w, ok := v.plan.links[lid]; ok && w.kind == writeDelete {
				continue
			}
			out = append(out, l)
		}
	}
	for lid, w := range v.plan.links {
		if w.kind == writeCreate && w.to == id {
			out = append(out, Link{ID: lid, Type: w.linkType, From: w.from, To: w.to, Version: 1})
		}
	}
	return out
}
