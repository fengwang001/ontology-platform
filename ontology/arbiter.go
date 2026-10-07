package ontology

import "sort"

// Applied 记录一条命令在全局串行序列中的位置，用于确定性重放与测试打印。
type Applied struct {
	Seq int
	Cmd Command
}

// CreateEvidence 是创建仲裁的完整依据：四步判定各自的中间结论。
type CreateEvidence struct {
	Spec     LinkTypeSpec
	SrcBasis VisBasis
	TgtBasis VisBasis
	Card     CardVerdict
}

// CreateResult 是创建链接的结果；成功时 Link 非空，否则 Err 非空。
type CreateResult struct {
	Link     *Link
	Err      *ArbError
	Evidence CreateEvidence
	Seq      int
}

// DeleteResult 是删除链接的结果。
type DeleteResult struct {
	Err *ArbError
	Seq int
	// HiddenButExists 仅供测试打印“据以判定的依据”：错误类别仍统一为 ErrNotFound。
	HiddenButExists bool
}

// Arbiter 是联合仲裁器：维护全局串行序号，协调账本与解析器，
// 按统一优先级产出归一化错误。
type Arbiter struct {
	cat      *catalog
	ledger   *Ledger
	resolver *Resolver
	log      []Applied
}

// NewArbiter 创建仲裁器。
func NewArbiter() *Arbiter {
	return &Arbiter{
		cat:      newCatalog(),
		ledger:   NewLedger(),
		resolver: NewResolver(),
	}
}

// Log 返回已成功应用的命令的确定性序列副本。
func (a *Arbiter) Log() []Applied {
	out := make([]Applied, len(a.log))
	copy(out, a.log)
	return out
}

func (a *Arbiter) nextSeq() int { return len(a.log) + 1 }

func (a *Arbiter) record(cmd Command) int {
	seq := a.nextSeq()
	a.log = append(a.log, Applied{Seq: seq, Cmd: cmd})
	return seq
}

// Apply 串行应用任意命令；管理类命令在参数非法时返回 ErrInvalidParam 且不留痕。
func (a *Arbiter) Apply(cmd Command) *ArbError {
	switch c := cmd.(type) {
	case RegisterObjectType:
		if !a.cat.registerObjectType(c.Name, c.Attrs) {
			return &ArbError{ErrInvalidParam, "object type invalid or duplicated"}
		}
	case SetTypeDefault:
		if !a.cat.setTypeDefault(c.TypeName, c.Attr, c.Vis) {
			return &ArbError{ErrInvalidParam, "unknown object type or attribute"}
		}
	case RegisterLinkType:
		if !a.cat.registerLinkType(c.Spec) {
			return &ArbError{ErrInvalidParam, "link type invalid or duplicated"}
		}
	case CreateInstance:
		if !a.cat.createInstance(c.ID, c.TypeName) {
			return &ArbError{ErrInvalidParam, "instance invalid or duplicated"}
		}
	case AddRole:
		if !a.resolver.addRole(c.Role) {
			return &ArbError{ErrInvalidParam, "role invalid or duplicated"}
		}
	case IncludeRole:
		if !a.resolver.includeRole(c.Child, c.Parent) {
			return &ArbError{ErrInvalidParam, "role inclusion invalid or duplicated"}
		}
	case AssignRole:
		if !a.resolver.assignRole(c.Operator, c.Role) {
			return &ArbError{ErrInvalidParam, "role assignment invalid or duplicated"}
		}
	case DeclareOverride:
		if !a.attrKnown(c.InstanceID, c.Attr) {
			return &ArbError{ErrInvalidParam, "override target unknown"}
		}
		seq := a.nextSeq()
		if !a.resolver.declareOverride(c.InstanceID, c.Attr, c.Kind, c.Subject, c.Vis, seq) {
			return &ArbError{ErrInvalidParam, "override declaration invalid"}
		}
		a.record(c)
		return nil
	case CreateLink:
		r := a.CreateLink(c)
		return r.Err
	case DeleteLink:
		r := a.DeleteLink(c)
		return r.Err
	default:
		return &ArbError{ErrInvalidParam, "unknown command"}
	}
	a.record(cmd)
	return nil
}

func (a *Arbiter) attrKnown(instanceID, attr string) bool {
	typeName, ok := a.cat.instanceType[instanceID]
	if !ok {
		return false
	}
	attrs, ok := a.cat.objectAttrs[typeName]
	if !ok {
		return false
	}
	_, ok = attrs[attr]
	return ok
}

func (a *Arbiter) vis(operator, instanceID, attr string) VisBasis {
	return a.resolver.resolve(operator, instanceID, attr, a.cat.typeDefault(instanceID, attr))
}

// CreateLink 执行创建联合仲裁。拒绝次序严格为：
// 参数非法 → 任一端来源属性不可见 → 起点基数超限 → 终点基数超限；只报第一个命中。
func (a *Arbiter) CreateLink(req CreateLink) CreateResult {
	res := CreateResult{}
	spec, specOK := a.cat.linkTypes[req.TypeName]
	res.Evidence.Spec = spec

	// 1) 参数非法：链接类型/两端实例存在、实例类型与链接类型一致、ID 非空且未占用。
	srcType, srcExists := a.cat.instanceType[req.SrcID]
	tgtType, tgtExists := a.cat.instanceType[req.TgtID]
	paramOK := req.ID != "" && req.Operator != "" && specOK &&
		srcExists && tgtExists &&
		srcType == spec.SrcType && tgtType == spec.TgtType &&
		!a.ledger.exists(req.ID)
	if !paramOK {
		res.Err = &ArbError{ErrInvalidParam, "create-link parameters invalid"}
		return res
	}

	// 2) 权限：两端来源属性均需可见。起点先于终点被报告，但二者同属“权限类”。
	srcBasis := a.vis(req.Operator, req.SrcID, spec.SrcAttr)
	tgtBasis := a.vis(req.Operator, req.TgtID, spec.TgtAttr)
	res.Evidence.SrcBasis = srcBasis
	res.Evidence.TgtBasis = tgtBasis
	if srcBasis.Vis == Invisible || tgtBasis.Vis == Invisible {
		res.Err = &ArbError{ErrInvisible, "operator lacks visibility on a source attribute"}
		return res
	}

	// 3)/4) 基数：账本独立判定，起点优先于终点报告。
	card := a.ledger.check(spec, req.SrcID, req.TgtID)
	res.Evidence.Card = card
	if !card.SrcOK {
		res.Err = &ArbError{ErrSrcCardinality, "source-side cardinality exceeded"}
		return res
	}
	if !card.TgtOK {
		res.Err = &ArbError{ErrTgtCardinality, "target-side cardinality exceeded"}
		return res
	}

	seq := a.nextSeq()
	lk := &Link{ID: req.ID, TypeName: req.TypeName, SrcID: req.SrcID, TgtID: req.TgtID, CreatedSeq: seq}
	a.ledger.add(lk)
	a.record(req)
	res.Link = lk
	res.Seq = seq
	return res
}

// DeleteLink 执行删除仲裁。删除不做基数判断，拒绝次序简化为：
// 参数非法 → （不可见 | 不存在）合并为同一 ErrNotFound 类别。
// 为避免借错误文案泄露链接存在，两种情况返回完全相同的错误对象。
func (a *Arbiter) DeleteLink(req DeleteLink) DeleteResult {
	res := DeleteResult{}
	lk, exists := a.ledger.get(req.ID)
	if req.ID == "" || req.Operator == "" || !exists {
		res.Err = notFoundErr()
		return res
	}
	spec := a.cat.linkTypes[lk.TypeName]
	srcBasis := a.vis(req.Operator, lk.SrcID, spec.SrcAttr)
	tgtBasis := a.vis(req.Operator, lk.TgtID, spec.TgtAttr)
	if srcBasis.Vis == Invisible || tgtBasis.Vis == Invisible {
		// 物理链接保持存在，不释放基数；对外与“不存在”完全同类别同文案。
		res.Err = notFoundErr()
		res.HiddenButExists = true
		return res
	}

	seq := a.nextSeq()
	a.ledger.remove(lk.ID)
	a.record(req)
	res.Seq = seq
	return res
}

func notFoundErr() *ArbError {
	return &ArbError{ErrNotFound, "link not found"}
}

// VisibleLinks 枚举操作者当前可见的全部链接：两端来源属性都可见才可见。
// 该读操作是纯函数式快照，不改变链接、基数与其他操作者的可见性。
func (a *Arbiter) VisibleLinks(operator string) []Link {
	var out []Link
	for _, id := range a.ledger.ordered {
		lk := a.ledger.links[id]
		spec := a.cat.linkTypes[lk.TypeName]
		sb := a.vis(operator, lk.SrcID, spec.SrcAttr)
		tb := a.vis(operator, lk.TgtID, spec.TgtAttr)
		if sb.Vis == Visible && tb.Vis == Visible {
			out = append(out, *lk)
		}
	}
	sortLinks(out)
	return out
}

// PhysicalLinks 返回物理存在的全部链接（无视权限），仅供测试与重放比对。
func (a *Arbiter) PhysicalLinks() []Link { return a.ledger.snapshot() }

// Visibility 暴露某次最终可见性判定及其依据，供测试打印。
func (a *Arbiter) Visibility(operator, instanceID, attr string) VisBasis {
	return a.vis(operator, instanceID, attr)
}

// Replay 用同一命令序列重建一个全新仲裁器；成功即代表结果可确定性复现。
func Replay(cmds []Command) *Arbiter {
	a := NewArbiter()
	for _, cmd := range cmds {
		a.Apply(cmd)
	}
	return a
}

// SortedIDs 是供外部测试使用的确定性 ID 排序辅助。
func SortedIDs(links []Link) []string {
	ids := make([]string, len(links))
	for i, lk := range links {
		ids[i] = lk.ID
	}
	sort.Strings(ids)
	return ids
}
