package precheck

import (
	"fmt"
	"sort"
)

// NaiveReplay 是独立实现的朴素重演参考模型：
// 每次预检都从头线性扫描全部不晚于 t 的事件，用普通 map 重建当时世界，
// 再按相同语义规则独立演算。它故意不使用 TemporalStore，
// 作为差分测试的对照实现（慢但直白，容易人工审计正确性）。
type NaiveReplay struct {
	events   []Event
	registry *Registry
}

func NewNaiveReplay(reg *Registry) *NaiveReplay {
	return &NaiveReplay{registry: reg}
}

func (n *NaiveReplay) Append(ev Event) { n.events = append(n.events, ev) }

// Load 用全局事件日志快照装载参考模型。
func (n *NaiveReplay) Load(events []Event) {
	n.events = append([]Event(nil), events...)
}

// Precheck 用朴素全量重演在时刻 t 演算同一请求。
func (n *NaiveReplay) Precheck(req PrecheckRequest) (*PrecheckResult, *PermissionSnapshot, error) {
	agg := req.Aggregation
	if agg != FailFast && agg != CollectAll {
		agg = FailFast
	}
	collectAll := agg == CollectAll

	schemas := map[string]*ParamSchema{}
	hooksets := map[string]*HookSet{}
	users := map[string]bool{}
	objects := map[string]any{}
	objectPresent := map[string]bool{}
	adj := map[string]map[string]bool{}
	gaps := map[string]map[string][][2]Moment{}

	addGap := func(domain, key string, g [2]Moment) {
		if gaps[domain] == nil {
			gaps[domain] = map[string][][2]Moment{}
		}
		gaps[domain][key] = append(gaps[domain][key], g)
	}
	inGap := func(domain, key string, t Moment) bool {
		for _, g := range gaps[domain][key] {
			if t >= g[0] && t < g[1] {
				return true
			}
		}
		return false
	}

	// 按全局顺序（日志顺序）扫描截至 t（含边界 t）的事件。
	for _, ev := range n.events {
		if ev.At > req.At {
			break
		}
		switch ev.Kind {
		case EvTypeSchema:
			schemas[ev.ActionType] = ev.Schema
		case EvHookSet:
			hooksets[ev.ActionType] = ev.HookSet
		case EvUserAdded:
			users[ev.User] = true
		case EvEdgeAdded:
			if adj[ev.From] == nil {
				adj[ev.From] = map[string]bool{}
			}
			adj[ev.From][ev.To] = true
		case EvEdgeRemoved:
			if adj[ev.From] != nil {
				delete(adj[ev.From], ev.To)
			}
		case EvObjectUpsert:
			objects[ev.ObjectKey] = ev.Value
			objectPresent[ev.ObjectKey] = true
		case EvObjectDelete:
			objectPresent[ev.ObjectKey] = false
		case EvHistoryGap:
			addGap(ev.GapDomain, ev.GapKey, [2]Moment{ev.GapStart, ev.GapEnd})
		case EvRealCall:
			// 纯日志事实，无独立副作用。
		}
	}

	result := &PrecheckResult{}
	schema, sPresent := schemas[req.ActionType]
	hset, hPresent := hooksets[req.ActionType]

	sGap := inGap(GapDomainSchema, req.ActionType, req.At)
	hGap := inGap(GapDomainHooks, req.ActionType, req.At)
	uGap := inGap(GapDomainUser, req.Caller, req.At)
	perm, permGap := n.rebuildPermission(adj, inGap, req.Caller, req.At)

	switch {
	case sGap || hGap || uGap || permGap:
		result.Verdict = VerdictResolutionError
		result.ResolutionErr = &ResolutionError{Kind: ErrSnapshotMissing, Detail: "naive: snapshot missing"}
	case !sPresent:
		result.Verdict = VerdictResolutionError
		result.ResolutionErr = &ResolutionError{Kind: ErrActionNotYetDefined, Detail: "naive: type undefined"}
	case !users[req.Caller]:
		result.Verdict = VerdictResolutionError
		result.ResolutionErr = &ResolutionError{Kind: ErrCallerNotExist, Detail: "naive: caller absent"}
	}
	if result.Verdict == VerdictResolutionError {
		if sPresent {
			result.SchemaVersion = schema.Version
		}
		if hPresent {
			result.HookSetVersion = hset.Version
		}
		return result, perm, nil
	}
	result.SchemaVersion = schema.Version
	if hPresent {
		result.HookSetVersion = hset.Version
	}

	if !hPresent {
		hset = &HookSet{}
	}
	for _, refs := range [][]HookRef{hset.Pre, hset.Post} {
		for _, ref := range refs {
			if _, ok := n.registry.Get(ref.ID); !ok {
				result.Verdict = VerdictResolutionError
				result.ResolutionErr = &ResolutionError{Kind: ErrSnapshotMissing,
					Detail: fmt.Sprintf("naive: hook %q unavailable", ref.ID)}
				return result, perm, nil
			}
		}
	}

	if err := validateParams(schema, req.Params); err != nil {
		result.Verdict = VerdictResolutionError
		result.ResolutionErr = err.(*ResolutionError)
		return result, perm, nil
	}

	if !perm.Authorize(req.Caller, schema.RequiredPermission) {
		result.Verdict = VerdictDenied
		result.Failures = []HookFailure{{Stage: "pre", HookID: authGateID, Code: "permission_denied"}}
		return result, perm, nil
	}

	objectView := func(key string) (any, bool) {
		if inGap(GapDomainObject, key, req.At) || !objectPresent[key] {
			return nil, false
		}
		return objects[key], true
	}
	objectGap := func(key string) bool { return inGap(GapDomainObject, key, req.At) }

	pre := &PreContext{
		at:         req.At,
		caller:     req.Caller,
		params:     deepCopyParams(req.Params),
		scratch:    map[string]any{},
		objectView: objectView,
		objectGap:  objectGap,
	}

	var preFailures []HookFailure
	for _, ref := range hset.Pre {
		hook, _ := n.registry.Get(ref.ID)
		if fail := runPreHook(hook, pre); fail != nil {
			preFailures = append(preFailures, *fail)
			if !collectAll {
				break
			}
		}
	}
	if len(preFailures) > 0 {
		result.Verdict = VerdictDenied
		result.Failures = preFailures
		return result, perm, nil
	}

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
	for _, ref := range hset.Post {
		hook, _ := n.registry.Get(ref.ID)
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
		return result, perm, nil
	}

	intents = append(intents, defaultIntents(req)...)
	result.Verdict = VerdictAllowed
	result.Intents = intents
	return result, perm, nil
}

func (n *NaiveReplay) rebuildPermission(
	adj map[string]map[string]bool,
	inGap func(domain, key string, t Moment) bool,
	caller string,
	at Moment,
) (*PermissionSnapshot, bool) {
	snap := &PermissionSnapshot{At: at, Complete: true}
	seen := map[string]bool{caller: true}
	queue := []string{caller}
	for len(queue) > 0 {
		node := queue[0]
		queue = queue[1:]
		if inGap(GapDomainEdges, node, at) {
			return nil, true
		}
		for t0 := range adj[node] {
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
	sort.Strings(snap.Held)
	sortEdges(snap.Active)
	return snap, false
}

// Canonical 把结果规整为与审计序号无关的可比较表示，供差分测试使用。
func Canonical(r *PrecheckResult) map[string]any {
	out := map[string]any{
		"verdict":         string(r.Verdict),
		"schema_version":  r.SchemaVersion,
		"hookset_version": r.HookSetVersion,
	}
	if r.ResolutionErr != nil {
		out["error"] = string(r.ResolutionErr.Kind)
	}
	fails := make([]map[string]string, 0, len(r.Failures))
	for _, f := range r.Failures {
		fails = append(fails, map[string]string{"stage": f.Stage, "hook": f.HookID, "code": f.Code})
	}
	out["failures"] = fails
	keys := make([]string, 0, len(r.Intents))
	for _, in := range r.Intents {
		keys = append(keys, in.Op+"|"+in.ObjectKey+"|"+in.Source)
	}
	sort.Strings(keys)
	out["intent_keys"] = keys
	return out
}
