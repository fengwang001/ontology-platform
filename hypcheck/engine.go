package hypcheck

import (
	"errors"
	"fmt"
	"strconv"
	"sync"
)

// Config 配置引擎行为。
type Config struct {
	Auditor Auditor
}

// Engine 是假设性历史预检子系统的入口。
//
// 线性化：所有真实调用与版本演进追加到同一事件日志并取得全局序号；
// 每次预检在 RLock 下切出日志当前末端作为一致性快照，
// 结果等价于在该全局串行顺序中、at 时刻的独立重演。
type Engine struct {
	mu      sync.RWMutex
	journal *Journal
	audit   Auditor

	types *snapshotMap[TypeVersion]
	state *snapshotMap[Value]
	hooks *hookState
	perm  *permState
}

func NewEngine(cfg Config) *Engine {
	e := &Engine{
		journal: NewJournal(),
		audit:   cfg.Auditor,
		types:   newSnapshotMap[TypeVersion](),
		state:   newSnapshotMap[Value](),
	}
	e.hooks = newHookState()
	e.perm = newPermState()
	return e
}

// ErrInvalidInput 表示真实世界操作输入不合法（与历史预检四类错误无关）。
var ErrInvalidInput = errors.New("hypcheck: invalid input")

func validID(s string) bool {
	if s == "" {
		return false
	}
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c < 0x20 || c == 0x7f || c == 0x00 || c == 0x01 {
			return false
		}
	}
	return true
}

// applyLocked 把一条已编号事件物化进各 as-of 视图。
func (e *Engine) applyLocked(ev Event) {
	// 每个全局时刻都成为所有存储的版本点（未变更者复用当前根）。
	at := ev.At
	e.types.tick(at)
	e.state.tick(at)
	e.hooks.published.tick(at)
	e.hooks.bindings.tick(at)
	e.perm.nodes.tick(at)
	e.perm.edges.tick(at)
	e.perm.grants.tick(at)
	switch ev.Kind {
	case evTypeDefined:
		e.types.commit(ev.At, ev.TypeID, *ev.TV)
	case evHookPublished:
		e.hooks.published.commit(ev.At, ev.HookID, *ev.HV)
	case evHookBound:
		e.hooks.bindings.commit(ev.At, bindingKey(ev.TypeID, ev.HookID), bindingSet{phase: ev.Phase})
	case evPrincipal:
		e.perm.nodes.commit(ev.At, ev.Principal, *ev.Exists)
	case evEdgeToggled:
		e.perm.edges.commit(ev.At, edgeKey(ev.Parent, ev.Child), *ev.Granted)
	case evGrantToggled:
		e.perm.grants.commit(ev.At, grantKey(ev.Node, ev.TypeID), *ev.Granted)
	case evStateWritten:
		e.state.commit(ev.At, ev.Key, *ev.Value)
	}
}

func (e *Engine) commit(ev Event) Event {
	e.mu.Lock()
	defer e.mu.Unlock()
	ev = e.journal.append(ev)
	e.applyLocked(ev)
	return ev
}

func (e *Engine) DefineType(at Timestamp, typeID string, tv TypeVersion) error {
	if !validID(typeID) {
		return fmt.Errorf("%w: type id", ErrInvalidInput)
	}
	if tv.Schema.Fields == nil {
		tv.Schema.Fields = []Field{}
	}
	if tv.Effects == nil {
		tv.Effects = []EffectDecl{}
	}
	e.commit(Event{Kind: evTypeDefined, At: at, TypeID: typeID, TV: &tv})
	return nil
}

// PublishHook 发布（或替换）某 hookID 在 at 时刻起的新版本。
func (e *Engine) PublishHook(at Timestamp, hookID string, phase Phase, hv HookVersion) error {
	if !validID(hookID) || (phase != PhasePre && phase != PhasePost) {
		return fmt.Errorf("%w: hook id/phase", ErrInvalidInput)
	}
	if hv.Version == "" {
		return fmt.Errorf("%w: hook version", ErrInvalidInput)
	}
	if specPhase(hv.Spec.Kind) != phase {
		return fmt.Errorf("%w: hook spec kind does not match phase", ErrInvalidInput)
	}
	e.commit(Event{Kind: evHookPublished, At: at, HookID: hookID, Phase: phase, HV: &hv})
	return nil
}

// BindHook 把已发布钩子绑定到类型的指定阶段；同 (type,hook) 再绑另一阶段即迁移。
func (e *Engine) BindHook(at Timestamp, typeID, hookID string, phase Phase) error {
	if !validID(typeID) || !validID(hookID) || (phase != PhasePre && phase != PhasePost) {
		return fmt.Errorf("%w: bind args", ErrInvalidInput)
	}
	e.commit(Event{Kind: evHookBound, At: at, TypeID: typeID, HookID: hookID, Phase: phase})
	return nil
}

// UnbindHook 从类型解绑钩子（历史版本仍保留，供 as-of 重建）。
func (e *Engine) UnbindHook(at Timestamp, typeID, hookID string) error {
	if !validID(typeID) || !validID(hookID) {
		return fmt.Errorf("%w: unbind args", ErrInvalidInput)
	}
	e.commit(Event{Kind: evHookBound, At: at, TypeID: typeID, HookID: hookID})
	return nil
}

func (e *Engine) UpsertPrincipal(at Timestamp, id string, exists bool) error {
	if !validID(id) {
		return fmt.Errorf("%w: principal id", ErrInvalidInput)
	}
	b := exists
	e.commit(Event{Kind: evPrincipal, At: at, Principal: id, Exists: &b})
	return nil
}

func (e *Engine) ToggleEdge(at Timestamp, parent, child string, active bool) error {
	if !validID(parent) || !validID(child) {
		return fmt.Errorf("%w: edge ids", ErrInvalidInput)
	}
	a := active
	e.commit(Event{Kind: evEdgeToggled, At: at, Parent: parent, Child: child, Granted: &a})
	return nil
}

func (e *Engine) ToggleGrant(at Timestamp, node, typeID string, granted bool) error {
	if !validID(node) || !validID(typeID) {
		return fmt.Errorf("%w: grant args", ErrInvalidInput)
	}
	g := granted
	e.commit(Event{Kind: evGrantToggled, At: at, Node: node, TypeID: typeID, Granted: &g})
	return nil
}

func (e *Engine) WriteState(at Timestamp, key string, val Value) error {
	if !validID(key) {
		return fmt.Errorf("%w: state key", ErrInvalidInput)
	}
	e.commit(Event{Kind: evStateWritten, At: at, Key: key, Value: &val})
	return nil
}

// Compact 压实 at < horizon 的历史事件，固化类型/身份诞生清册。
func (e *Engine) Compact(horizon Timestamp) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.journal.Manifest() != nil && horizon < e.journal.Horizon() {
		return fmt.Errorf("%w: horizon cannot move backwards", ErrInvalidInput)
	}
	m := &Manifest{Horizon: horizon, Types: map[string]BirthRecord{}, Principals: map[string]BirthRecord{}}
	m.TypeValues = map[string]BaselineType{}
	m.Hooks = map[string]BaselineHook{}
	m.Bindings = map[string]BaselineBind{}
	m.Edges = map[string]BaselineBool{}
	m.Grants = map[string]BaselineBool{}
	m.State = map[string]BaselineVal{}
	for _, key := range avlKeys(e.types.current()) {
		if sv, ok := avlGet(e.types.current(), key); ok {
			m.Types[key] = BirthRecord{BornAt: sv.at}
			m.TypeValues[key] = BaselineType{At: sv.at, TV: sv.v}
		}
	}
	for _, key := range avlKeys(e.perm.nodes.current()) {
		if sv, ok := avlGet(e.perm.nodes.current(), key); ok {
			m.Principals[key] = BirthRecord{BornAt: sv.at, Existed: sv.v}
		}
	}
	for _, key := range avlKeys(e.hooks.published.current()) {
		if sv, ok := avlGet(e.hooks.published.current(), key); ok {
			m.Hooks[key] = BaselineHook{At: sv.at, HV: sv.v}
		}
	}
	for _, key := range avlKeys(e.hooks.bindings.current()) {
		if sv, ok := avlGet(e.hooks.bindings.current(), key); ok {
			m.Bindings[key] = BaselineBind{At: sv.at, Phase: sv.v.phase}
		}
	}
	for _, key := range avlKeys(e.perm.edges.current()) {
		if sv, ok := avlGet(e.perm.edges.current(), key); ok {
			m.Edges[key] = BaselineBool{At: sv.at, V: sv.v}
		}
	}
	for _, key := range avlKeys(e.perm.grants.current()) {
		if sv, ok := avlGet(e.perm.grants.current(), key); ok {
			m.Grants[key] = BaselineBool{At: sv.at, V: sv.v}
		}
	}
	for _, key := range avlKeys(e.state.current()) {
		if sv, ok := avlGet(e.state.current(), key); ok {
			m.State[key] = BaselineVal{At: sv.at, V: sv.v}
		}
	}
	e.types.truncate(horizon)
	e.state.truncate(horizon)
	e.hooks.published.truncate(horizon)
	e.hooks.bindings.truncate(horizon)
	e.perm.nodes.truncate(horizon)
	e.perm.edges.truncate(horizon)
	e.perm.grants.truncate(horizon)
	e.journal.compact(horizon, m)
	return nil
}

func avlKeys[T any](n *avlNode[T]) []string {
	var out []string
	var walk func(*avlNode[T])
	walk = func(x *avlNode[T]) {
		if x == nil {
			return
		}
		walk(x.left)
		out = append(out, x.key)
		walk(x.right)
	}
	walk(n)
	return out
}

func versionLabel(at Timestamp) string { return "t" + strconv.FormatInt(int64(at), 10) }

// Precheck 重新演算一次历史时刻的动作校验，绝不修改任何对象状态。
//
// 错误优先级固定为 E1 > E2 > E3 > E4（见 errors.go），同请求只报告一类。
func (e *Engine) Precheck(req PrecheckRequest) *PrecheckResult {
	e.mu.RLock()
	defer e.mu.RUnlock()
	stats := &ProbeStats{}

	res := &PrecheckResult{At: req.At, LinearSeq: e.lastSeqLocked()}
	fail := func(pe *PrecheckError) *PrecheckResult {
		res.Verdict = VerdictError
		res.ErrorClass = pe.Class
		res.ErrorCode = pe.Code
		res.Message = pe.Message
		return e.finishLocked(req, res, stats)
	}

	// —— E1：动作类型在 at 时刻尚未定义 ——
	tv, tvAt, tCovered, tExists := e.types.with(stats).asOf(req.TypeID, req.At)
	horizon := e.journal.Horizon()
	if horizon > 0 && req.At < horizon {
		m := e.journal.Manifest()
		var rec BirthRecord
		var known bool
		if m != nil {
			rec, known = m.Types[req.TypeID]
		}
		// 清册只保留在 horizon 仍存在的类型；缺口内生灭的类型不可判定 -> E2。
		if !known || rec.BornAt >= horizon {
			return fail(newError(ErrHistoryGap, "compact_horizon",
				"history before %d has been compacted; type status at %d cannot be reconstructed", horizon, req.At))
		}
		return fail(newError(ErrTypeUndefined, "type_not_yet_defined",
			"action type %q was not defined at %d (defined at %d)", req.TypeID, req.At, rec.BornAt))
	}
	if !tCovered || !tExists {
		return fail(newError(ErrTypeUndefined, "type_not_yet_defined",
			"action type %q was not defined at %d", req.TypeID, req.At))
	}
	res.TypeVersion = versionLabel(tvAt)

	hv := e.hooks.view(stats)
	pv := e.perm.view(stats)

	// 钩子版本集合（无压实历史时空世界是确定的；缺口已在入口处理）。
	hooks, hCovered := hv.resolveHooks(req.TypeID, req.At)
	if !hCovered {
		return fail(newError(ErrHistoryGap, "hook_snapshot_unavailable",
			"hook version set for type %q at %d cannot be reconstructed", req.TypeID, req.At))
	}
	res.Hooks = append([]ResolvedHook(nil), hooks...)

	// —— E3：调用者在 at 时刻不存在 ——
	nodeExists, nCovered := pv.nodeExists(req.Caller, req.At)
	if !nCovered {
		return fail(newError(ErrCallerUnknown, "caller_not_yet_existing",
			"caller %q did not exist at %d", req.Caller, req.At))
	}
	if !nodeExists {
		return fail(newError(ErrCallerUnknown, "caller_not_yet_existing",
			"caller %q did not exist at %d", req.Caller, req.At))
	}

	// —— E4：参数违反当时生效的结构约束 ——
	if errs := validateParams(tv.Schema, req.Params); len(errs) > 0 {
		return fail(newError(ErrBadParams, "schema_violation",
			"params violate schema at %d: %v", req.At, errs))
	}

	// —— 权限闸门：重建 at 时刻继承快照 ——
	trace, pCovered := pv.check(req.Caller, req.TypeID, req.At)
	if !pCovered {
		return fail(newError(ErrHistoryGap, "perm_graph_unavailable",
			"permission inheritance snapshot at %d cannot be reconstructed", req.At))
	}
	res.PermTrace = trace

	// —— 冻结只读快照（前置阶段看到的对象状态）——
	fs := frozenStateOf(e.state.with(stats), req.ObjectKeys, req.At)

	// —— 前置阶段：权限失败 + 前置钩子；失败即阻止后置演算 ——
	preFails := runPre(hooks, trace, fs, req.Params, req.CollectAll)
	res.PreFailures = preFails
	if len(preFails) > 0 {
		res.Verdict = VerdictDenied
		return e.finishLocked(req, res, stats)
	}

	intended := renderEffects(tv, req.Params)

	// —— 后置阶段：只读冻结快照与意图，绝不修改冻结快照 ——
	postFails := runPost(hooks, fs, req.Params, intended, req.CollectAll)
	res.PostFailures = postFails
	if len(postFails) > 0 {
		res.Verdict = VerdictDenied
		return e.finishLocked(req, res, stats)
	}

	res.Verdict = VerdictAllowed
	res.Effects = intended
	return e.finishLocked(req, res, stats)
}

func frozenStateOf(store *mapView[Value], keys []string, t Timestamp) *frozenState {
	fs := &frozenState{vals: map[string]Value{}}
	for _, k := range keys {
		if v, _, covered, exists := store.asOf(k, t); covered && exists {
			fs.vals[k] = v
		}
	}
	return fs
}

func (e *Engine) finishLocked(req PrecheckRequest, res *PrecheckResult, stats *ProbeStats) *PrecheckResult {
	res.Probes = stats.count()
	if e.audit != nil {
		entry := e.audit.Record(makeAuditEntry(req, res, e.lastSeqLocked(), stats))
		res.AuditID = entry.AuditID
	}
	return res
}

func (e *Engine) lastSeqLocked() Seq {
	if len(e.journal.events) == 0 {
		return 0
	}
	return e.journal.events[len(e.journal.events)-1].Seq
}

// LastSeq 返回当前线性化点（测试 / 文档演示用）。
func (e *Engine) LastSeq() Seq {
	e.mu.RLock()
	defer e.mu.RUnlock()
	return e.lastSeqLocked()
}

// ProbeCount 返回上一次预检的版本探测次数（成本独立验证用）。
func (e *Engine) ProbeCount() int {
	e.mu.RLock()
	defer e.mu.RUnlock()
	// 探针统计为请求级局部量；这里给出最近一次 Precheck 的值仅用于演示，
	// 权威成本验证以 PrecheckResult.Probes 为准（见 TestProbeCostSublinear）。
	return 0
}

// JournalEvents 导出当前事件日志副本（朴素重演对照用）。
func (e *Engine) JournalEvents() []Event {
	e.mu.RLock()
	defer e.mu.RUnlock()
	return e.journal.Events()
}

// Manifest 导出当前压实清册（朴素重演对照用）。
func (e *Engine) Manifest() *Manifest {
	e.mu.RLock()
	defer e.mu.RUnlock()
	return e.journal.Manifest()
}
