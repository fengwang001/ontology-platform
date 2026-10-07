package ontology

// naiveOverride 朴素模型自行保存的原始声明（不与生产结构共享字段布局以外的任何逻辑）。
type naiveOverride struct {
	instance string
	attr     string
	kind     SubjectKind
	subject  string
	vis      Visibility
	declared int // 成功声明在命令序列中的次序（含全部成功命令），越大越新
}

// NaiveModel 是独立的朴素对照模型：全量保存链接与权限声明，
// 每次判定都重新遍历整张角色层级图。
type NaiveModel struct {
	objectAttrs map[string]map[string]Visibility
	instanceT   map[string]string
	linkTypes   map[string]LinkTypeSpec

	roles       map[string]bool
	parents     map[string]map[string]bool
	memberships map[string]map[string]bool

	overrides []naiveOverride
	links     []Link

	ticks int
}

// NewNaiveModel 创建朴素模型。
func NewNaiveModel() *NaiveModel {
	return &NaiveModel{
		objectAttrs: map[string]map[string]Visibility{},
		instanceT:   map[string]string{},
		linkTypes:   map[string]LinkTypeSpec{},
		roles:       map[string]bool{},
		parents:     map[string]map[string]bool{},
		memberships: map[string]map[string]bool{},
	}
}

// NaiveResult 是朴素模型对一条命令的判定，字段与生产结果可直接比对。
type NaiveResult struct {
	OK              bool
	Code            ErrorCode // 成功时为空串
	PhysicalLinkIDs []string
	VisibleLinkIDs  []string
	SrcVis          Visibility
	TgtVis          Visibility
	HiddenButExists bool
}

// Apply 让朴素模型消费同一条命令；判定逻辑独立编写，每次可见性都全量重算。
func (n *NaiveModel) Apply(cmd Command) NaiveResult {
	switch c := cmd.(type) {
	case RegisterObjectType:
		if _, dup := n.objectAttrs[c.Name]; c.Name == "" || dup {
			return n.fail(ErrInvalidParam)
		}
		m := map[string]Visibility{}
		for _, a := range c.Attrs {
			if a.Name == "" {
				return n.fail(ErrInvalidParam)
			}
			if _, dup := m[a.Name]; dup {
				return n.fail(ErrInvalidParam)
			}
			m[a.Name] = a.Default
		}
		n.objectAttrs[c.Name] = m
	case SetTypeDefault:
		attrs, ok := n.objectAttrs[c.TypeName]
		if !ok {
			return n.fail(ErrInvalidParam)
		}
		if _, ok := attrs[c.Attr]; !ok {
			return n.fail(ErrInvalidParam)
		}
		attrs[c.Attr] = c.Vis
	case RegisterLinkType:
		s := c.Spec
		if s.Name == "" {
			return n.fail(ErrInvalidParam)
		}
		if _, dup := n.linkTypes[s.Name]; dup {
			return n.fail(ErrInvalidParam)
		}
		if _, ok := n.objectAttrs[s.SrcType]; !ok {
			return n.fail(ErrInvalidParam)
		}
		if _, ok := n.objectAttrs[s.TgtType]; !ok {
			return n.fail(ErrInvalidParam)
		}
		if _, ok := n.objectAttrs[s.SrcType][s.SrcAttr]; !ok {
			return n.fail(ErrInvalidParam)
		}
		if _, ok := n.objectAttrs[s.TgtType][s.TgtAttr]; !ok {
			return n.fail(ErrInvalidParam)
		}
		n.linkTypes[s.Name] = s
	case CreateInstance:
		if c.ID == "" {
			return n.fail(ErrInvalidParam)
		}
		if _, ok := n.objectAttrs[c.TypeName]; !ok {
			return n.fail(ErrInvalidParam)
		}
		if _, dup := n.instanceT[c.ID]; dup {
			return n.fail(ErrInvalidParam)
		}
		n.instanceT[c.ID] = c.TypeName
	case AddRole:
		if c.Role == "" || n.roles[c.Role] {
			return n.fail(ErrInvalidParam)
		}
		n.roles[c.Role] = true
	case IncludeRole:
		if c.Child == "" || c.Parent == "" || c.Child == c.Parent ||
			!n.roles[c.Child] || !n.roles[c.Parent] ||
			n.parents[c.Child][c.Parent] {
			return n.fail(ErrInvalidParam)
		}
		if n.parents[c.Child] == nil {
			n.parents[c.Child] = map[string]bool{}
		}
		n.parents[c.Child][c.Parent] = true
	case AssignRole:
		if c.Operator == "" || !n.roles[c.Role] || n.memberships[c.Operator][c.Role] {
			return n.fail(ErrInvalidParam)
		}
		if n.memberships[c.Operator] == nil {
			n.memberships[c.Operator] = map[string]bool{}
		}
		n.memberships[c.Operator][c.Role] = true
	case DeclareOverride:
		t, ok := n.instanceT[c.InstanceID]
		if !ok || c.Attr == "" || c.Subject == "" {
			return n.fail(ErrInvalidParam)
		}
		if _, ok := n.objectAttrs[t][c.Attr]; !ok {
			return n.fail(ErrInvalidParam)
		}
		if c.Kind == SubjectRole && !n.roles[c.Subject] {
			return n.fail(ErrInvalidParam)
		}
		n.ticks++
		n.overrides = append(n.overrides, naiveOverride{
			instance: c.InstanceID, attr: c.Attr, kind: c.Kind,
			subject: c.Subject, vis: c.Vis, declared: n.ticks,
		})
	case CreateLink:
		return n.applyCreate(c)
	case DeleteLink:
		return n.applyDelete(c)
	default:
		return n.fail(ErrInvalidParam)
	}
	n.ticks++
	return n.snapshotFor("")
}

func (n *NaiveModel) fail(code ErrorCode) NaiveResult {
	return NaiveResult{OK: false, Code: code, PhysicalLinkIDs: n.physicalIDs()}
}

func (n *NaiveModel) applyCreate(c CreateLink) NaiveResult {
	spec, ok := n.linkTypes[c.TypeName]
	srcType, srcOK := n.instanceT[c.SrcID]
	tgtType, tgtOK := n.instanceT[c.TgtID]
	dupID := n.findLink(c.ID) >= 0
	if c.ID == "" || c.Operator == "" || !ok || !srcOK || !tgtOK ||
		srcType != spec.SrcType || tgtType != spec.TgtType || dupID {
		return n.fail(ErrInvalidParam)
	}

	srcVis := n.visibility(c.Operator, c.SrcID, spec.SrcAttr)
	tgtVis := n.visibility(c.Operator, c.TgtID, spec.TgtAttr)
	res := n.snapshotFor("")
	res.SrcVis, res.TgtVis = srcVis, tgtVis
	if srcVis == Invisible || tgtVis == Invisible {
		res.OK, res.Code = false, ErrInvisible
		return res
	}

	srcUse, tgtUse := n.usage(spec.Name, c.SrcID, c.TgtID)
	srcOK2 := spec.SrcMax <= 0 || srcUse < spec.SrcMax
	tgtOK2 := spec.TgtMax <= 0 || tgtUse < spec.TgtMax
	if !srcOK2 {
		res.OK, res.Code = false, ErrSrcCardinality
		return res
	}
	if !tgtOK2 {
		res.OK, res.Code = false, ErrTgtCardinality
		return res
	}

	n.ticks++
	n.links = append(n.links, Link{
		ID: c.ID, TypeName: c.TypeName, SrcID: c.SrcID, TgtID: c.TgtID,
		CreatedSeq: n.ticks,
	})
	return n.snapshotFor("")
}

func (n *NaiveModel) applyDelete(c DeleteLink) NaiveResult {
	idx := n.findLink(c.ID)
	if c.ID == "" || c.Operator == "" || idx < 0 {
		return n.fail(ErrNotFound)
	}
	lk := n.links[idx]
	spec := n.linkTypes[lk.TypeName]
	srcVis := n.visibility(c.Operator, lk.SrcID, spec.SrcAttr)
	tgtVis := n.visibility(c.Operator, lk.TgtID, spec.TgtAttr)
	res := n.snapshotFor("")
	res.SrcVis, res.TgtVis = srcVis, tgtVis
	if srcVis == Invisible || tgtVis == Invisible {
		// 与“确实不存在”同一类别；物理链接保留，基数不释放。
		res.OK, res.Code, res.HiddenButExists = false, ErrNotFound, true
		return res
	}
	n.links = append(n.links[:idx], n.links[idx+1:]...)
	n.ticks++
	return n.snapshotFor("")
}

func (n *NaiveModel) findLink(id string) int {
	for i, lk := range n.links {
		if lk.ID == id {
			return i
		}
	}
	return -1
}

func (n *NaiveModel) usage(typeName, srcID, tgtID string) (int, int) {
	var srcUse, tgtUse int
	for _, lk := range n.links {
		if lk.TypeName != typeName {
			continue
		}
		if lk.SrcID == srcID {
			srcUse++
		}
		if lk.TgtID == tgtID {
			tgtUse++
		}
	}
	return srcUse, tgtUse
}

// visibility 朴素可见性：每次都从操作者出发对整张角色层级重新全量 BFS，
// 枚举全部可达主体的全部覆盖，再在其中取距离最近、同距离声明最新者。
func (n *NaiveModel) visibility(operator, instance, attr string) Visibility {
	type node struct {
		id   string
		role bool
		dist int
	}
	dist := map[string]int{operator: 0}
	queue := []node{{id: operator, dist: 0}}
	for r := range n.memberships[operator] {
		if _, seen := dist[r]; !seen {
			dist[r] = 1
			queue = append(queue, node{id: r, role: true, dist: 1})
		}
	}
	for head := 0; head < len(queue); head++ {
		cur := queue[head]
		if !cur.role {
			continue
		}
		for p := range n.parents[cur.id] {
			if _, seen := dist[p]; !seen {
				dist[p] = cur.dist + 1
				queue = append(queue, node{id: p, role: true, dist: cur.dist + 1})
			}
		}
	}

	var (
		bestDist = 1 << 30
		bestTime = -1
		bestVis  Visibility
		found    bool
	)
	consider := func(subject string) {
		d, reachable := dist[subject]
		for i := range n.overrides {
			ov := n.overrides[i]
			if ov.instance != instance || ov.attr != attr || ov.subject != subject {
				continue
			}
			if !reachable {
				continue
			}
			if !found || d < bestDist || (d == bestDist && ov.declared > bestTime) {
				bestDist, bestTime, bestVis, found = d, ov.declared, ov.vis, true
			}
		}
	}
	consider(operator)
	for r := range n.roles {
		consider(r)
	}
	if found {
		return bestVis
	}
	if t, ok := n.instanceT[instance]; ok {
		if v, ok := n.objectAttrs[t][attr]; ok {
			return v
		}
	}
	return Visible
}

func (n *NaiveModel) physicalIDs() []string {
	ids := make([]string, 0, len(n.links))
	for _, lk := range n.links {
		ids = append(ids, lk.ID)
	}
	sortStrings(ids)
	return ids
}

// VisibleIDs 供测试按操作者查询朴素模型的可见链接集合。
func (n *NaiveModel) VisibleIDs(operator string) []string {
	var ids []string
	for _, lk := range n.links {
		spec := n.linkTypes[lk.TypeName]
		if n.visibility(operator, lk.SrcID, spec.SrcAttr) == Visible &&
			n.visibility(operator, lk.TgtID, spec.TgtAttr) == Visible {
			ids = append(ids, lk.ID)
		}
	}
	sortStrings(ids)
	return ids
}

func (n *NaiveModel) snapshotFor(_ string) NaiveResult {
	return NaiveResult{OK: true, PhysicalLinkIDs: n.physicalIDs()}
}

// PhysicalIDs 返回朴素模型的物理链接 ID 集合。
func (n *NaiveModel) PhysicalIDs() []string { return n.physicalIDs() }

func sortStrings(s []string) {
	for i := 1; i < len(s); i++ {
		for j := i; j > 0 && s[j-1] > s[j]; j-- {
			s[j-1], s[j] = s[j], s[j-1]
		}
	}
}
