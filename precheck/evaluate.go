package precheck

import "fmt"

// evaluationPlan 汇总一次演算所依据的快照材料，供审计与对照使用。
type evaluationPlan struct {
	schema  *ParamSchema
	hookset *HookSet
	perm    *PermissionSnapshot
	hookIDs []string
}

func (e *Engine) evaluate(req PrecheckRequest, audit bool) (*PrecheckResult, *evaluationPlan, error) {
	e.mu.Lock()
	defer e.mu.Unlock()

	agg := req.Aggregation
	if agg != FailFast && agg != CollectAll {
		agg = FailFast
	}
	e.resetStats()

	result := &PrecheckResult{}
	plan := &evaluationPlan{}

	// —— 解析阶段：四类错误按固定优先级 A<B<C<D 中的“B 最高”顺序检查 ——
	// 取舍（见设计说明）：快照缺失会使“未定义/不存在”的判断本身不可信，
	// 因此 ErrSnapshotMissing 优先于其余三类；之后依次 A、C、D。
	schema, sPresent, sGap := e.schemas.Point(req.ActionType, req.At)
	hset, hPresent, hGap := e.hooksets.Point(req.ActionType, req.At)
	_, uPresent, uGap := e.users.Point(req.Caller, req.At)

	perm, permGap := e.rebuildPermission(req.Caller, req.At)
	plan.perm = perm

	switch {
	case sGap || hGap || uGap || permGap:
		result.Verdict = VerdictResolutionError
		result.ResolutionErr = &ResolutionError{Kind: ErrSnapshotMissing,
			Detail: fmt.Sprintf("history required to reconstruct snapshot at %d is missing", req.At)}
	case !sPresent:
		result.Verdict = VerdictResolutionError
		result.ResolutionErr = &ResolutionError{Kind: ErrActionNotYetDefined,
			Detail: fmt.Sprintf("action type %q not defined at %d", req.ActionType, req.At)}
	case !uPresent:
		result.Verdict = VerdictResolutionError
		result.ResolutionErr = &ResolutionError{Kind: ErrCallerNotExist,
			Detail: fmt.Sprintf("caller %q does not exist at %d", req.Caller, req.At)}
	}

	if result.Verdict == VerdictResolutionError {
		if sPresent {
			result.SchemaVersion = schema.Version
		}
		if hPresent {
			result.HookSetVersion = hset.Version
		}
		e.finishAudit(req, result, plan, audit)
		return result, plan, nil
	}

	plan.schema = schema
	plan.perm = perm
	result.SchemaVersion = schema.Version

	// 钩子集合缺省为空集合（该类型当时没有钩子是合法状态）。
	if hPresent {
		plan.hookset = hset
		result.HookSetVersion = hset.Version
	} else {
		plan.hookset = &HookSet{}
	}
	for _, ref := range plan.hookset.Pre {
		plan.hookIDs = append(plan.hookIDs, "pre:"+ref.ID)
		if _, ok := e.registry.Get(ref.ID); !ok {
			result.Verdict = VerdictResolutionError
			result.ResolutionErr = &ResolutionError{Kind: ErrSnapshotMissing,
				Detail: fmt.Sprintf("hook implementation %q referenced at %d is unavailable", ref.ID, req.At)}
			e.finishAudit(req, result, plan, audit)
			return result, plan, nil
		}
	}
	for _, ref := range plan.hookset.Post {
		plan.hookIDs = append(plan.hookIDs, "post:"+ref.ID)
		if _, ok := e.registry.Get(ref.ID); !ok {
			result.Verdict = VerdictResolutionError
			result.ResolutionErr = &ResolutionError{Kind: ErrSnapshotMissing,
				Detail: fmt.Sprintf("hook implementation %q referenced at %d is unavailable", ref.ID, req.At)}
			e.finishAudit(req, result, plan, audit)
			return result, plan, nil
		}
	}

	// D：参数结构约束（当时生效版本）。
	if err := validateParams(schema, req.Params); err != nil {
		result.Verdict = VerdictResolutionError
		result.ResolutionErr = err.(*ResolutionError)
		e.finishAudit(req, result, plan, audit)
		return result, plan, nil
	}

	// —— 校验阶段 ——
	collectAll := agg == CollectAll

	// 授权门：权限不足本身是一个前置阶段失败，且必须阻断一切前置钩子。
	if !perm.Authorize(req.Caller, schema.RequiredPermission) {
		result.Verdict = VerdictDenied
		result.Failures = []HookFailure{{
			Stage:   "pre",
			HookID:  authGateID,
			Code:    "permission_denied",
			Message: fmt.Sprintf("caller %q lacks %q at %d", req.Caller, schema.RequiredPermission, req.At),
		}}
		e.finishAudit(req, result, plan, audit)
		return result, plan, nil
	}

	objectView := func(key string) (any, bool) {
		v, ok, gap := e.objects.Point(key, req.At)
		if gap {
			return nil, false
		}
		return v, ok
	}
	objectGap := func(key string) bool {
		_, _, gap := e.objects.Point(key, req.At)
		return gap
	}

	pre := &PreContext{
		at:         req.At,
		caller:     req.Caller,
		params:     deepCopyParams(req.Params),
		scratch:    map[string]any{},
		objectView: objectView,
		objectGap:  objectGap,
	}

	// 前置阶段：FailFast 遇首个失败即停；CollectAll 跑完全部前置钩子并聚合。
	// 固定规则：前置阶段只要存在失败，后置阶段一律不演算，
	// 因此前置失败绝不与后置失败混合聚合（两个阶段的失败永不同时出现）。
	var preFailures []HookFailure
	for _, ref := range plan.hookset.Pre {
		hook, _ := e.registry.Get(ref.ID)
		fail := runPreHook(hook, pre)
		if fail != nil {
			preFailures = append(preFailures, *fail)
			if !collectAll {
				break
			}
		}
	}
	if len(preFailures) > 0 {
		result.Verdict = VerdictDenied
		result.Failures = preFailures
		e.finishAudit(req, result, plan, audit)
		return result, plan, nil
	}

	// 前置快照冻结：后置阶段拿到的是不可变视图与 scratch 的深拷贝。
	post := &PostContext{pre: &PreContext{
		at:         pre.at,
		caller:     pre.caller,
		params:     deepCopyParams(pre.params),
		scratch:    deepCopy(pre.scratch).(map[string]any),
		objectView: objectView,
		objectGap:  objectGap,
	}}

	var postFailures []HookFailure
	var intents []Intent
	for _, ref := range plan.hookset.Post {
		hook, _ := e.registry.Get(ref.ID)
		got, fail := runPostHook(hook, post)
		if fail != nil {
			postFailures = append(postFailures, *fail)
			if !collectAll {
				break
			}
			continue
		}
		intents = append(intents, got...)
	}
	if len(postFailures) > 0 {
		result.Verdict = VerdictDenied
		result.Failures = postFailures
		e.finishAudit(req, result, plan, audit)
		return result, plan, nil
	}

	intents = append(intents, defaultIntents(req)...)
	result.Verdict = VerdictAllowed
	result.Intents = intents
	e.finishAudit(req, result, plan, audit)
	return result, plan, nil
}

// rebuildPermission 从调用者出发沿活跃边做 BFS 重建当时的权限继承快照。
// 只需点查“可达节点”的出边段：每个被访问节点 O(log 该节点历史段数)。
func (e *Engine) rebuildPermission(caller string, at Moment) (*PermissionSnapshot, bool) {
	snap := &PermissionSnapshot{At: at, Complete: true}
	seen := map[string]bool{caller: true}
	queue := []string{caller}
	for len(queue) > 0 {
		node := queue[0]
		queue = queue[1:]
		for t0 := range e.outTargets[node] {
			_, on, gap := e.edgeState.Point(edgeKey(node, t0), at)
			if gap {
				return nil, true
			}
			if !on {
				continue
			}
			snap.Active = append(snap.Active, Edge{From: node, To: t0})
			if !seen[t0] {
				seen[t0] = true
				queue = append(queue, t0)
			}
		}
	}
	for node := range seen {
		snap.Held = append(snap.Held, node)
	}
	sortStrings(snap.Held)
	sortEdges(snap.Active)
	return snap, false
}

func (e *Engine) finishAudit(req PrecheckRequest, result *PrecheckResult, plan *evaluationPlan, write bool) {
	if !write {
		return
	}
	var held []string
	var active []Edge
	complete := false
	if plan.perm != nil {
		held = append([]string(nil), plan.perm.Held...)
		active = append([]Edge(nil), plan.perm.Active...)
		complete = true
	}
	entry := AuditEntry{
		Request: req,
		Result:  *result,
		HookIDs: append([]string(nil), plan.hookIDs...),
		PermSnap: &PermissionSnapshot{
			At:       req.At,
			Active:   active,
			Held:     held,
			Complete: complete,
		},
		Metrics: e.metrics(),
	}
	result.AuditSeq = e.audit.Append(entry)
}

func runPreHook(h Hook, ctx *PreContext) (failure *HookFailure) {
	defer func() {
		if r := recover(); r != nil {
			failure = &HookFailure{Stage: "pre", HookID: h.ID(), Code: "hook_panic", Message: fmt.Sprint(r)}
		}
	}()
	if err := h.Pre(ctx); err != nil {
		return asFailure("pre", h.ID(), err)
	}
	return nil
}

func runPostHook(h Hook, ctx *PostContext) (intents []Intent, failure *HookFailure) {
	defer func() {
		if r := recover(); r != nil {
			failure = &HookFailure{Stage: "post", HookID: h.ID(), Code: "hook_panic", Message: fmt.Sprint(r)}
		}
	}()
	got, err := h.Post(ctx)
	if err != nil {
		return nil, asFailure("post", h.ID(), err)
	}
	return got, nil
}

func asFailure(stage, id string, err error) *HookFailure {
	if he, ok := err.(*HookError); ok {
		return &HookFailure{Stage: stage, HookID: id, Code: he.Code, Message: he.Message}
	}
	return &HookFailure{Stage: stage, HookID: id, Code: "hook_error", Message: err.Error()}
}

// defaultIntents 给出与钩子无关的、由参数声明的规范状态改变意图。
func defaultIntents(req PrecheckRequest) []Intent {
	key, ok := req.Params["object_key"].(string)
	if !ok || key == "" {
		return nil
	}
	return []Intent{{
		Op:        "upsert",
		ObjectKey: key,
		Value:     deepCopy(req.Params["value"]),
		Source:    "default-effect:" + req.ActionType,
	}}
}

func sortStrings(xs []string) {
	for i := 1; i < len(xs); i++ {
		for j := i; j > 0 && xs[j-1] > xs[j]; j-- {
			xs[j-1], xs[j] = xs[j], xs[j-1]
		}
	}
}

func sortEdges(es []Edge) {
	less := func(a, b Edge) bool {
		if a.From != b.From {
			return a.From < b.From
		}
		return a.To < b.To
	}
	for i := 1; i < len(es); i++ {
		for j := i; j > 0 && less(es[j], es[j-1]); j-- {
			es[j-1], es[j] = es[j], es[j-1]
		}
	}
}
