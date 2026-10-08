package replay

import "fmt"

// assumption 为差异记录对良好快照的一条依赖声明，
// 由阶段一收集、阶段二逐条与快照核对。
type assumption struct {
	changeIndex int
	kind        assumptionKind
	// 期望快照中存在且相等的取值（按 kind 取用）。
	objectType *ObjectType
	linkType   *LinkType
	object     *ObjectInstance
	link       *LinkInstance
	constraint *Constraint
	propType   string
	typeName   string
	property   string
	id         string
}

type assumptionKind int

const (
	assumeObjectTypeAbsent assumptionKind = iota
	assumeObjectTypeEqual
	assumeObjectTypeExists
	assumePropertyAbsent
	assumePropertyEqual
	assumeLinkTypeAbsent
	assumeLinkTypeEqual
	assumeLinkTypeExists
	assumeConstraintEqual
	assumeObjectAbsent
	assumeObjectEqual
	assumeObjectExists
	assumeLinkAbsent
	assumeLinkEqual
)

// snapshotView 为快照的只读计数视图：校验对快照的一切访问
// 都经过它，以便用 Stats 复核校验开销与快照规模无关。
type snapshotView struct {
	snap *Snapshot
	st   Stats
}

func (v *snapshotView) stats() Stats { return v.st }

func (v *snapshotView) objectType(name string) (ObjectType, bool) {
	v.st.SchemaReads++
	t, ok := v.snap.Schema.ObjectTypes[name]
	return t, ok
}

func (v *snapshotView) linkType(name string) (LinkType, bool) {
	v.st.SchemaReads++
	t, ok := v.snap.Schema.LinkTypes[name]
	return t, ok
}

func (v *snapshotView) object(id string) (ObjectInstance, bool) {
	v.st.ObjectReads++
	o, ok := v.snap.Objects[id]
	return o, ok
}

func (v *snapshotView) link(id string) (LinkInstance, bool) {
	v.st.LinkReads++
	l, ok := v.snap.Links[id]
	return l, ok
}

// linkOutCount / linkInCount 查询构造快照时建立的索引，计一次读取。
func (v *snapshotView) linkOutCount(linkType, source string) int {
	v.st.LinkReads++
	return v.snap.outCount[[2]string{linkType, source}]
}

func (v *snapshotView) linkInCount(linkType, target string) int {
	v.st.LinkReads++
	return v.snap.inCount[[2]string{linkType, target}]
}

func unmetf(idx int, format string, args ...any) *failure {
	return &failure{
		location:    NoLocation,
		changeIndex: idx,
		reason:      "重放前提环境（良好快照）不满足：" + fmt.Sprintf(format, args...),
	}
}

// checkPreconditions 阶段二：逐条核对差异记录对快照的依赖。
// 对快照只读；任一依赖不满足即判定前提环境不满足。
func checkPreconditions(view *snapshotView, self *selfModel) *failure {
	for _, a := range self.assumptions {
		if f := checkAssumption(view, a); f != nil {
			return f
		}
	}
	return nil
}

func checkAssumption(view *snapshotView, a assumption) *failure {
	switch a.kind {
	case assumeObjectTypeAbsent:
		if _, ok := view.objectType(a.typeName); ok {
			return unmetf(a.changeIndex, "快照中已存在对象类型 %q，与差异记录的新增声明冲突", a.typeName)
		}
	case assumeObjectTypeEqual:
		t, ok := view.objectType(a.typeName)
		if !ok {
			return unmetf(a.changeIndex, "快照中缺少差异记录所依赖的对象类型 %q", a.typeName)
		}
		if !equalObjectType(&t, a.objectType) {
			return unmetf(a.changeIndex, "快照中对象类型 %q 的定义与差异记录声明的变化前定义不一致", a.typeName)
		}
	case assumeObjectTypeExists:
		if _, ok := view.objectType(a.typeName); !ok {
			return unmetf(a.changeIndex, "快照中缺少差异记录所依赖的对象类型 %q", a.typeName)
		}
	case assumePropertyAbsent:
		t, ok := view.objectType(a.typeName)
		if !ok {
			return unmetf(a.changeIndex, "快照中缺少差异记录所依赖的对象类型 %q", a.typeName)
		}
		if _, ok := t.Properties[a.property]; ok {
			return unmetf(a.changeIndex, "快照中对象类型 %q 已存在属性 %q，与差异记录的新增声明冲突", a.typeName, a.property)
		}
	case assumePropertyEqual:
		t, ok := view.objectType(a.typeName)
		if !ok {
			return unmetf(a.changeIndex, "快照中缺少差异记录所依赖的对象类型 %q", a.typeName)
		}
		if cur, ok := t.Properties[a.property]; !ok || cur != a.propType {
			return unmetf(a.changeIndex, "快照中属性 %q.%q 与差异记录声明的变化前类型 %q 不一致", a.typeName, a.property, a.propType)
		}
	case assumeLinkTypeAbsent:
		if _, ok := view.linkType(a.typeName); ok {
			return unmetf(a.changeIndex, "快照中已存在链接类型 %q，与差异记录的新增声明冲突", a.typeName)
		}
	case assumeLinkTypeEqual:
		t, ok := view.linkType(a.typeName)
		if !ok {
			return unmetf(a.changeIndex, "快照中缺少差异记录所依赖的链接类型 %q", a.typeName)
		}
		if !equalLinkType(&t, a.linkType) {
			return unmetf(a.changeIndex, "快照中链接类型 %q 的定义与差异记录声明的变化前定义不一致", a.typeName)
		}
	case assumeLinkTypeExists:
		if _, ok := view.linkType(a.typeName); !ok {
			return unmetf(a.changeIndex, "快照中缺少差异记录所依赖的链接类型 %q", a.typeName)
		}
	case assumeConstraintEqual:
		t, ok := view.linkType(a.typeName)
		if !ok {
			return unmetf(a.changeIndex, "快照中缺少差异记录所依赖的链接类型 %q", a.typeName)
		}
		if t.Constraint != *a.constraint {
			return unmetf(a.changeIndex, "快照中链接类型 %q 的约束与差异记录声明的变化前约束不一致", a.typeName)
		}
	case assumeObjectAbsent:
		if _, ok := view.object(a.id); ok {
			return unmetf(a.changeIndex, "快照中已存在对象 %q，与差异记录的新增声明冲突", a.id)
		}
	case assumeObjectEqual:
		o, ok := view.object(a.id)
		if !ok {
			return unmetf(a.changeIndex, "快照中缺少差异记录所依赖的对象 %q", a.id)
		}
		if !equalObject(&o, a.object) {
			return unmetf(a.changeIndex, "快照中对象 %q 的取值与差异记录声明的变化前状态不一致", a.id)
		}
	case assumeObjectExists:
		if _, ok := view.object(a.id); !ok {
			return unmetf(a.changeIndex, "快照中缺少差异记录所依赖的对象 %q", a.id)
		}
	case assumeLinkAbsent:
		if _, ok := view.link(a.id); ok {
			return unmetf(a.changeIndex, "快照中已存在链接 %q，与差异记录的新增声明冲突", a.id)
		}
	case assumeLinkEqual:
		l, ok := view.link(a.id)
		if !ok {
			return unmetf(a.changeIndex, "快照中缺少差异记录所依赖的链接 %q", a.id)
		}
		if !equalLink(&l, a.link) {
			return unmetf(a.changeIndex, "快照中链接 %q 的取值与差异记录声明的变化前状态不一致", a.id)
		}
	}
	return nil
}
