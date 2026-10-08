package replay

// 本文件实现朴素参照模型：按规则逐步独立判定，用于与优化实现做随机对照。
// 与优化实现刻意保持实现风格差异：整体深拷贝快照后直接在其上重放、
// 约束计数用全量扫描、不做任何索引与增量优化。

// naiveModel 为朴素模型阶段一的产出。
type naiveModel struct {
	assumptions      []assumption
	touchedObjTypes  map[string]bool
	touchedLinkTypes map[string]bool
	touchedObjects   map[string]bool
	touchedLinks     map[string]bool
}

// naiveValidate 朴素参照判定，与 Validator.Validate 遵循相同规则与优先级。
func naiveValidate(snap *Snapshot, delta *Delta) Verdict {
	model, f := naiveSelfCheck(delta)
	if f != nil {
		return Verdict{Category: DeltaInconsistent, ChangeIndex: f.changeIndex, Reason: f.reason}
	}
	if f := naivePreconditions(snap, model); f != nil {
		return Verdict{Category: PreconditionUnmet, ChangeIndex: f.changeIndex, Reason: f.reason}
	}
	work, f := naiveReplay(snap, delta)
	if f != nil {
		return Verdict{Category: DeltaInconsistent, ChangeIndex: f.changeIndex, Reason: f.reason}
	}
	if f := naiveEquivalence(work, delta, model); f != nil {
		return Verdict{Category: NotEquivalent, Location: f.location, ChangeIndex: -1, Reason: f.reason}
	}
	return Verdict{Category: Valid, ChangeIndex: -1, Reason: "朴素模型：重放结果与声明目标一致"}
}

// --- 朴素阶段一：差异记录自洽性 ---

type nObjType struct {
	state tri
	def   *ObjectType
	mods  map[string]propMod
}

type nLinkType struct {
	state  tri
	def    *LinkType
	before *Constraint
	after  *Constraint
}

type naiveSelfChecker struct {
	model     *naiveModel
	objTypes  map[string]*nObjType
	linkTypes map[string]*nLinkType
	objects   map[string]*ObjectInstance
	links     map[string]*LinkInstance

	objSchemaAt    map[string][]int
	objInstanceAt  map[string][]int
	linkSchemaAt   map[string][]int
	linkInstanceAt map[string][]int
}

func naiveSelfCheck(delta *Delta) (*naiveModel, *failure) {
	c := &naiveSelfChecker{
		model: &naiveModel{
			touchedObjTypes:  map[string]bool{},
			touchedLinkTypes: map[string]bool{},
			touchedObjects:   map[string]bool{},
			touchedLinks:     map[string]bool{},
		},
		objTypes:       map[string]*nObjType{},
		linkTypes:      map[string]*nLinkType{},
		objects:        map[string]*ObjectInstance{},
		links:          map[string]*LinkInstance{},
		objSchemaAt:    map[string][]int{},
		objInstanceAt:  map[string][]int{},
		linkSchemaAt:   map[string][]int{},
		linkInstanceAt: map[string][]int{},
	}
	for i := range delta.Changes {
		if f := c.step(delta, i, &delta.Changes[i]); f != nil {
			return nil, f
		}
	}
	// 顺序核对：任一类型名存在“实例变化早于结构变化”即不自洽。
	for name, instIdxs := range c.objInstanceAt {
		for _, si := range c.objSchemaAt[name] {
			for _, ii := range instIdxs {
				if ii < si {
					return nil, inconsistentf(si, "对象类型 %q 的结构层变化（第 %d 条）出现在相关实例层变化（第 %d 条）之后", name, si, ii)
				}
			}
		}
	}
	for name, instIdxs := range c.linkInstanceAt {
		for _, si := range c.linkSchemaAt[name] {
			for _, ii := range instIdxs {
				if ii < si {
					return nil, inconsistentf(si, "链接类型 %q 的结构层变化（第 %d 条）出现在相关实例层变化（第 %d 条）之后", name, si, ii)
				}
			}
		}
	}
	// 目标完备性。
	t := &delta.Target
	if f := checkTouchedVsTarget("对象类型", c.model.touchedObjTypes, t.ObjectTypes); f != nil {
		return nil, f
	}
	if f := checkTouchedVsTarget("链接类型", c.model.touchedLinkTypes, t.LinkTypes); f != nil {
		return nil, f
	}
	if f := checkTouchedVsTarget("对象", c.model.touchedObjects, t.Objects); f != nil {
		return nil, f
	}
	if f := checkTouchedVsTarget("链接", c.model.touchedLinks, t.Links); f != nil {
		return nil, f
	}
	return c.model, nil
}

func (c *naiveSelfChecker) objType(name string) *nObjType {
	p, ok := c.objTypes[name]
	if !ok {
		p = &nObjType{state: triUnknown, mods: map[string]propMod{}}
		c.objTypes[name] = p
	}
	return p
}

func (c *naiveSelfChecker) linkType(name string) *nLinkType {
	p, ok := c.linkTypes[name]
	if !ok {
		p = &nLinkType{state: triUnknown}
		c.linkTypes[name] = p
	}
	return p
}

func (c *naiveSelfChecker) step(delta *Delta, i int, ch *Change) *failure {
	switch ch.Kind {
	case AddObjectType:
		if !validObjectTypeDecl(ch.DeclaredObjectType) || ch.DeclaredObjectType.Name != ch.TypeName {
			return inconsistentf(i, "AddObjectType 缺少与 TypeName 一致的合法类型声明")
		}
		c.objSchemaAt[ch.TypeName] = append(c.objSchemaAt[ch.TypeName], i)
		c.model.touchedObjTypes[ch.TypeName] = true
		p := c.objType(ch.TypeName)
		if p.state == triPresent {
			return inconsistentf(i, "对象类型 %q 被重复新增", ch.TypeName)
		}
		if p.state == triUnknown {
			c.model.assumptions = append(c.model.assumptions, assumption{changeIndex: i, kind: assumeObjectTypeAbsent, typeName: ch.TypeName})
		}
		p.state = triPresent
		p.def = cloneObjectType(ch.DeclaredObjectType)
		p.mods = map[string]propMod{}
	case RemoveObjectType:
		if !validObjectTypeDecl(ch.DeclaredObjectType) || ch.DeclaredObjectType.Name != ch.TypeName {
			return inconsistentf(i, "RemoveObjectType 缺少与 TypeName 一致的合法变化前类型声明")
		}
		c.objSchemaAt[ch.TypeName] = append(c.objSchemaAt[ch.TypeName], i)
		c.model.touchedObjTypes[ch.TypeName] = true
		p := c.objType(ch.TypeName)
		if p.state == triAbsent {
			return inconsistentf(i, "对象类型 %q 被重复删除", ch.TypeName)
		}
		if p.def != nil {
			if !equalObjectType(p.def, ch.DeclaredObjectType) {
				return inconsistentf(i, "RemoveObjectType 声明的变化前定义与差异记录自身推出的类型 %q 定义不一致", ch.TypeName)
			}
		} else {
			base, f := undoObjMods(ch.TypeName, ch.DeclaredObjectType, p.mods, i)
			if f != nil {
				return f
			}
			c.model.assumptions = append(c.model.assumptions, assumption{changeIndex: i, kind: assumeObjectTypeEqual, typeName: ch.TypeName, objectType: base})
		}
		p.state = triAbsent
		p.def = nil
		p.mods = map[string]propMod{}
	case AddProperty, RemoveProperty, SetPropertyType:
		if f := c.schemaProp(i, ch); f != nil {
			return f
		}
	case AddLinkType:
		if !validLinkTypeDecl(ch.DeclaredLinkType) || ch.DeclaredLinkType.Name != ch.TypeName {
			return inconsistentf(i, "AddLinkType 缺少与 TypeName 一致的合法类型声明")
		}
		c.linkSchemaAt[ch.TypeName] = append(c.linkSchemaAt[ch.TypeName], i)
		c.model.touchedLinkTypes[ch.TypeName] = true
		p := c.linkType(ch.TypeName)
		if p.state == triPresent {
			return inconsistentf(i, "链接类型 %q 被重复新增", ch.TypeName)
		}
		if p.state == triUnknown {
			c.model.assumptions = append(c.model.assumptions, assumption{changeIndex: i, kind: assumeLinkTypeAbsent, typeName: ch.TypeName})
		}
		p.state = triPresent
		p.def = cloneLinkType(ch.DeclaredLinkType)
		p.before, p.after = nil, nil
	case RemoveLinkType:
		if !validLinkTypeDecl(ch.DeclaredLinkType) || ch.DeclaredLinkType.Name != ch.TypeName {
			return inconsistentf(i, "RemoveLinkType 缺少与 TypeName 一致的合法变化前类型声明")
		}
		c.linkSchemaAt[ch.TypeName] = append(c.linkSchemaAt[ch.TypeName], i)
		c.model.touchedLinkTypes[ch.TypeName] = true
		p := c.linkType(ch.TypeName)
		if p.state == triAbsent {
			return inconsistentf(i, "链接类型 %q 被重复删除", ch.TypeName)
		}
		if p.def != nil {
			if !equalLinkType(p.def, ch.DeclaredLinkType) {
				return inconsistentf(i, "RemoveLinkType 声明的变化前定义与差异记录自身推出的链接类型 %q 定义不一致", ch.TypeName)
			}
		} else {
			base := cloneLinkType(ch.DeclaredLinkType)
			if p.after != nil {
				if ch.DeclaredLinkType.Constraint != *p.after {
					return inconsistentf(i, "RemoveLinkType 声明的变化前约束与差异记录自身推出的链接类型 %q 约束不一致", ch.TypeName)
				}
				base.Constraint = *p.before
			}
			c.model.assumptions = append(c.model.assumptions, assumption{changeIndex: i, kind: assumeLinkTypeEqual, typeName: ch.TypeName, linkType: base})
		}
		p.state = triAbsent
		p.def = nil
		p.before, p.after = nil, nil
	case SetLinkConstraint:
		if ch.TypeName == "" || ch.BeforeConstraint == nil || ch.AfterConstraint == nil ||
			ch.BeforeConstraint.MaxOutgoingPerSource < 0 || ch.BeforeConstraint.MaxIncomingPerTarget < 0 ||
			ch.AfterConstraint.MaxOutgoingPerSource < 0 || ch.AfterConstraint.MaxIncomingPerTarget < 0 {
			return inconsistentf(i, "SetLinkConstraint 缺少类型名或合法的变化前/后约束")
		}
		c.linkSchemaAt[ch.TypeName] = append(c.linkSchemaAt[ch.TypeName], i)
		c.model.touchedLinkTypes[ch.TypeName] = true
		p := c.linkType(ch.TypeName)
		if p.state == triAbsent {
			return inconsistentf(i, "链接类型 %q 已被差异记录删除，无法再修改约束", ch.TypeName)
		}
		if p.def != nil {
			if p.def.Constraint != *ch.BeforeConstraint {
				return inconsistentf(i, "SetLinkConstraint 声明的变化前约束与差异记录自身推出的链接类型 %q 约束不一致", ch.TypeName)
			}
			p.def.Constraint = *ch.AfterConstraint
			return nil
		}
		if p.after != nil {
			if *p.after != *ch.BeforeConstraint {
				return inconsistentf(i, "SetLinkConstraint 声明的变化前约束与差异记录自身推出的链接类型 %q 约束不一致", ch.TypeName)
			}
			after := *ch.AfterConstraint
			p.after = &after
			return nil
		}
		c.model.assumptions = append(c.model.assumptions, assumption{changeIndex: i, kind: assumeConstraintEqual, typeName: ch.TypeName, constraint: ch.BeforeConstraint})
		before, after := *ch.BeforeConstraint, *ch.AfterConstraint
		p.before, p.after = &before, &after
	case CreateObject, UpdateObject, DeleteObject, CreateLink, DeleteLink:
		return c.instance(i, ch)
	default:
		return inconsistentf(i, "未知的变化种类 %d", int(ch.Kind))
	}
	return nil
}

// schemaProp 朴素处理属性级结构变化。
func (c *naiveSelfChecker) schemaProp(i int, ch *Change) *failure {
	if ch.TypeName == "" || ch.Property == "" {
		return inconsistentf(i, "属性级结构变化缺少类型名或属性名")
	}
	needBefore := ch.Kind != AddProperty
	needAfter := ch.Kind != RemoveProperty
	if needBefore && !validPropType(ch.BeforePropType) {
		return inconsistentf(i, "属性级结构变化缺少合法的变化前属性类型")
	}
	if needAfter && !validPropType(ch.AfterPropType) {
		return inconsistentf(i, "属性级结构变化缺少合法的变化后属性类型")
	}
	c.objSchemaAt[ch.TypeName] = append(c.objSchemaAt[ch.TypeName], i)
	c.model.touchedObjTypes[ch.TypeName] = true
	p := c.objType(ch.TypeName)
	if p.state == triAbsent {
		return inconsistentf(i, "对象类型 %q 已被差异记录删除，无法再变更属性", ch.TypeName)
	}
	// known 报告该属性的当前投影是否已确定（不依赖快照基座）。
	known := p.def != nil
	if !known {
		_, known = p.mods[ch.Property]
	}
	current := func() (string, bool) {
		if p.def != nil {
			t, ok := p.def.Properties[ch.Property]
			return t, ok
		}
		if m, ok := p.mods[ch.Property]; ok {
			if m.after == nil {
				return "", false
			}
			return *m.after, true
		}
		return "", false
	}
	record := func(before, after *string) {
		if p.def != nil {
			if after == nil {
				delete(p.def.Properties, ch.Property)
			} else {
				p.def.Properties[ch.Property] = *after
			}
			return
		}
		m, ok := p.mods[ch.Property]
		if !ok {
			if before != nil {
				c.model.assumptions = append(c.model.assumptions, assumption{
					changeIndex: i, kind: assumePropertyEqual,
					typeName: ch.TypeName, property: ch.Property, propType: *before,
				})
			} else {
				c.model.assumptions = append(c.model.assumptions, assumption{
					changeIndex: i, kind: assumePropertyAbsent,
					typeName: ch.TypeName, property: ch.Property,
				})
			}
			p.mods[ch.Property] = propMod{before: before, after: after}
			return
		}
		m.after = after
		if m.before == nil && after == nil {
			delete(p.mods, ch.Property)
			return
		}
		p.mods[ch.Property] = m
	}
	switch ch.Kind {
	case AddProperty:
		if known {
			if _, ok := current(); ok {
				return inconsistentf(i, "属性 %q.%q 被重复新增", ch.TypeName, ch.Property)
			}
		}
		after := ch.AfterPropType
		record(nil, &after)
	case RemoveProperty:
		if known {
			cur, ok := current()
			if !ok || cur != ch.BeforePropType {
				return inconsistentf(i, "RemoveProperty 声明的变化前类型与差异记录自身推出的属性 %q.%q 不一致", ch.TypeName, ch.Property)
			}
		}
		record(ptr(ch.BeforePropType), nil)
	case SetPropertyType:
		if known {
			cur, ok := current()
			if !ok || cur != ch.BeforePropType {
				return inconsistentf(i, "SetPropertyType 声明的变化前类型与差异记录自身推出的属性 %q.%q 不一致", ch.TypeName, ch.Property)
			}
		}
		record(ptr(ch.BeforePropType), ptr(ch.AfterPropType))
	}
	return nil
}

// instance 朴素处理实例层变化。
func (c *naiveSelfChecker) instance(i int, ch *Change) *failure {
	requireObjType := func(typeName string) *failure {
		p := c.objType(typeName)
		switch p.state {
		case triAbsent:
			return inconsistentf(i, "实例变化引用的对象类型 %q 已被差异记录删除", typeName)
		case triUnknown:
			c.model.assumptions = append(c.model.assumptions, assumption{changeIndex: i, kind: assumeObjectTypeExists, typeName: typeName})
		}
		return nil
	}
	switch ch.Kind {
	case CreateObject:
		if !validObjectInst(ch.ObjectAfter) || ch.ObjectBefore != nil {
			return inconsistentf(i, "CreateObject 需要合法的变化后实例且不得声明变化前状态")
		}
		id := ch.ObjectAfter.ID
		c.objInstanceAt[ch.ObjectAfter.Type] = append(c.objInstanceAt[ch.ObjectAfter.Type], i)
		c.model.touchedObjects[id] = true
		if prev, touched := c.objects[id]; touched {
			if prev != nil {
				return inconsistentf(i, "对象 %q 被重复创建", id)
			}
		} else {
			c.model.assumptions = append(c.model.assumptions, assumption{changeIndex: i, kind: assumeObjectAbsent, id: id})
		}
		if f := requireObjType(ch.ObjectAfter.Type); f != nil {
			return f
		}
		c.objects[id] = cloneObject(ch.ObjectAfter)
	case UpdateObject:
		if !validObjectInst(ch.ObjectBefore) || !validObjectInst(ch.ObjectAfter) ||
			ch.ObjectBefore.ID != ch.ObjectAfter.ID || ch.ObjectBefore.Type != ch.ObjectAfter.Type {
			return inconsistentf(i, "UpdateObject 需要 ID 与类型一致的合法变化前/后实例")
		}
		id := ch.ObjectAfter.ID
		c.objInstanceAt[ch.ObjectAfter.Type] = append(c.objInstanceAt[ch.ObjectAfter.Type], i)
		c.model.touchedObjects[id] = true
		if prev, touched := c.objects[id]; touched {
			if prev == nil || !equalObject(prev, ch.ObjectBefore) {
				return inconsistentf(i, "UpdateObject 声明的变化前状态与差异记录自身推出的对象 %q 状态不一致", id)
			}
		} else {
			c.model.assumptions = append(c.model.assumptions, assumption{changeIndex: i, kind: assumeObjectEqual, id: id, object: ch.ObjectBefore})
		}
		if f := requireObjType(ch.ObjectAfter.Type); f != nil {
			return f
		}
		c.objects[id] = cloneObject(ch.ObjectAfter)
	case DeleteObject:
		if !validObjectInst(ch.ObjectBefore) || ch.ObjectAfter != nil {
			return inconsistentf(i, "DeleteObject 需要合法的变化前实例且不得声明变化后状态")
		}
		id := ch.ObjectBefore.ID
		c.objInstanceAt[ch.ObjectBefore.Type] = append(c.objInstanceAt[ch.ObjectBefore.Type], i)
		c.model.touchedObjects[id] = true
		if prev, touched := c.objects[id]; touched {
			if prev == nil || !equalObject(prev, ch.ObjectBefore) {
				return inconsistentf(i, "DeleteObject 声明的变化前状态与差异记录自身推出的对象 %q 状态不一致", id)
			}
		} else {
			c.model.assumptions = append(c.model.assumptions, assumption{changeIndex: i, kind: assumeObjectEqual, id: id, object: ch.ObjectBefore})
		}
		c.objects[id] = nil
	case CreateLink:
		if !validLinkInst(ch.LinkAfter) || ch.LinkBefore != nil {
			return inconsistentf(i, "CreateLink 需要合法的变化后实例且不得声明变化前状态")
		}
		id := ch.LinkAfter.ID
		c.linkInstanceAt[ch.LinkAfter.Type] = append(c.linkInstanceAt[ch.LinkAfter.Type], i)
		c.model.touchedLinks[id] = true
		if prev, touched := c.links[id]; touched {
			if prev != nil {
				return inconsistentf(i, "链接 %q 被重复创建", id)
			}
		} else {
			c.model.assumptions = append(c.model.assumptions, assumption{changeIndex: i, kind: assumeLinkAbsent, id: id})
		}
		p := c.linkType(ch.LinkAfter.Type)
		switch p.state {
		case triAbsent:
			return inconsistentf(i, "实例变化引用的链接类型 %q 已被差异记录删除", ch.LinkAfter.Type)
		case triUnknown:
			c.model.assumptions = append(c.model.assumptions, assumption{changeIndex: i, kind: assumeLinkTypeExists, typeName: ch.LinkAfter.Type})
		}
		for _, endpoint := range [2]string{ch.LinkAfter.Source, ch.LinkAfter.Target} {
			if prev, touched := c.objects[endpoint]; touched {
				if prev == nil {
					return inconsistentf(i, "链接 %q 的端点对象 %q 在差异记录自身投影中不存在", id, endpoint)
				}
			} else {
				c.model.assumptions = append(c.model.assumptions, assumption{changeIndex: i, kind: assumeObjectExists, id: endpoint})
			}
		}
		c.links[id] = cloneLink(ch.LinkAfter)
	case DeleteLink:
		if !validLinkInst(ch.LinkBefore) || ch.LinkAfter != nil {
			return inconsistentf(i, "DeleteLink 需要合法的变化前实例且不得声明变化后状态")
		}
		id := ch.LinkBefore.ID
		c.linkInstanceAt[ch.LinkBefore.Type] = append(c.linkInstanceAt[ch.LinkBefore.Type], i)
		c.model.touchedLinks[id] = true
		if prev, touched := c.links[id]; touched {
			if prev == nil || !equalLink(prev, ch.LinkBefore) {
				return inconsistentf(i, "DeleteLink 声明的变化前状态与差异记录自身推出的链接 %q 状态不一致", id)
			}
		} else {
			c.model.assumptions = append(c.model.assumptions, assumption{changeIndex: i, kind: assumeLinkEqual, id: id, link: ch.LinkBefore})
		}
		c.links[id] = nil
	}
	return nil
}

// --- 朴素阶段二：前提环境核对（直接读快照） ---

func naivePreconditions(snap *Snapshot, model *naiveModel) *failure {
	view := &snapshotView{snap: snap}
	for _, a := range model.assumptions {
		if f := checkAssumption(view, a); f != nil {
			return f
		}
	}
	return nil
}

// --- 朴素阶段三：整体深拷贝后直接重放 ---

func naiveReplay(snap *Snapshot, delta *Delta) (*Snapshot, *failure) {
	work := CloneSnapshot(snap)
	for i := range delta.Changes {
		if f := naiveApply(work, i, &delta.Changes[i]); f != nil {
			return nil, f
		}
	}
	return work, nil
}

func naiveApply(work *Snapshot, i int, ch *Change) *failure {
	switch ch.Kind {
	case AddObjectType:
		work.Schema.ObjectTypes[ch.TypeName] = *cloneObjectType(ch.DeclaredObjectType)
	case RemoveObjectType:
		delete(work.Schema.ObjectTypes, ch.TypeName)
	case AddProperty:
		t, ok := work.Schema.ObjectTypes[ch.TypeName]
		if !ok {
			return replayFailf(i, "对象类型 %q 在当前重放状态下不存在，无法变更属性", ch.TypeName)
		}
		t.Properties[ch.Property] = ch.AfterPropType
		work.Schema.ObjectTypes[ch.TypeName] = t
	case RemoveProperty:
		t, ok := work.Schema.ObjectTypes[ch.TypeName]
		if !ok {
			return replayFailf(i, "对象类型 %q 在当前重放状态下不存在，无法变更属性", ch.TypeName)
		}
		delete(t.Properties, ch.Property)
		work.Schema.ObjectTypes[ch.TypeName] = t
	case SetPropertyType:
		t, ok := work.Schema.ObjectTypes[ch.TypeName]
		if !ok {
			return replayFailf(i, "对象类型 %q 在当前重放状态下不存在，无法变更属性", ch.TypeName)
		}
		t.Properties[ch.Property] = ch.AfterPropType
		work.Schema.ObjectTypes[ch.TypeName] = t
	case AddLinkType:
		work.Schema.LinkTypes[ch.TypeName] = *cloneLinkType(ch.DeclaredLinkType)
	case RemoveLinkType:
		delete(work.Schema.LinkTypes, ch.TypeName)
	case SetLinkConstraint:
		t, ok := work.Schema.LinkTypes[ch.TypeName]
		if !ok {
			return replayFailf(i, "链接类型 %q 在当前重放状态下不存在，无法修改约束", ch.TypeName)
		}
		t.Constraint = *ch.AfterConstraint
		work.Schema.LinkTypes[ch.TypeName] = t
	case CreateObject, UpdateObject:
		after := ch.ObjectAfter
		t, ok := work.Schema.ObjectTypes[after.Type]
		if !ok {
			return replayFailf(i, "对象类型 %q 在当前重放状态下不存在，无法写入对象 %q", after.Type, after.ID)
		}
		if f := checkObjectAgainstType(i, after, &t); f != nil {
			return f
		}
		if ch.Kind == UpdateObject {
			cur, ok := work.Objects[after.ID]
			if !ok {
				return replayFailf(i, "对象 %q 在当前重放状态下不存在，无法更新", after.ID)
			}
			if !equalObject(&cur, ch.ObjectBefore) {
				return replayFailf(i, "对象 %q 的当前状态与声明的变化前状态不一致", after.ID)
			}
		}
		work.Objects[after.ID] = *cloneObject(after)
	case DeleteObject:
		cur, ok := work.Objects[ch.ObjectBefore.ID]
		if !ok {
			return replayFailf(i, "对象 %q 在当前重放状态下不存在，无法删除", ch.ObjectBefore.ID)
		}
		if !equalObject(&cur, ch.ObjectBefore) {
			return replayFailf(i, "对象 %q 的当前状态与声明的变化前状态不一致", ch.ObjectBefore.ID)
		}
		delete(work.Objects, ch.ObjectBefore.ID)
	case CreateLink:
		after := ch.LinkAfter
		lt, ok := work.Schema.LinkTypes[after.Type]
		if !ok {
			return replayFailf(i, "链接类型 %q 在当前重放状态下不存在，无法创建链接 %q", after.Type, after.ID)
		}
		if _, ok := work.Objects[after.Source]; !ok {
			return replayFailf(i, "链接 %q 的源对象 %q 在当前重放状态下不存在", after.ID, after.Source)
		}
		if _, ok := work.Objects[after.Target]; !ok {
			return replayFailf(i, "链接 %q 的目标对象 %q 在当前重放状态下不存在", after.ID, after.Target)
		}
		// 朴素约束计数：全量扫描当前链接。
		out, in := 0, 0
		for _, l := range work.Links {
			if l.Type != after.Type {
				continue
			}
			if l.Source == after.Source {
				out++
			}
			if l.Target == after.Target {
				in++
			}
		}
		if max := lt.Constraint.MaxOutgoingPerSource; max > 0 && out+1 > max {
			return replayFailf(i, "创建链接 %q 将违反链接类型 %q 对源对象 %q 的出边基数约束（上限 %d）",
				after.ID, after.Type, after.Source, max)
		}
		if max := lt.Constraint.MaxIncomingPerTarget; max > 0 && in+1 > max {
			return replayFailf(i, "创建链接 %q 将违反链接类型 %q 对目标对象 %q 的入边基数约束（上限 %d）",
				after.ID, after.Type, after.Target, max)
		}
		work.Links[after.ID] = *cloneLink(after)
	case DeleteLink:
		cur, ok := work.Links[ch.LinkBefore.ID]
		if !ok {
			return replayFailf(i, "链接 %q 在当前重放状态下不存在，无法删除", ch.LinkBefore.ID)
		}
		if !equalLink(&cur, ch.LinkBefore) {
			return replayFailf(i, "链接 %q 的当前状态与声明的变化前状态不一致", ch.LinkBefore.ID)
		}
		delete(work.Links, ch.LinkBefore.ID)
	}
	return nil
}

// --- 朴素阶段四：等价核对（排序后逐项比较） ---

func naiveEquivalence(work *Snapshot, delta *Delta, model *naiveModel) *failure {
	t := &delta.Target
	for _, name := range sortedKeys(model.touchedObjTypes) {
		got, ok := work.Schema.ObjectTypes[name]
		if !equalObjectTypeOpt(ptr(got), ok, t.ObjectTypes[name]) {
			return notEquivf(SchemaLocation, "对象类型 %q 的重放结果与声明目标不一致", name)
		}
	}
	for _, name := range sortedKeys(model.touchedLinkTypes) {
		got, ok := work.Schema.LinkTypes[name]
		if !equalLinkTypeOpt(ptr(got), ok, t.LinkTypes[name]) {
			return notEquivf(SchemaLocation, "链接类型 %q 的重放结果与声明目标不一致", name)
		}
	}
	for _, id := range sortedKeys(model.touchedObjects) {
		got, ok := work.Objects[id]
		if !equalObjectOpt(ptr(got), ok, t.Objects[id]) {
			return notEquivf(ObjectLocation, "对象 %q 的重放结果与声明目标不一致", id)
		}
	}
	for _, id := range sortedKeys(model.touchedLinks) {
		got, ok := work.Links[id]
		if !equalLinkOpt(ptr(got), ok, t.Links[id]) {
			return notEquivf(LinkLocation, "链接 %q 的重放结果与声明目标不一致", id)
		}
	}
	return nil
}

// naiveTouched 供随机用例构造目标状态：返回差异记录触及的集合。
func naiveTouched(delta *Delta) (objTypes, linkTypes, objects, links map[string]bool) {
	objTypes, linkTypes = map[string]bool{}, map[string]bool{}
	objects, links = map[string]bool{}, map[string]bool{}
	for i := range delta.Changes {
		ch := &delta.Changes[i]
		switch ch.Kind {
		case AddObjectType, RemoveObjectType, AddProperty, RemoveProperty, SetPropertyType:
			objTypes[ch.TypeName] = true
		case AddLinkType, RemoveLinkType, SetLinkConstraint:
			linkTypes[ch.TypeName] = true
		case CreateObject, UpdateObject:
			objects[ch.ObjectAfter.ID] = true
		case DeleteObject:
			objects[ch.ObjectBefore.ID] = true
		case CreateLink:
			links[ch.LinkAfter.ID] = true
		case DeleteLink:
			links[ch.LinkBefore.ID] = true
		}
	}
	return
}
