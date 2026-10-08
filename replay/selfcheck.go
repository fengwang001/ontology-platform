package replay

import "fmt"

// selfModel 为阶段一的产出：差异记录在不依赖快照的情况下
// 可确定的全部信息，供阶段二（前提校验）与阶段四（等价核对）使用。
type selfModel struct {
	delta  *Delta
	target *DeclaredTarget

	// assumptions 为差异记录对快照的全部依赖（需阶段二逐条核对）。
	assumptions []assumption

	// 被结构层变化触及的类型名 / 被实例层变化触及的实体 ID。
	touchedObjectTypes map[string]bool
	touchedLinkTypes   map[string]bool
	touchedObjects     map[string]bool
	touchedLinks       map[string]bool
}

// tri 为三态：未知（依赖快照）/ 存在 / 不存在。
type tri int

const (
	triUnknown tri = iota
	triPresent
	triAbsent
)

// propMod 记录对基座（快照）类型定义中某属性的修改：
// before 为 nil 表示基座中不存在该属性（由差异记录新增），
// after 为 nil 表示投影中该属性已被删除。
type propMod struct {
	before *string
	after  *string
}

type objTypeProj struct {
	exists  tri
	fullDef *ObjectType // 定义完全由差异记录确定时非空
	mods    map[string]propMod
}

type conMod struct {
	before *Constraint
	after  *Constraint
}

type linkTypeProj struct {
	exists  tri
	fullDef *LinkType
	conMod  *conMod
}

type selfChecker struct {
	model *selfModel

	objTypes  map[string]*objTypeProj
	linkTypes map[string]*linkTypeProj
	objects   map[string]*ObjectInstance // 键存在即触及；nil 表示投影中不存在
	links     map[string]*LinkInstance

	objFirstInstance  map[string]int
	objLastSchema     map[string]int
	linkFirstInstance map[string]int
	linkLastSchema    map[string]int
}

func inconsistentf(idx int, format string, args ...any) *failure {
	return &failure{
		location:    NoLocation,
		changeIndex: idx,
		reason:      "差异记录自身不自洽：" + fmt.Sprintf(format, args...),
	}
}

// checkDeltaSelfConsistency 阶段一：差异记录自身自洽性校验。
// 只读取差异记录本身，不访问快照。覆盖：
//  1. 每条变化的内部良构性；
//  2. 差异记录内部投影一致性（声明的变化前状态与差异记录自身推出的状态一致）；
//  3. 结构层变化与相关实例层变化的相对顺序；
//  4. 声明目标与被触及投影的完备对应。
func checkDeltaSelfConsistency(delta *Delta) (*selfModel, *failure) {
	c := &selfChecker{
		model: &selfModel{
			delta:              delta,
			target:             &delta.Target,
			touchedObjectTypes: map[string]bool{},
			touchedLinkTypes:   map[string]bool{},
			touchedObjects:     map[string]bool{},
			touchedLinks:       map[string]bool{},
		},
		objTypes:          map[string]*objTypeProj{},
		linkTypes:         map[string]*linkTypeProj{},
		objects:           map[string]*ObjectInstance{},
		links:             map[string]*LinkInstance{},
		objFirstInstance:  map[string]int{},
		objLastSchema:     map[string]int{},
		linkFirstInstance: map[string]int{},
		linkLastSchema:    map[string]int{},
	}
	for i := range delta.Changes {
		if f := c.checkChange(i, &delta.Changes[i]); f != nil {
			return nil, f
		}
	}
	if f := c.checkOrdering(); f != nil {
		return nil, f
	}
	if f := c.checkTargetCompleteness(); f != nil {
		return nil, f
	}
	return c.model, nil
}

func (c *selfChecker) assume(a assumption) {
	c.model.assumptions = append(c.model.assumptions, a)
}

func (c *selfChecker) objTypeProj(name string) *objTypeProj {
	p, ok := c.objTypes[name]
	if !ok {
		p = &objTypeProj{exists: triUnknown, mods: map[string]propMod{}}
		c.objTypes[name] = p
	}
	return p
}

func (c *selfChecker) linkTypeProj(name string) *linkTypeProj {
	p, ok := c.linkTypes[name]
	if !ok {
		p = &linkTypeProj{exists: triUnknown}
		c.linkTypes[name] = p
	}
	return p
}

func (c *selfChecker) checkChange(i int, ch *Change) *failure {
	if ch.Kind.IsSchema() {
		return c.checkSchemaChange(i, ch)
	}
	return c.checkInstanceChange(i, ch)
}

// noteObjSchema / noteObjInstance 记录结构层与实例层变化的位置，
// 用于相对顺序核对（按类型名关联）。
func (c *selfChecker) noteObjSchema(name string, i int)  { c.objLastSchema[name] = i }
func (c *selfChecker) noteLinkSchema(name string, i int) { c.linkLastSchema[name] = i }

func (c *selfChecker) noteObjInstance(name string, i int) {
	if _, ok := c.objFirstInstance[name]; !ok {
		c.objFirstInstance[name] = i
	}
}

func (c *selfChecker) noteLinkInstance(name string, i int) {
	if _, ok := c.linkFirstInstance[name]; !ok {
		c.linkFirstInstance[name] = i
	}
}

// checkSchemaChange 校验一条结构层变化并推进结构投影。
func (c *selfChecker) checkSchemaChange(i int, ch *Change) *failure {
	switch ch.Kind {
	case AddObjectType:
		return c.addObjectType(i, ch)
	case RemoveObjectType:
		return c.removeObjectType(i, ch)
	case AddProperty:
		return c.addProperty(i, ch)
	case RemoveProperty:
		return c.removeProperty(i, ch)
	case SetPropertyType:
		return c.setPropertyType(i, ch)
	case AddLinkType:
		return c.addLinkType(i, ch)
	case RemoveLinkType:
		return c.removeLinkType(i, ch)
	case SetLinkConstraint:
		return c.setLinkConstraint(i, ch)
	}
	return inconsistentf(i, "未知的变化种类 %d", int(ch.Kind))
}

// validObjectTypeDecl 校验对象类型声明的良构性。
func validObjectTypeDecl(d *ObjectType) bool {
	if d == nil || d.Name == "" {
		return false
	}
	for p, t := range d.Properties {
		if p == "" || !validPropType(t) {
			return false
		}
	}
	return true
}

// validLinkTypeDecl 校验链接类型声明的良构性。
func validLinkTypeDecl(d *LinkType) bool {
	if d == nil || d.Name == "" || d.SourceType == "" || d.TargetType == "" {
		return false
	}
	return d.Constraint.MaxOutgoingPerSource >= 0 && d.Constraint.MaxIncomingPerTarget >= 0
}

func (c *selfChecker) addObjectType(i int, ch *Change) *failure {
	if !validObjectTypeDecl(ch.DeclaredObjectType) || ch.DeclaredObjectType.Name != ch.TypeName {
		return inconsistentf(i, "AddObjectType 缺少与 TypeName 一致的合法类型声明")
	}
	c.noteObjSchema(ch.TypeName, i)
	c.model.touchedObjectTypes[ch.TypeName] = true
	p := c.objTypeProj(ch.TypeName)
	if p.exists == triPresent {
		return inconsistentf(i, "对象类型 %q 被重复新增", ch.TypeName)
	}
	if p.exists == triUnknown {
		c.assume(assumption{changeIndex: i, kind: assumeObjectTypeAbsent, typeName: ch.TypeName})
	}
	p.exists = triPresent
	p.fullDef = cloneObjectType(ch.DeclaredObjectType)
	p.mods = map[string]propMod{}
	return nil
}

func (c *selfChecker) removeObjectType(i int, ch *Change) *failure {
	if !validObjectTypeDecl(ch.DeclaredObjectType) || ch.DeclaredObjectType.Name != ch.TypeName {
		return inconsistentf(i, "RemoveObjectType 缺少与 TypeName 一致的合法变化前类型声明")
	}
	c.noteObjSchema(ch.TypeName, i)
	c.model.touchedObjectTypes[ch.TypeName] = true
	p := c.objTypeProj(ch.TypeName)
	if p.exists == triAbsent {
		return inconsistentf(i, "对象类型 %q 被重复删除", ch.TypeName)
	}
	if p.fullDef != nil {
		if !equalObjectType(p.fullDef, ch.DeclaredObjectType) {
			return inconsistentf(i, "RemoveObjectType 声明的变化前定义与差异记录自身推出的类型 %q 定义不一致", ch.TypeName)
		}
	} else {
		base, f := undoObjMods(ch.TypeName, ch.DeclaredObjectType, p.mods, i)
		if f != nil {
			return f
		}
		c.assume(assumption{changeIndex: i, kind: assumeObjectTypeEqual, typeName: ch.TypeName, objectType: base})
	}
	p.exists = triAbsent
	p.fullDef = nil
	p.mods = map[string]propMod{}
	return nil
}

// undoObjMods 由 Remove 声明的变化前定义反推快照基座定义，
// 并核对声明与差异记录自身的属性修改投影一致。
func undoObjMods(name string, declared *ObjectType, mods map[string]propMod, i int) (*ObjectType, *failure) {
	for prop, m := range mods {
		if m.after == nil {
			if _, ok := declared.Properties[prop]; ok {
				return nil, inconsistentf(i, "RemoveObjectType 声明的变化前定义仍包含已被差异记录删除的属性 %q.%q", name, prop)
			}
		} else if declared.Properties[prop] != *m.after {
			return nil, inconsistentf(i, "RemoveObjectType 声明的变化前定义中属性 %q.%q 与差异记录自身推出的取值不一致", name, prop)
		}
	}
	base := make(map[string]string, len(declared.Properties))
	for prop, t := range declared.Properties {
		base[prop] = t
	}
	for prop, m := range mods {
		if m.before == nil {
			delete(base, prop)
		} else {
			base[prop] = *m.before
		}
	}
	return &ObjectType{Name: name, Properties: base}, nil
}

func (c *selfChecker) addProperty(i int, ch *Change) *failure {
	if ch.TypeName == "" || ch.Property == "" || !validPropType(ch.AfterPropType) {
		return inconsistentf(i, "AddProperty 缺少类型名、属性名或合法的变化后属性类型")
	}
	c.noteObjSchema(ch.TypeName, i)
	c.model.touchedObjectTypes[ch.TypeName] = true
	p := c.objTypeProj(ch.TypeName)
	if p.exists == triAbsent {
		return inconsistentf(i, "对象类型 %q 已被差异记录删除，无法再新增属性", ch.TypeName)
	}
	if p.fullDef != nil {
		if _, ok := p.fullDef.Properties[ch.Property]; ok {
			return inconsistentf(i, "属性 %q.%q 被重复新增", ch.TypeName, ch.Property)
		}
		p.fullDef.Properties[ch.Property] = ch.AfterPropType
		return nil
	}
	if m, ok := p.mods[ch.Property]; ok {
		if m.after != nil {
			return inconsistentf(i, "属性 %q.%q 被重复新增", ch.TypeName, ch.Property)
		}
		after := ch.AfterPropType
		m.after = &after
		p.mods[ch.Property] = m
		return nil
	}
	c.assume(assumption{changeIndex: i, kind: assumePropertyAbsent, typeName: ch.TypeName, property: ch.Property})
	after := ch.AfterPropType
	p.mods[ch.Property] = propMod{before: nil, after: &after}
	return nil
}

func (c *selfChecker) removeProperty(i int, ch *Change) *failure {
	if ch.TypeName == "" || ch.Property == "" || !validPropType(ch.BeforePropType) {
		return inconsistentf(i, "RemoveProperty 缺少类型名、属性名或合法的变化前属性类型")
	}
	c.noteObjSchema(ch.TypeName, i)
	c.model.touchedObjectTypes[ch.TypeName] = true
	p := c.objTypeProj(ch.TypeName)
	if p.exists == triAbsent {
		return inconsistentf(i, "对象类型 %q 已被差异记录删除，无法再删除属性", ch.TypeName)
	}
	if p.fullDef != nil {
		if cur, ok := p.fullDef.Properties[ch.Property]; !ok || cur != ch.BeforePropType {
			return inconsistentf(i, "RemoveProperty 声明的变化前类型与差异记录自身推出的属性 %q.%q 不一致", ch.TypeName, ch.Property)
		}
		delete(p.fullDef.Properties, ch.Property)
		return nil
	}
	if m, ok := p.mods[ch.Property]; ok {
		if m.after == nil || *m.after != ch.BeforePropType {
			return inconsistentf(i, "RemoveProperty 声明的变化前类型与差异记录自身推出的属性 %q.%q 不一致", ch.TypeName, ch.Property)
		}
		if m.before == nil {
			delete(p.mods, ch.Property)
		} else {
			m.after = nil
			p.mods[ch.Property] = m
		}
		return nil
	}
	c.assume(assumption{changeIndex: i, kind: assumePropertyEqual, typeName: ch.TypeName, property: ch.Property, propType: ch.BeforePropType})
	before := ch.BeforePropType
	p.mods[ch.Property] = propMod{before: &before, after: nil}
	return nil
}

func (c *selfChecker) setPropertyType(i int, ch *Change) *failure {
	if ch.TypeName == "" || ch.Property == "" || !validPropType(ch.BeforePropType) || !validPropType(ch.AfterPropType) {
		return inconsistentf(i, "SetPropertyType 缺少类型名、属性名或合法的变化前/后属性类型")
	}
	c.noteObjSchema(ch.TypeName, i)
	c.model.touchedObjectTypes[ch.TypeName] = true
	p := c.objTypeProj(ch.TypeName)
	if p.exists == triAbsent {
		return inconsistentf(i, "对象类型 %q 已被差异记录删除，无法再修改属性", ch.TypeName)
	}
	if p.fullDef != nil {
		if cur, ok := p.fullDef.Properties[ch.Property]; !ok || cur != ch.BeforePropType {
			return inconsistentf(i, "SetPropertyType 声明的变化前类型与差异记录自身推出的属性 %q.%q 不一致", ch.TypeName, ch.Property)
		}
		p.fullDef.Properties[ch.Property] = ch.AfterPropType
		return nil
	}
	if m, ok := p.mods[ch.Property]; ok {
		if m.after == nil || *m.after != ch.BeforePropType {
			return inconsistentf(i, "SetPropertyType 声明的变化前类型与差异记录自身推出的属性 %q.%q 不一致", ch.TypeName, ch.Property)
		}
		after := ch.AfterPropType
		m.after = &after
		p.mods[ch.Property] = m
		return nil
	}
	c.assume(assumption{changeIndex: i, kind: assumePropertyEqual, typeName: ch.TypeName, property: ch.Property, propType: ch.BeforePropType})
	before, after := ch.BeforePropType, ch.AfterPropType
	p.mods[ch.Property] = propMod{before: &before, after: &after}
	return nil
}

func (c *selfChecker) addLinkType(i int, ch *Change) *failure {
	if !validLinkTypeDecl(ch.DeclaredLinkType) || ch.DeclaredLinkType.Name != ch.TypeName {
		return inconsistentf(i, "AddLinkType 缺少与 TypeName 一致的合法类型声明")
	}
	c.noteLinkSchema(ch.TypeName, i)
	c.model.touchedLinkTypes[ch.TypeName] = true
	p := c.linkTypeProj(ch.TypeName)
	if p.exists == triPresent {
		return inconsistentf(i, "链接类型 %q 被重复新增", ch.TypeName)
	}
	if p.exists == triUnknown {
		c.assume(assumption{changeIndex: i, kind: assumeLinkTypeAbsent, typeName: ch.TypeName})
	}
	p.exists = triPresent
	p.fullDef = cloneLinkType(ch.DeclaredLinkType)
	p.conMod = nil
	return nil
}

func (c *selfChecker) removeLinkType(i int, ch *Change) *failure {
	if !validLinkTypeDecl(ch.DeclaredLinkType) || ch.DeclaredLinkType.Name != ch.TypeName {
		return inconsistentf(i, "RemoveLinkType 缺少与 TypeName 一致的合法变化前类型声明")
	}
	c.noteLinkSchema(ch.TypeName, i)
	c.model.touchedLinkTypes[ch.TypeName] = true
	p := c.linkTypeProj(ch.TypeName)
	if p.exists == triAbsent {
		return inconsistentf(i, "链接类型 %q 被重复删除", ch.TypeName)
	}
	if p.fullDef != nil {
		if !equalLinkType(p.fullDef, ch.DeclaredLinkType) {
			return inconsistentf(i, "RemoveLinkType 声明的变化前定义与差异记录自身推出的链接类型 %q 定义不一致", ch.TypeName)
		}
	} else {
		base := cloneLinkType(ch.DeclaredLinkType)
		if p.conMod != nil {
			if ch.DeclaredLinkType.Constraint != *p.conMod.after {
				return inconsistentf(i, "RemoveLinkType 声明的变化前约束与差异记录自身推出的链接类型 %q 约束不一致", ch.TypeName)
			}
			base.Constraint = *p.conMod.before
		}
		c.assume(assumption{changeIndex: i, kind: assumeLinkTypeEqual, typeName: ch.TypeName, linkType: base})
	}
	p.exists = triAbsent
	p.fullDef = nil
	p.conMod = nil
	return nil
}

func (c *selfChecker) setLinkConstraint(i int, ch *Change) *failure {
	if ch.TypeName == "" || ch.BeforeConstraint == nil || ch.AfterConstraint == nil ||
		ch.BeforeConstraint.MaxOutgoingPerSource < 0 || ch.BeforeConstraint.MaxIncomingPerTarget < 0 ||
		ch.AfterConstraint.MaxOutgoingPerSource < 0 || ch.AfterConstraint.MaxIncomingPerTarget < 0 {
		return inconsistentf(i, "SetLinkConstraint 缺少类型名或合法的变化前/后约束")
	}
	c.noteLinkSchema(ch.TypeName, i)
	c.model.touchedLinkTypes[ch.TypeName] = true
	p := c.linkTypeProj(ch.TypeName)
	if p.exists == triAbsent {
		return inconsistentf(i, "链接类型 %q 已被差异记录删除，无法再修改约束", ch.TypeName)
	}
	if p.fullDef != nil {
		if p.fullDef.Constraint != *ch.BeforeConstraint {
			return inconsistentf(i, "SetLinkConstraint 声明的变化前约束与差异记录自身推出的链接类型 %q 约束不一致", ch.TypeName)
		}
		p.fullDef.Constraint = *ch.AfterConstraint
		return nil
	}
	if p.conMod != nil {
		if *p.conMod.after != *ch.BeforeConstraint {
			return inconsistentf(i, "SetLinkConstraint 声明的变化前约束与差异记录自身推出的链接类型 %q 约束不一致", ch.TypeName)
		}
		after := *ch.AfterConstraint
		p.conMod.after = &after
		return nil
	}
	c.assume(assumption{changeIndex: i, kind: assumeConstraintEqual, typeName: ch.TypeName, constraint: ch.BeforeConstraint})
	before, after := *ch.BeforeConstraint, *ch.AfterConstraint
	p.conMod = &conMod{before: &before, after: &after}
	return nil
}

// checkInstanceChange 校验一条实例层变化并推进实例投影。
func (c *selfChecker) checkInstanceChange(i int, ch *Change) *failure {
	switch ch.Kind {
	case CreateObject:
		return c.createObject(i, ch)
	case UpdateObject:
		return c.updateObject(i, ch)
	case DeleteObject:
		return c.deleteObject(i, ch)
	case CreateLink:
		return c.createLink(i, ch)
	case DeleteLink:
		return c.deleteLink(i, ch)
	}
	return inconsistentf(i, "未知的变化种类 %d", int(ch.Kind))
}

// requireObjectTypeUsable 核对实例变化引用的对象类型在投影中可用；
// 若类型存在性依赖快照，则记录一条前提假设。
func (c *selfChecker) requireObjectTypeUsable(i int, typeName string) *failure {
	p := c.objTypeProj(typeName)
	switch p.exists {
	case triAbsent:
		return inconsistentf(i, "实例变化引用的对象类型 %q 已被差异记录删除", typeName)
	case triUnknown:
		c.assume(assumption{changeIndex: i, kind: assumeObjectTypeExists, typeName: typeName})
	}
	return nil
}

func (c *selfChecker) requireLinkTypeUsable(i int, typeName string) *failure {
	p := c.linkTypeProj(typeName)
	switch p.exists {
	case triAbsent:
		return inconsistentf(i, "实例变化引用的链接类型 %q 已被差异记录删除", typeName)
	case triUnknown:
		c.assume(assumption{changeIndex: i, kind: assumeLinkTypeExists, typeName: typeName})
	}
	return nil
}

func validObjectInst(o *ObjectInstance) bool {
	return o != nil && o.ID != "" && o.Type != ""
}

func validLinkInst(l *LinkInstance) bool {
	return l != nil && l.ID != "" && l.Type != "" && l.Source != "" && l.Target != ""
}

func (c *selfChecker) createObject(i int, ch *Change) *failure {
	if !validObjectInst(ch.ObjectAfter) || ch.ObjectBefore != nil {
		return inconsistentf(i, "CreateObject 需要合法的变化后实例且不得声明变化前状态")
	}
	id := ch.ObjectAfter.ID
	c.noteObjInstance(ch.ObjectAfter.Type, i)
	c.model.touchedObjects[id] = true
	if prev, touched := c.objects[id]; touched {
		if prev != nil {
			return inconsistentf(i, "对象 %q 被重复创建", id)
		}
	} else {
		c.assume(assumption{changeIndex: i, kind: assumeObjectAbsent, id: id})
	}
	if f := c.requireObjectTypeUsable(i, ch.ObjectAfter.Type); f != nil {
		return f
	}
	c.objects[id] = cloneObject(ch.ObjectAfter)
	return nil
}

func (c *selfChecker) updateObject(i int, ch *Change) *failure {
	if !validObjectInst(ch.ObjectBefore) || !validObjectInst(ch.ObjectAfter) ||
		ch.ObjectBefore.ID != ch.ObjectAfter.ID || ch.ObjectBefore.Type != ch.ObjectAfter.Type {
		return inconsistentf(i, "UpdateObject 需要 ID 与类型一致的合法变化前/后实例")
	}
	id := ch.ObjectAfter.ID
	c.noteObjInstance(ch.ObjectAfter.Type, i)
	c.model.touchedObjects[id] = true
	if prev, touched := c.objects[id]; touched {
		if prev == nil {
			return inconsistentf(i, "对象 %q 在差异记录自身投影中不存在，无法更新", id)
		}
		if !equalObject(prev, ch.ObjectBefore) {
			return inconsistentf(i, "UpdateObject 声明的变化前状态与差异记录自身推出的对象 %q 状态不一致", id)
		}
	} else {
		c.assume(assumption{changeIndex: i, kind: assumeObjectEqual, id: id, object: ch.ObjectBefore})
	}
	if f := c.requireObjectTypeUsable(i, ch.ObjectAfter.Type); f != nil {
		return f
	}
	c.objects[id] = cloneObject(ch.ObjectAfter)
	return nil
}

func (c *selfChecker) deleteObject(i int, ch *Change) *failure {
	if !validObjectInst(ch.ObjectBefore) || ch.ObjectAfter != nil {
		return inconsistentf(i, "DeleteObject 需要合法的变化前实例且不得声明变化后状态")
	}
	id := ch.ObjectBefore.ID
	c.noteObjInstance(ch.ObjectBefore.Type, i)
	c.model.touchedObjects[id] = true
	if prev, touched := c.objects[id]; touched {
		if prev == nil {
			return inconsistentf(i, "对象 %q 在差异记录自身投影中不存在，无法删除", id)
		}
		if !equalObject(prev, ch.ObjectBefore) {
			return inconsistentf(i, "DeleteObject 声明的变化前状态与差异记录自身推出的对象 %q 状态不一致", id)
		}
	} else {
		c.assume(assumption{changeIndex: i, kind: assumeObjectEqual, id: id, object: ch.ObjectBefore})
	}
	c.objects[id] = nil
	return nil
}

func (c *selfChecker) createLink(i int, ch *Change) *failure {
	if !validLinkInst(ch.LinkAfter) || ch.LinkBefore != nil {
		return inconsistentf(i, "CreateLink 需要合法的变化后实例且不得声明变化前状态")
	}
	id := ch.LinkAfter.ID
	c.noteLinkInstance(ch.LinkAfter.Type, i)
	c.model.touchedLinks[id] = true
	if prev, touched := c.links[id]; touched {
		if prev != nil {
			return inconsistentf(i, "链接 %q 被重复创建", id)
		}
	} else {
		c.assume(assumption{changeIndex: i, kind: assumeLinkAbsent, id: id})
	}
	if f := c.requireLinkTypeUsable(i, ch.LinkAfter.Type); f != nil {
		return f
	}
	for _, endpoint := range [2]string{ch.LinkAfter.Source, ch.LinkAfter.Target} {
		if prev, touched := c.objects[endpoint]; touched {
			if prev == nil {
				return inconsistentf(i, "链接 %q 的端点对象 %q 在差异记录自身投影中不存在", id, endpoint)
			}
		} else {
			c.assume(assumption{changeIndex: i, kind: assumeObjectExists, id: endpoint})
		}
	}
	c.links[id] = cloneLink(ch.LinkAfter)
	return nil
}

func (c *selfChecker) deleteLink(i int, ch *Change) *failure {
	if !validLinkInst(ch.LinkBefore) || ch.LinkAfter != nil {
		return inconsistentf(i, "DeleteLink 需要合法的变化前实例且不得声明变化后状态")
	}
	id := ch.LinkBefore.ID
	c.noteLinkInstance(ch.LinkBefore.Type, i)
	c.model.touchedLinks[id] = true
	if prev, touched := c.links[id]; touched {
		if prev == nil {
			return inconsistentf(i, "链接 %q 在差异记录自身投影中不存在，无法删除", id)
		}
		if !equalLink(prev, ch.LinkBefore) {
			return inconsistentf(i, "DeleteLink 声明的变化前状态与差异记录自身推出的链接 %q 状态不一致", id)
		}
	} else {
		c.assume(assumption{changeIndex: i, kind: assumeLinkEqual, id: id, link: ch.LinkBefore})
	}
	c.links[id] = nil
	return nil
}

// checkOrdering 核对结构层变化与相关实例层变化的相对顺序：
// 对同一类型名，结构层变化必须先于所有相关实例层变化。
// 差异记录顺序不符时拒绝，绝不自行调整顺序代为纠正。
func (c *selfChecker) checkOrdering() *failure {
	for name, firstInst := range c.objFirstInstance {
		if lastSchema, ok := c.objLastSchema[name]; ok && firstInst < lastSchema {
			return inconsistentf(lastSchema,
				"对象类型 %q 的结构层变化（第 %d 条）出现在相关实例层变化（第 %d 条）之后",
				name, lastSchema, firstInst)
		}
	}
	for name, firstInst := range c.linkFirstInstance {
		if lastSchema, ok := c.linkLastSchema[name]; ok && firstInst < lastSchema {
			return inconsistentf(lastSchema,
				"链接类型 %q 的结构层变化（第 %d 条）出现在相关实例层变化（第 %d 条）之后",
				name, lastSchema, firstInst)
		}
	}
	return nil
}

// checkTargetCompleteness 核对声明目标与被触及投影的完备对应：
// 每个被触及的类型 / 实体都必须有终态声明（nil 表示终态不存在），
// 且声明目标不得包含差异记录未触及的条目。
func (c *selfChecker) checkTargetCompleteness() *failure {
	t := c.model.target
	if f := checkTouchedVsTarget("对象类型", c.model.touchedObjectTypes, t.ObjectTypes); f != nil {
		return f
	}
	if f := checkTouchedVsTarget("链接类型", c.model.touchedLinkTypes, t.LinkTypes); f != nil {
		return f
	}
	if f := checkTouchedVsTarget("对象", c.model.touchedObjects, t.Objects); f != nil {
		return f
	}
	if f := checkTouchedVsTarget("链接", c.model.touchedLinks, t.Links); f != nil {
		return f
	}
	for name, ot := range t.ObjectTypes {
		if ot != nil && ot.Name != name {
			return inconsistentf(-1, "声明目标中对象类型条目键 %q 与定义名 %q 不一致", name, ot.Name)
		}
	}
	for name, lt := range t.LinkTypes {
		if lt != nil && lt.Name != name {
			return inconsistentf(-1, "声明目标中链接类型条目键 %q 与定义名 %q 不一致", name, lt.Name)
		}
	}
	for id, o := range t.Objects {
		if o != nil && o.ID != id {
			return inconsistentf(-1, "声明目标中对象条目键 %q 与实例 ID %q 不一致", id, o.ID)
		}
	}
	for id, l := range t.Links {
		if l != nil && l.ID != id {
			return inconsistentf(-1, "声明目标中链接条目键 %q 与实例 ID %q 不一致", id, l.ID)
		}
	}
	return nil
}

func checkTouchedVsTarget[T any](what string, touched map[string]bool, target map[string]*T) *failure {
	for name := range touched {
		if _, ok := target[name]; !ok {
			return inconsistentf(-1, "声明目标缺少对被触及%s %q 的终态声明", what, name)
		}
	}
	for name := range target {
		if !touched[name] {
			return inconsistentf(-1, "声明目标包含差异记录未触及的%s %q", what, name)
		}
	}
	return nil
}
