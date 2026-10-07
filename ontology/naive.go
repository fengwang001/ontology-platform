package ontology

// naiveModel 是与 Gateway 独立实现的朴素参照模型：
// 不使用任何物化视图，每次判定都从不可变历史（版本链 + 权限流水）
// 全量重放计算结果。它的价值在于实现路径刻意不同——若优化实现与
// 朴素重放在大量随机操作序列上结果一致，则可交叉证明优化正确性。
type naiveModel struct {
	store *Store
}

func newNaiveModel(g *Gateway) *naiveModel {
	return &naiveModel{store: g.store}
}

// naiveDecision 是朴素模型对一次读/写判定的可比较结论。
type naiveDecision struct {
	errKind      ErrorKind
	writeApplied bool
	written      []string // attrID
	ignored      []string // attrID
	denyKinds    map[string]ErrorKind
	partial      bool
	visible      []string // attrID（读投影存活字段）
	removed      []string
}

// resolve 全量扫描版本历史，重建指定版本的属性集合（含标识符与名字）。
func (m *naiveModel) snapshotAt(typeID string, version int) (map[string]*Attribute, map[string]*Attribute, bool) {
	var tv *TypeVersion
	for _, v := range m.store.versionHistory[typeID] {
		if v.Version == version {
			tv = v
			break
		}
	}
	if tv == nil {
		return nil, nil, false
	}
	byID := map[string]*Attribute{}
	byName := map[string]*Attribute{}
	for i := range tv.Attrs {
		byID[tv.Attrs[i].ID] = &tv.Attrs[i]
		byName[tv.Attrs[i].Name] = &tv.Attrs[i]
	}
	return byID, byName, true
}

// allowed 全量扫描权限流水重放授权并集与吊销并集（吊销优先）。
func (m *naiveModel) allowed(typeID, subject, attrID string, op Op, atVersion int) bool {
	grant, revoke := false, false
	for _, e := range m.store.permLog {
		if e.TypeID != typeID || e.Subject != subject || e.AttrID != attrID || e.Op != op {
			continue
		}
		if !e.Covers(atVersion) {
			continue
		}
		if e.Grant {
			grant = true
		} else {
			revoke = true
		}
	}
	return grant && !revoke
}

// decideWrite 朴素重放一次写判定。调用方须已与网关相同的串行锁语义一致
// （测试中在同一 goroutine 顺序调用，故无需加锁）。
func (m *naiveModel) decideWrite(req WriteRequest, exists bool, action string) naiveDecision {
	if action == "write" && !exists {
		return naiveDecision{errKind: ErrObjectNotFound}
	}
	byID, byName, ok := m.snapshotAt(req.TypeID, req.SchemaVer)
	if !ok {
		// 版本本身不存在：若类型存在，则必然是过期/未来版本。
		if t := m.store.getType(req.TypeID); t != nil && req.SchemaVer != t.current.Version {
			return naiveDecision{errKind: ErrVersionExpired}
		}
		return naiveDecision{errKind: ErrTypeNotFound}
	}
	t := m.store.getType(req.TypeID)
	if t.current.Version != req.SchemaVer {
		return naiveDecision{errKind: ErrVersionExpired}
	}

	var refs []refAttr
	for ref := range req.Fields {
		a := byID[ref]
		if a == nil {
			a = byName[ref]
		}
		if a == nil {
			return naiveDecision{errKind: ErrAttrNotFound}
		}
		refs = append(refs, refAttr{ref: ref, a: a})
	}
	sortRefAttrs(refs)

	denyKinds := map[string]ErrorKind{}
	var denyOrder []string
	for _, ra := range refs {
		okW := m.allowed(req.TypeID, req.Subject, ra.a.ID, OpWrite, req.SchemaVer) ||
			m.allowed(req.TypeID, req.Subject, "*", OpWrite, req.SchemaVer)
		if !okW {
			kind := ErrPermissionDenied
			// 与网关一致：存在覆盖该版本的授权（并集意义上）且被吊销覆盖 => 吊销。
			if m.coversGrant(req.TypeID, req.Subject, ra.a.ID, OpWrite, req.SchemaVer) &&
				m.revoked(req.TypeID, req.Subject, ra.a.ID, OpWrite, req.SchemaVer) {
				kind = ErrRevoked
			}
			denyKinds[ra.ref] = kind
			denyOrder = append(denyOrder, ra.ref)
		}
	}
	if len(denyOrder) > 0 && t.writePolicy == PolicyRejectWhole {
		return naiveDecision{writeApplied: false, denyKinds: denyKinds}
	}
	d := naiveDecision{writeApplied: true, denyKinds: denyKinds}
	for _, ra := range refs {
		if _, denied := denyKinds[ra.ref]; denied {
			d.ignored = append(d.ignored, ra.a.ID)
		} else {
			d.written = append(d.written, ra.a.ID)
		}
	}
	return d
}

// decideRead 朴素重放一次读判定。
func (m *naiveModel) decideRead(objectID, typeID, subject string) naiveDecision {
	obj := m.store.getObject(objectID)
	if obj == nil {
		return naiveDecision{errKind: ErrObjectNotFound}
	}
	t := m.store.getType(typeID)
	if t == nil {
		return naiveDecision{errKind: ErrTypeNotFound}
	}
	latest := t.current.Version
	byID, _, _ := m.snapshotAt(typeID, latest)
	typeAllowed := m.allowed(typeID, subject, "*", OpRead, latest)
	if !typeAllowed {
		anyAttr := false
		for id := range byID {
			if m.allowed(typeID, subject, id, OpRead, latest) {
				anyAttr = true
				break
			}
		}
		if !anyAttr {
			return naiveDecision{errKind: ErrTypeNoPermission}
		}
	}
	d := naiveDecision{}
	for id := range obj.Fields {
		alive := byID[id] != nil
		canRead := alive && (typeAllowed || m.allowed(typeID, subject, id, OpRead, latest))
		if canRead {
			d.visible = append(d.visible, id)
		} else {
			d.removed = append(d.removed, id)
		}
	}
	sortStrings(d.visible)
	sortStrings(d.removed)
	d.partial = len(d.removed) > 0
	return d
}

func (m *naiveModel) revoked(typeID, subject, attrID string, op Op, at int) bool {
	for _, e := range m.store.permLog {
		if e.TypeID != typeID || e.Subject != subject || e.AttrID != attrID || e.Op != op {
			continue
		}
		if !e.Grant && e.Covers(at) {
			return true
		}
	}
	return false
}

// coversGrant 报告是否存在任一覆盖版本 at 的授权条目（不考虑吊销）。
func (m *naiveModel) coversGrant(typeID, subject, attrID string, op Op, at int) bool {
	for _, e := range m.store.permLog {
		if e.TypeID != typeID || e.Subject != subject || e.AttrID != attrID || e.Op != op {
			continue
		}
		if e.Grant && e.Covers(at) {
			return true
		}
	}
	return false
}

type refAttr struct {
	ref string
	a   *Attribute
}

func sortRefAttrs(xs []refAttr) {
	for i := 1; i < len(xs); i++ {
		for j := i; j > 0 && xs[j-1].ref > xs[j].ref; j-- {
			xs[j-1], xs[j] = xs[j], xs[j-1]
		}
	}
}
