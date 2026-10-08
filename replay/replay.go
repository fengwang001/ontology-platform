package replay

import "fmt"

// replayState 为重放过程中的写时复制覆盖层：
// 只有被差异记录触及的条目会被复制进覆盖层，读取回退到快照。
// 原始快照不会被任何方式修改；每次校验使用独立覆盖层，天然并发安全。
type replayState struct {
	view *snapshotView

	objTypes  map[string]*ObjectType // nil 表示重放后不存在
	linkTypes map[string]*LinkType
	objects   map[string]*ObjectInstance
	links     map[string]*LinkInstance

	// 相对快照索引的约束计数增量。
	outDelta map[[2]string]int
	inDelta  map[[2]string]int
}

func newReplayState(view *snapshotView) *replayState {
	return &replayState{
		view:      view,
		objTypes:  map[string]*ObjectType{},
		linkTypes: map[string]*LinkType{},
		objects:   map[string]*ObjectInstance{},
		links:     map[string]*LinkInstance{},
		outDelta:  map[[2]string]int{},
		inDelta:   map[[2]string]int{},
	}
}

// getObjectType 读取当前重放状态下的对象类型定义。
// 返回值的 Properties map 可能与快照共享，调用方不得原地修改。
func (s *replayState) getObjectType(name string) (*ObjectType, bool) {
	if t, ok := s.objTypes[name]; ok {
		return t, t != nil
	}
	t, ok := s.view.objectType(name)
	if !ok {
		return nil, false
	}
	return &t, true
}

func (s *replayState) getLinkType(name string) (*LinkType, bool) {
	if t, ok := s.linkTypes[name]; ok {
		return t, t != nil
	}
	t, ok := s.view.linkType(name)
	if !ok {
		return nil, false
	}
	return &t, true
}

func (s *replayState) getObject(id string) (*ObjectInstance, bool) {
	if o, ok := s.objects[id]; ok {
		return o, o != nil
	}
	o, ok := s.view.object(id)
	if !ok {
		return nil, false
	}
	return &o, true
}

func (s *replayState) getLink(id string) (*LinkInstance, bool) {
	if l, ok := s.links[id]; ok {
		return l, l != nil
	}
	l, ok := s.view.link(id)
	if !ok {
		return nil, false
	}
	return &l, true
}

func (s *replayState) outCount(linkType, source string) int {
	return s.view.linkOutCount(linkType, source) + s.outDelta[[2]string{linkType, source}]
}

func (s *replayState) inCount(linkType, target string) int {
	return s.view.linkInCount(linkType, target) + s.inDelta[[2]string{linkType, target}]
}

func replayFailf(idx int, format string, args ...any) *failure {
	return &failure{
		location:    NoLocation,
		changeIndex: idx,
		reason:      "差异记录声明的变化无法重放为合法状态：" + fmt.Sprintf(format, args...),
	}
}

// replayChanges 阶段三：在覆盖层上按差异记录声明的顺序逐条应用变化。
// 实例层变化在应用时基于“当前已演化结构”做合法性核对
// （类型存在性、属性匹配、链接端点存在性、链接约束），
// 既不提前对实例做先行校验，也不在结构变化生效后跳过重新核对。
func replayChanges(view *snapshotView, delta *Delta) (*replayState, *failure) {
	state := newReplayState(view)
	for i := range delta.Changes {
		if f := state.apply(i, &delta.Changes[i]); f != nil {
			return nil, f
		}
	}
	return state, nil
}

func (s *replayState) apply(i int, ch *Change) *failure {
	switch ch.Kind {
	case AddObjectType:
		s.objTypes[ch.TypeName] = cloneObjectType(ch.DeclaredObjectType)
	case RemoveObjectType:
		s.objTypes[ch.TypeName] = nil
	case AddProperty, RemoveProperty, SetPropertyType:
		return s.applyPropertyChange(i, ch)
	case AddLinkType:
		s.linkTypes[ch.TypeName] = cloneLinkType(ch.DeclaredLinkType)
	case RemoveLinkType:
		s.linkTypes[ch.TypeName] = nil
	case SetLinkConstraint:
		t, ok := s.getLinkType(ch.TypeName)
		if !ok {
			return replayFailf(i, "链接类型 %q 在当前重放状态下不存在，无法修改约束", ch.TypeName)
		}
		nt := cloneLinkType(t)
		nt.Constraint = *ch.AfterConstraint
		s.linkTypes[ch.TypeName] = nt
	case CreateObject, UpdateObject:
		return s.applyWriteObject(i, ch)
	case DeleteObject:
		return s.applyDeleteObject(i, ch)
	case CreateLink:
		return s.applyCreateLink(i, ch)
	case DeleteLink:
		return s.applyDeleteLink(i, ch)
	}
	return nil
}

func (s *replayState) applyPropertyChange(i int, ch *Change) *failure {
	t, ok := s.getObjectType(ch.TypeName)
	if !ok {
		return replayFailf(i, "对象类型 %q 在当前重放状态下不存在，无法变更属性", ch.TypeName)
	}
	nt := cloneObjectType(t)
	switch ch.Kind {
	case AddProperty:
		nt.Properties[ch.Property] = ch.AfterPropType
	case RemoveProperty:
		delete(nt.Properties, ch.Property)
	case SetPropertyType:
		nt.Properties[ch.Property] = ch.AfterPropType
	}
	s.objTypes[ch.TypeName] = nt
	return nil
}

// checkObjectAgainstType 核对实例取值与当前类型定义匹配：
// 属性集合恰好一致且每个值与声明的属性类型匹配。
func checkObjectAgainstType(i int, o *ObjectInstance, t *ObjectType) *failure {
	for prop, v := range o.Properties {
		pt, ok := t.Properties[prop]
		if !ok {
			return replayFailf(i, "对象 %q 含有类型 %q 未定义的属性 %q", o.ID, o.Type, prop)
		}
		if !valueMatchesPropType(pt, v) {
			return replayFailf(i, "对象 %q 的属性 %q 取值与类型 %q 声明的类型 %q 不匹配", o.ID, prop, o.Type, pt)
		}
	}
	for prop := range t.Properties {
		if _, ok := o.Properties[prop]; !ok {
			return replayFailf(i, "对象 %q 缺少类型 %q 要求的属性 %q", o.ID, o.Type, prop)
		}
	}
	return nil
}

func (s *replayState) applyWriteObject(i int, ch *Change) *failure {
	after := ch.ObjectAfter
	t, ok := s.getObjectType(after.Type)
	if !ok {
		return replayFailf(i, "对象类型 %q 在当前重放状态下不存在，无法写入对象 %q", after.Type, after.ID)
	}
	if f := checkObjectAgainstType(i, after, t); f != nil {
		return f
	}
	if ch.Kind == UpdateObject {
		cur, ok := s.getObject(after.ID)
		if !ok {
			return replayFailf(i, "对象 %q 在当前重放状态下不存在，无法更新", after.ID)
		}
		if !equalObject(cur, ch.ObjectBefore) {
			return replayFailf(i, "对象 %q 的当前状态与声明的变化前状态不一致", after.ID)
		}
	}
	s.objects[after.ID] = cloneObject(after)
	return nil
}

func (s *replayState) applyDeleteObject(i int, ch *Change) *failure {
	before := ch.ObjectBefore
	cur, ok := s.getObject(before.ID)
	if !ok {
		return replayFailf(i, "对象 %q 在当前重放状态下不存在，无法删除", before.ID)
	}
	if !equalObject(cur, before) {
		return replayFailf(i, "对象 %q 的当前状态与声明的变化前状态不一致", before.ID)
	}
	s.objects[before.ID] = nil
	return nil
}

func (s *replayState) applyCreateLink(i int, ch *Change) *failure {
	after := ch.LinkAfter
	lt, ok := s.getLinkType(after.Type)
	if !ok {
		return replayFailf(i, "链接类型 %q 在当前重放状态下不存在，无法创建链接 %q", after.Type, after.ID)
	}
	if _, ok := s.getObject(after.Source); !ok {
		return replayFailf(i, "链接 %q 的源对象 %q 在当前重放状态下不存在", after.ID, after.Source)
	}
	if _, ok := s.getObject(after.Target); !ok {
		return replayFailf(i, "链接 %q 的目标对象 %q 在当前重放状态下不存在", after.ID, after.Target)
	}
	// 基于当前已生效的约束重新核对，不采信任何先行结论。
	if max := lt.Constraint.MaxOutgoingPerSource; max > 0 && s.outCount(after.Type, after.Source)+1 > max {
		return replayFailf(i, "创建链接 %q 将违反链接类型 %q 对源对象 %q 的出边基数约束（上限 %d）",
			after.ID, after.Type, after.Source, max)
	}
	if max := lt.Constraint.MaxIncomingPerTarget; max > 0 && s.inCount(after.Type, after.Target)+1 > max {
		return replayFailf(i, "创建链接 %q 将违反链接类型 %q 对目标对象 %q 的入边基数约束（上限 %d）",
			after.ID, after.Type, after.Target, max)
	}
	s.links[after.ID] = cloneLink(after)
	s.outDelta[[2]string{after.Type, after.Source}]++
	s.inDelta[[2]string{after.Type, after.Target}]++
	return nil
}

func (s *replayState) applyDeleteLink(i int, ch *Change) *failure {
	before := ch.LinkBefore
	cur, ok := s.getLink(before.ID)
	if !ok {
		return replayFailf(i, "链接 %q 在当前重放状态下不存在，无法删除", before.ID)
	}
	if !equalLink(cur, before) {
		return replayFailf(i, "链接 %q 的当前状态与声明的变化前状态不一致", before.ID)
	}
	s.links[before.ID] = nil
	s.outDelta[[2]string{before.Type, before.Source}]--
	s.inDelta[[2]string{before.Type, before.Target}]--
	return nil
}
