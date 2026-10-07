package ontology

import (
	"fmt"
	"sync"
)

// Logger 接收每次判定的输入、输出与依据。
type Logger interface {
	Log(DecisionLogEntry)
}

// sliceLogger 是默认日志实现，线程安全，供测试与本地验证使用。
type sliceLogger struct {
	mu      sync.Mutex
	entries []DecisionLogEntry
}

func newSliceLogger() *sliceLogger { return &sliceLogger{} }

func (l *sliceLogger) Log(e DecisionLogEntry) {
	l.mu.Lock()
	l.entries = append(l.entries, e)
	l.mu.Unlock()
}

// Entries 返回日志快照。
func (l *sliceLogger) Entries() []DecisionLogEntry {
	l.mu.Lock()
	defer l.mu.Unlock()
	out := make([]DecisionLogEntry, len(l.entries))
	copy(out, l.entries)
	return out
}

// Gateway 是属性级权限随对象类型版本迁移联动的判定网关。
//
// 并发取舍：所有改变状态或读取状态的请求都在单一互斥锁下串行执行，
// 因此任一交错执行都严格等价于某个全局串行顺序（linearizable）；
// 任何判定结果只可能对应该串行顺序的某个确定前缀，不会出现
// 授权变更与版本演进交错产生的中间态误判。被拒绝操作在持锁段内
// 不做任何状态变更，天然满足“拒绝不改状态”。
type Gateway struct {
	mu     sync.Mutex
	store  *Store
	logger Logger
}

// NewGateway 构造空网关，自带判定日志。
func NewGateway() *Gateway {
	return &Gateway{store: newStore(), logger: newSliceLogger()}
}

// SetLogger 替换判定日志记录器。
func (g *Gateway) SetLogger(l Logger) {
	g.mu.Lock()
	g.logger = l
	g.mu.Unlock()
}

// DecisionLog 返回判定日志快照。
func (g *Gateway) DecisionLog() []DecisionLogEntry {
	g.mu.Lock()
	defer g.mu.Unlock()
	if sl, ok := g.logger.(*sliceLogger); ok {
		return sl.Entries()
	}
	return nil
}

func (g *Gateway) log(action, reason string, in, out map[string]any) {
	g.logger.Log(DecisionLogEntry{Action: action, Input: in, Output: out, Reason: reason})
}

func errOut(err error) map[string]any {
	if err == nil {
		return map[string]any{"ok": true}
	}
	return map[string]any{"ok": false, "error_kind": string(ErrorKindOf(err)), "error": err.Error()}
}

func permAction(grant bool) string {
	if grant {
		return "grant"
	}
	return "revoke"
}

func grantReason(grant bool) string {
	if grant {
		return "grant union extended"
	}
	return "revocation takes precedence over grants"
}

func openLabel(toVer int) string {
	if toVer == 0 {
		return "open"
	}
	return fmt.Sprintf("%d", toVer)
}

func cloneAttrs(in []Attribute) []Attribute {
	out := make([]Attribute, len(in))
	copy(out, in)
	return out
}

func validateAttrs(attrs []Attribute) error {
	names := map[string]bool{}
	ids := map[string]bool{}
	for _, a := range attrs {
		if a.ID == "" {
			return newError(ErrInvalidArgument, "empty attribute id")
		}
		if a.Name == "" {
			return newError(ErrInvalidArgument, "empty attribute name for %q", a.ID)
		}
		if ids[a.ID] {
			return newError(ErrInvalidArgument, "duplicated attribute id %q", a.ID)
		}
		if names[a.Name] {
			return newError(ErrInvalidArgument, "duplicated attribute name %q", a.Name)
		}
		ids[a.ID] = true
		names[a.Name] = true
	}
	return nil
}

// resolveRef 把请求引用（标识符或当前名字）解析为存活属性。
// 优先按标识符精确匹配；旧名字、已废弃标识符、从未存在的标识符一律
// 返回 ErrAttrNotFound，绝不退化为权限错误。
func (st *typeState) resolveRef(ref string) (*Attribute, error) {
	if a := st.byID[ref]; a != nil {
		return a, nil
	}
	if a := st.byName[ref]; a != nil {
		return a, nil
	}
	return nil, newError(ErrAttrNotFound,
		"attribute identifier %q does not exist (deprecated, renamed away, or never existed)", ref)
}

// CreateType 创建对象类型与首个版本。writePolicy 一经声明在类型内全局固定。
func (g *Gateway) CreateType(typeID string, attrs []Attribute, policy WritePolicy) (int, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if typeID == "" {
		return 0, newError(ErrInvalidArgument, "empty type id")
	}
	if policy != PolicyRejectWhole && policy != PolicyIgnoreField {
		return 0, newError(ErrInvalidArgument, "write policy must be reject_whole or ignore_field")
	}
	if s := g.store.getType(typeID); s != nil {
		return 0, newError(ErrInvalidArgument, "type %q already exists", typeID)
	}
	if err := validateAttrs(attrs); err != nil {
		return 0, err
	}
	v := &TypeVersion{
		TypeID:      typeID,
		Version:     1,
		Attrs:       cloneAttrs(attrs),
		WritePolicy: policy,
	}
	g.store.appendVersion(v)
	st := &typeState{
		typeID:      typeID,
		current:     v,
		byID:        map[string]*Attribute{},
		byName:      map[string]*Attribute{},
		attrs:       map[string]*AttrSnapshot{},
		writePolicy: policy,
	}
	for i := range v.Attrs {
		st.byID[v.Attrs[i].ID] = &v.Attrs[i]
		st.byName[v.Attrs[i].Name] = &v.Attrs[i]
		st.attrs[v.Attrs[i].ID] = &AttrSnapshot{
			AttrID: v.Attrs[i].ID, Name: v.Attrs[i].Name, Alive: true, FirstVer: 1,
		}
	}
	g.store.putType(st)
	g.log("create_type", fmt.Sprintf("version=1 policy=%s attrs=%d", policy, len(attrs)),
		map[string]any{"type_id": typeID, "attrs": attrs, "write_policy": string(policy)},
		map[string]any{"ok": true, "version": 1})
	return 1, nil
}

// Evolve 产生新版本，支持 add / rename / tighten / deprecate。
// requestedPolicy 非空时必须与类型既定策略一致，否则报策略冲突。
func (g *Gateway) Evolve(typeID string, changes []AttrChange, requestedPolicy WritePolicy) (int, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	st := g.store.getType(typeID)
	if st == nil {
		err := newError(ErrTypeNotFound, "type %q not found", typeID)
		g.log("evolve", "type missing", map[string]any{"type_id": typeID}, errOut(err))
		return 0, err
	}
	if requestedPolicy != "" && requestedPolicy != st.writePolicy {
		err := newError(ErrWritePolicyConflict,
			"type %q policy fixed at %q, cannot mix with %q", typeID, st.writePolicy, requestedPolicy)
		g.log("evolve", "write policy conflict",
			map[string]any{"type_id": typeID, "fixed": string(st.writePolicy),
				"requested": string(requestedPolicy)},
			errOut(err))
		return 0, err
	}
	// 全新切片承载新版本，避免与旧版本快照共享底层数组（演进不得改写历史）。
	var next []Attribute
	byID := map[string]*Attribute{}
	for _, a := range st.current.Attrs {
		cp := a
		next = append(next, cp)
	}
	for i := range next {
		byID[next[i].ID] = &next[i]
	}
	applied := make([]AttrChange, 0, len(changes))
	for _, c := range changes {
		a := byID[c.AttrID]
		switch c.Kind {
		case ChangeAdd:
			if a != nil {
				return 0, newError(ErrInvalidArgument, "attr %q already exists", c.AttrID)
			}
			if c.NewName == "" {
				return 0, newError(ErrInvalidArgument, "added attr %q needs a name", c.AttrID)
			}
			next = append(next, Attribute{ID: c.AttrID, Name: c.NewName, Kind: c.NewAttr})
			byID[c.AttrID] = &next[len(next)-1]
		case ChangeRename:
			if a == nil {
				err := newError(ErrAttrNotFound, "attr %q not found for rename", c.AttrID)
				g.log("evolve", "rename missing attr", map[string]any{"type_id": typeID}, errOut(err))
				return 0, err
			}
			a.Name = c.NewName
		case ChangeTighten:
			if a == nil {
				err := newError(ErrAttrNotFound, "attr %q not found for tighten", c.AttrID)
				g.log("evolve", "tighten missing attr", map[string]any{"type_id": typeID}, errOut(err))
				return 0, err
			}
			if c.NewAttr != "" {
				a.Kind = c.NewAttr
			}
		case ChangeDeprecate:
			if a == nil {
				err := newError(ErrAttrNotFound, "attr %q not found for deprecate", c.AttrID)
				g.log("evolve", "deprecate missing attr", map[string]any{"type_id": typeID}, errOut(err))
				return 0, err
			}
			// 废弃 = 标识符从新版本快照中整体移除（历史版本记录仍保留该属性）。
			removed := make([]Attribute, 0, len(next)-1)
			for _, x := range next {
				if x.ID != c.AttrID {
					removed = append(removed, x)
				}
			}
			next = removed
			delete(byID, c.AttrID)
		default:
			return 0, newError(ErrInvalidArgument, "unknown change kind %q", c.Kind)
		}
		applied = append(applied, c)
	}
	if err := validateAttrs(next); err != nil {
		return 0, err
	}
	nv := st.current.Version + 1
	version := &TypeVersion{
		TypeID:      typeID,
		Version:     nv,
		Attrs:       next,
		Changes:     applied,
		WritePolicy: st.writePolicy,
	}
	g.store.appendVersion(version)
	st.current = version
	st.byID = map[string]*Attribute{}
	st.byName = map[string]*Attribute{}
	for i := range version.Attrs {
		st.byID[version.Attrs[i].ID] = &version.Attrs[i]
		st.byName[version.Attrs[i].Name] = &version.Attrs[i]
	}
	for _, c := range applied {
		snap := st.attrs[c.AttrID]
		switch c.Kind {
		case ChangeAdd:
			st.attrs[c.AttrID] = &AttrSnapshot{AttrID: c.AttrID, Name: c.NewName, Alive: true, FirstVer: nv}
		case ChangeRename:
			if snap != nil {
				snap.Name = c.NewName
			}
		case ChangeDeprecate:
			if snap != nil {
				snap.Alive = false
			}
		}
	}
	g.log("evolve", fmt.Sprintf("new version=%d changes=%d policy_kept=%s", nv, len(applied), st.writePolicy),
		map[string]any{"type_id": typeID, "changes": changes},
		map[string]any{"ok": true, "version": nv})
	return nv, nil
}

// CurrentVersion 返回类型当前最新版本号。
func (g *Gateway) CurrentVersion(typeID string) (int, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	st := g.store.getType(typeID)
	if st == nil {
		return 0, newError(ErrTypeNotFound, "type %q not found", typeID)
	}
	return st.current.Version, nil
}

// Grant 授予主体在版本区间 [fromVer,toVer] 上对属性的权限；toVer=0 为开放区间。
// attrID=="*" 表示类型级读权限。标识符必须在当前版本存活，否则报不存在。
func (g *Gateway) Grant(typeID, subject, attrID string, op Op, fromVer, toVer int) error {
	return g.mutatePerm(typeID, subject, attrID, op, fromVer, toVer, true)
}

// Revoke 写入显式吊销记录，吊销区间优先于授权并集。
func (g *Gateway) Revoke(typeID, subject, attrID string, op Op, fromVer, toVer int) error {
	return g.mutatePerm(typeID, subject, attrID, op, fromVer, toVer, false)
}

func (g *Gateway) mutatePerm(typeID, subject, attrID string, op Op, fromVer, toVer int, grant bool) error {
	g.mu.Lock()
	defer g.mu.Unlock()
	st := g.store.getType(typeID)
	if st == nil {
		err := newError(ErrTypeNotFound, "type %q not found", typeID)
		g.log(permAction(grant), "type missing", map[string]any{"type_id": typeID}, errOut(err))
		return err
	}
	if subject == "" || attrID == "" || (op != OpRead && op != OpWrite) {
		err := newError(ErrInvalidArgument, "subject/attr/op invalid")
		g.log(permAction(grant), "bad args", map[string]any{"type_id": typeID}, errOut(err))
		return err
	}
	if attrID != "*" {
		if _, ok := st.byID[attrID]; !ok {
			err := newError(ErrAttrNotFound,
				"attribute identifier %q does not exist in current version (deprecated or never existed)", attrID)
			g.log(permAction(grant), "attr identifier missing",
				map[string]any{"type_id": typeID, "attr_id": attrID}, errOut(err))
			return err
		}
	}
	// 允许预置未来版本区间（fromVer 可超过当前版本）：记录立即物化，
	// 但只有在对应版本真正到达后才参与判定。
	if fromVer < 1 || (toVer != 0 && toVer < fromVer) {
		err := newError(ErrInvalidArgument, "invalid version interval [%d,%d]", fromVer, toVer)
		g.log(permAction(grant), "bad interval", map[string]any{"type_id": typeID}, errOut(err))
		return err
	}
	e := &PermissionEntry{
		TypeID: typeID, Subject: subject, AttrID: attrID, Op: op, Grant: grant,
		FromVer: fromVer, ToVer: toVer, OpenEnded: toVer == 0,
	}
	g.store.appendPermission(e)
	g.log(permAction(grant),
		fmt.Sprintf("interval=[%d,%s] materialized into view; %s",
			fromVer, openLabel(toVer), grantReason(grant)),
		map[string]any{
			"type_id": typeID, "subject": subject, "attr_id": attrID,
			"op": string(op), "from_ver": fromVer, "to_ver": toVer,
		},
		map[string]any{"ok": true, "seq": e.Seq})
	return nil
}

// AuditPermissions 是审计查询：返回全部历史权限记录（含已废弃属性条目）。
// 历史记录永不物理删除，但这些条目不参与任何线上判定。
func (g *Gateway) AuditPermissions(typeID, subject, attrID string) []*PermissionEntry {
	g.mu.Lock()
	defer g.mu.Unlock()
	rows := g.store.permissionHistory(typeID, subject, attrID)
	g.log("audit_permissions", fmt.Sprintf("returned %d immutable historical rows", len(rows)),
		map[string]any{"type_id": typeID, "subject": subject, "attr_id": attrID},
		map[string]any{"count": len(rows)})
	return rows
}

func writeInput(req WriteRequest) map[string]any {
	return map[string]any{
		"object_id": req.ObjectID, "type_id": req.TypeID, "subject": req.Subject,
		"schema_ver": req.SchemaVer, "fields": req.Fields,
	}
}

func sortedKeys(m map[string]any) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sortStrings(keys)
	return keys
}

// CreateObject 建对象并执行一次与 Write 完全相同的权限判定。
func (g *Gateway) CreateObject(req WriteRequest) (*WriteResult, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.store.getObject(req.ObjectID) != nil {
		err := newError(ErrInvalidArgument, "object %q already exists", req.ObjectID)
		g.log("create_object", "object exists", writeInput(req), errOut(err))
		return nil, err
	}
	res, err := g.writeLocked(req, "create_object")
	if err != nil {
		return nil, err
	}
	if res.Applied {
		obj := &Object{
			ID:        req.ObjectID,
			TypeID:    req.TypeID,
			SchemaVer: req.SchemaVer,
			Fields:    map[string]any{},
		}
		for _, id := range res.WrittenFields {
			if ref := refForID(res.resolvedRefs, id); ref != "" {
				obj.Fields[id] = req.Fields[ref]
			}
		}
		g.store.putObject(obj)
		res.ObjectVersion = 1
	}
	return res, nil
}

func refForID(resolved map[string]*Attribute, attrID string) string {
	for ref, a := range resolved {
		if a.ID == attrID {
			return ref
		}
	}
	return ""
}

// Write 对已存在对象执行一次写判定。
func (g *Gateway) Write(req WriteRequest) (*WriteResult, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.writeLocked(req, "write")
}

// writeLocked 执行写判定，调用方必须持有 g.mu。
//
// 判定顺序（错误优先级）：
//  1. 对象不存在（write 场景）；
//  2. 版本过期：提交版本不是类型当前最新版本 => 整体拒绝，不改任何状态；
//  3. 属性标识符不存在（废弃/旧名字/从未存在）=> 直接报错而非权限错误；
//  4. 逐属性写权限（吊销优先），按类型全局唯一的策略处理：
//     reject_whole 整次失败；ignore_field 跳过被拒字段。
func (g *Gateway) writeLocked(req WriteRequest, action string) (*WriteResult, error) {
	obj := g.store.getObject(req.ObjectID)
	if action == "write" && obj == nil {
		err := newError(ErrObjectNotFound, "object %q not found", req.ObjectID)
		g.log(action, "object missing takes precedence", writeInput(req), errOut(err))
		return nil, err
	}
	st := g.store.getType(req.TypeID)
	if st == nil {
		err := newError(ErrTypeNotFound, "type %q not found", req.TypeID)
		g.log(action, "type missing", writeInput(req), errOut(err))
		return nil, err
	}
	latest := st.current.Version
	if req.SchemaVer != latest {
		err := newError(ErrVersionExpired,
			"submitted schema version %d is not current version %d; whole write rejected, no state changed",
			req.SchemaVer, latest)
		g.log(action, "version expired precedes attribute checks",
			writeInput(req), errOut(err))
		return nil, err
	}

	refs := sortedKeys(req.Fields)
	// 先解析全部引用：任何“标识符不存在”都直接失败，整条拒绝且不改状态。
	resolved := make(map[string]*Attribute, len(refs))
	for _, ref := range refs {
		a, err := st.resolveRef(ref)
		if err != nil {
			g.log(action, "attribute identifier does not exist",
				writeInput(req), errOut(err))
			return nil, err
		}
		resolved[ref] = a
	}

	view := g.store.view(req.TypeID, req.Subject)
	result := &WriteResult{}
	for _, ref := range refs {
		a := resolved[ref]
		allowed := view.attrAllowed(a.ID, OpWrite, latest) || view.typeAllowed(OpWrite, latest)
		if !allowed {
			kind := ErrPermissionDenied
			// 仅当该属性自身存在显式授权且被同属性吊销覆盖时归类为吊销；
			// 类型级（*）吊销不构成属性级“吊销覆盖”分类。
			if byOp := view.attrs[a.ID]; byOp != nil {
				if p := byOp[OpWrite]; p != nil &&
					p.revokes.contains(latest) && p.grants.contains(latest) {
					kind = ErrRevoked
				}
			}
			result.Denies = append(result.Denies, FieldDeny{
				Ref: ref, Op: OpWrite, Kind: kind,
				Msg: fmt.Sprintf("subject %q denied write on %q at version %d (%s)",
					req.Subject, a.ID, latest, kind),
			})
		}
	}

	if len(result.Denies) > 0 && st.writePolicy == PolicyRejectWhole {
		g.assertPolicyUniform(st)
		g.log(action,
			fmt.Sprintf("reject_whole: %d denied field(s), whole write failed, nothing changed", len(result.Denies)),
			writeInput(req),
			map[string]any{"ok": true, "applied": false, "denies": result.Denies,
				"write_policy": string(st.writePolicy)})
		return result, nil
	}

	result.Applied = true
	denied := map[string]bool{}
	for _, d := range result.Denies {
		denied[d.Ref] = true
		result.IgnoredFields = append(result.IgnoredFields, resolved[d.Ref].ID)
	}
	target := obj
	if action == "create_object" {
		// 对象尚未落盘；只收集允许字段，由 CreateObject 建对象。
		for _, ref := range refs {
			if !denied[ref] {
				result.WrittenFields = append(result.WrittenFields, resolved[ref].ID)
			}
		}
		result.resolvedRefs = resolved
	} else {
		for _, ref := range refs {
			a := resolved[ref]
			if denied[ref] {
				continue
			}
			target.Fields[a.ID] = req.Fields[ref]
			result.WrittenFields = append(result.WrittenFields, a.ID)
		}
		target.SchemaVer = latest
		result.ObjectVersion = target.SchemaVer
	}
	g.log(action,
		fmt.Sprintf("policy=%s applied=%v written=%d ignored=%d",
			st.writePolicy, result.Applied, len(result.WrittenFields), len(result.IgnoredFields)),
		writeInput(req),
		map[string]any{"ok": true, "applied": result.Applied,
			"written": result.WrittenFields, "ignored": result.IgnoredFields,
			"denies": result.Denies, "write_policy": string(st.writePolicy)})
	return result, nil
}

func (g *Gateway) assertPolicyUniform(st *typeState) {
	// 写入策略在 CreateType 固定、Evolve 拒绝切换；此处不变式自检。
	if st.current.WritePolicy != st.writePolicy {
		panic("write policy mixed within a single type")
	}
}

// Read 对已存在对象执行读判定。
//
// 拒绝优先级固定：对象不存在 > 版本过期（读同样校验读取视图的版本一致性）
// > 类型级无读权限 > 属性级（逐属性独立判定，单个属性被拒不中断其余属性）。
// 无读权限的属性从返回对象中整体移除，不返回零值或掩码；允许缺少必需属性，
// 结果以 PartialView 明确标记。
func (g *Gateway) Read(objectID, typeID, subject string) (*ReadResult, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	in := map[string]any{"object_id": objectID, "type_id": typeID, "subject": subject}
	obj := g.store.getObject(objectID)
	if obj == nil {
		err := newError(ErrObjectNotFound, "object %q not found", objectID)
		g.log("read", "object missing takes precedence", in, errOut(err))
		return nil, err
	}
	st := g.store.getType(typeID)
	if st == nil {
		err := newError(ErrTypeNotFound, "type %q not found", typeID)
		g.log("read", "type missing", in, errOut(err))
		return nil, err
	}
	latest := st.current.Version
	view := g.store.view(typeID, subject)
	typeAllowed := view.typeAllowed(OpRead, latest) // 授权并集覆盖且不被吊销
	if !typeAllowed {
		// 进入逐属性投影的条件：持有类型级读授权，或对任一当前存活属性
		// 持有读授权。二者皆无时按固定优先级报类型级无读权限。
		// 仅持有“已废弃标识符”授权的主体不再具有该类型的线上读入口，
		// 其历史授权仍可经审计查询读取。
		anyAttr := false
		for id := range st.byID {
			if view.attrAllowed(id, OpRead, latest) {
				anyAttr = true
				break
			}
		}
		if !anyAttr {
			err := newError(ErrTypeNoPermission,
				"subject %q has no read permission on type %q at version %d", subject, typeID, latest)
			g.log("read", "type-level no permission precedes attribute-level projection",
				in, errOut(err))
			return nil, err
		}
	}

	projected := &Object{
		ID: obj.ID, TypeID: obj.TypeID, SchemaVer: obj.SchemaVer,
		Fields: map[string]any{},
	}
	var removed []string
	// 逐属性独立判定：只投影当前版本存活且有权限的字段。
	for id, val := range obj.Fields {
		if _, alive := st.byID[id]; !alive {
			// 已废弃标识符：线上投影整体移除，不构成 schema 违反。
			removed = append(removed, id)
			continue
		}
		if typeAllowed || view.attrAllowed(id, OpRead, latest) {
			projected.Fields[id] = val
		} else {
			removed = append(removed, id)
		}
	}
	sortStrings(removed)
	res := &ReadResult{Object: projected, PartialView: len(removed) > 0, RemovedAttrs: removed}
	g.log("read",
		fmt.Sprintf("projective removal: removed=%d partial=%v (missing required allowed)",
			len(removed), res.PartialView),
		in,
		map[string]any{"ok": true, "partial_view": res.PartialView,
			"removed_attrs": removed, "field_count": len(projected.Fields),
			"at_version": latest})
	return res, nil
}

// PermissionView 暴露给定主体在给定版本下的权限视图（测试与诊断用）。
// 计算只读 1 条类型当前状态记录 + 1 条物化视图记录，不触碰任何历史记录。
func (g *Gateway) PermissionView(typeID, subject string, atVersion int) (map[Op]map[string]bool, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	st := g.store.getType(typeID)
	if st == nil {
		return nil, newError(ErrTypeNotFound, "type %q not found", typeID)
	}
	v := g.store.view(typeID, subject)
	out := map[Op]map[string]bool{OpRead: {}, OpWrite: {}}
	for id := range st.byID {
		for _, op := range []Op{OpRead, OpWrite} {
			if v.attrAllowed(id, op, atVersion) {
				out[op][id] = true
			}
		}
	}
	for _, op := range []Op{OpRead, OpWrite} {
		if v.typeAllowed(op, atVersion) {
			out[op]["*"] = true
		}
	}
	return out, nil
}

// AccessCounters 返回自上次复位以来在线判定访问的历史记录计数，
// 用于以可验证方式证明规模无关性（见 TestViewAccessIsScaleIndependent）。
func (g *Gateway) AccessCounters() AccessCounters {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.store.counters
}

// ResetAccessCounters 清零访问计数。
func (g *Gateway) ResetAccessCounters() {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.store.resetCounters()
}

func sortStrings(s []string) {
	for i := 1; i < len(s); i++ {
		for j := i; j > 0 && s[j-1] > s[j]; j-- {
			s[j-1], s[j] = s[j], s[j-1]
		}
	}
}
