package precheck

import (
	"fmt"
	"sync"
)

const (
	authGateID   = "#authorization-gate"
	objectSnapID = "#object-snapshot"
)

// Engine 是本体平台动作子系统：接受真实调用与版本演进，并提供假设性预检。
type Engine struct {
	mu       sync.Mutex
	registry *Registry
	audit    *AuditLog

	events eventLog

	// 按键分片的时态视图：点查 O(log 该键历史段数)，
	// 与系统全局累计演进次数无关。
	schemas  *TemporalStore[*ParamSchema]
	hooksets *TemporalStore[*HookSet]
	users    *TemporalStore[bool]
	objects  *TemporalStore[any]
	// edgeState 的键是 "from\x00to"，值是该条有向边在各时段是否存在。
	// 一条被反复增删的边只在自己的段历史上追加；写入/点查都不触碰
	// 其它边，也不复制任何邻接表。
	edgeState *TemporalStore[bool]
	// outTargets 记录每个发起节点“历史上出现过的目标”，供权限 BFS 枚举；
	// 集合只增，某条边当前是否存活由 edgeState 的时态点查判定。
	outTargets map[string]map[string]struct{}
}

// eventLog 是分块的全局事件日志：追加 O(1) 摊还，按下标 O(1) 访问，
// 顺序遍历不产生额外复制，避免大切片扩容时的 O(n) 整体搬运。
type eventLog struct {
	chunks [][]Event
}

const eventChunkSize = 1024

func (l *eventLog) append(ev Event) int64 {
	if len(l.chunks) == 0 || len(l.chunks[len(l.chunks)-1]) == eventChunkSize {
		l.chunks = append(l.chunks, make([]Event, 0, eventChunkSize))
	}
	last := len(l.chunks) - 1
	l.chunks[last] = append(l.chunks[last], ev)
	return int64(l.len())
}

func (l *eventLog) len() int {
	if len(l.chunks) == 0 {
		return 0
	}
	return (len(l.chunks)-1)*eventChunkSize + len(l.chunks[len(l.chunks)-1])
}

func (l *eventLog) at(i int) Event {
	return l.chunks[i/eventChunkSize][i%eventChunkSize]
}

func (l *eventLog) snapshot() []Event {
	out := make([]Event, 0, l.len())
	for _, c := range l.chunks {
		out = append(out, c...)
	}
	return out
}

func NewEngine(reg *Registry) *Engine {
	if reg == nil {
		reg = NewRegistry()
	}
	return &Engine{
		registry:   reg,
		audit:      NewAuditLog(),
		schemas:    NewTemporalStore[*ParamSchema](),
		hooksets:   NewTemporalStore[*HookSet](),
		users:      NewTemporalStore[bool](),
		objects:    NewTemporalStore[any](),
		edgeState:  NewTemporalStore[bool](),
		outTargets: map[string]map[string]struct{}{},
	}
}

func (e *Engine) Audit() *AuditLog { return e.audit }

// Events 返回全局事件日志的深拷贝。
func (e *Engine) Events() []Event {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.events.snapshot()
}

// Apply 把一条已发生的真实事件按全局串行顺序追加。时刻必须单调不递减。
func (e *Engine) Apply(ev Event) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	if n := e.events.len(); n > 0 {
		last := e.events.at(n - 1)
		if ev.At < last.At {
			return fmt.Errorf("engine: event at %d is earlier than last event at %d", ev.At, last.At)
		}
	}
	if err := e.route(ev); err != nil {
		return err
	}
	ev.Seq = e.events.append(ev)
	return nil
}

func (e *Engine) route(ev Event) error {
	switch ev.Kind {
	case EvTypeSchema:
		if ev.Schema == nil {
			return fmt.Errorf("engine: %s event missing schema", ev.Kind)
		}
		e.schemas.Put(ev.ActionType, ev.At, ev.Schema)
	case EvHookSet:
		if ev.HookSet == nil {
			return fmt.Errorf("engine: %s event missing hook set", ev.Kind)
		}
		e.hooksets.Put(ev.ActionType, ev.At, ev.HookSet)
	case EvUserAdded:
		e.users.Put(ev.User, ev.At, true)
	case EvEdgeAdded:
		e.toggleEdge(ev.From, ev.To, ev.At, true)
	case EvEdgeRemoved:
		e.toggleEdge(ev.From, ev.To, ev.At, false)
	case EvObjectUpsert:
		e.objects.Put(ev.ObjectKey, ev.At, ev.Value)
	case EvObjectDelete:
		e.objects.Remove(ev.ObjectKey, ev.At)
	case EvHistoryGap:
		e.markGap(ev)
	case EvRealCall:
		// 真实调用事件本身是日志事实；其允许后的副作用由 Execute
		// 以独立的对象事件追加，重演时只需处理那些对象事件。
	default:
		return fmt.Errorf("engine: unknown event kind %q", ev.Kind)
	}
	return nil
}

func (e *Engine) markGap(ev Event) {
	switch ev.GapDomain {
	case GapDomainSchema:
		e.schemas.Gap(ev.GapKey, ev.GapStart, ev.GapEnd)
	case GapDomainHooks:
		e.hooksets.Gap(ev.GapKey, ev.GapStart, ev.GapEnd)
	case GapDomainUser:
		e.users.Gap(ev.GapKey, ev.GapStart, ev.GapEnd)
	case GapDomainEdges:
		e.edgeState.Gap(ev.GapKey, ev.GapStart, ev.GapEnd)
	case GapDomainObject:
		e.objects.Gap(ev.GapKey, ev.GapStart, ev.GapEnd)
	default:
		panic(fmt.Sprintf("engine: unknown gap domain %q", ev.GapDomain))
	}
}

func (e *Engine) toggleEdge(from, to string, at Moment, add bool) {
	if add {
		e.edgeState.Put(edgeKey(from, to), at, true)
	} else {
		e.edgeState.Remove(edgeKey(from, to), at)
	}
	if e.outTargets[from] == nil {
		e.outTargets[from] = map[string]struct{}{}
	}
	e.outTargets[from][to] = struct{}{}
}

func edgeKey(from, to string) string { return from + "\x00" + to }

func (e *Engine) resetStats() {
	e.schemas.ResetStats()
	e.hooksets.ResetStats()
	e.users.ResetStats()
	e.objects.ResetStats()
	e.edgeState.ResetStats()
}

func (e *Engine) metrics() map[string]int64 {
	sum := func(get func() TemporalStats) TemporalStats {
		return get()
	}
	return map[string]int64{
		"schema_point_calls":    sum(e.schemas.Stats).PointCalls,
		"hookset_point_calls":   sum(e.hooksets.Stats).PointCalls,
		"user_point_calls":      sum(e.users.Stats).PointCalls,
		"object_point_calls":    sum(e.objects.Stats).PointCalls,
		"edge_point_calls":      sum(e.edgeState.Stats).PointCalls,
		"edge_segment_checks":   sum(e.edgeState.Stats).SegmentChecks,
		"schema_segment_checks": sum(e.schemas.Stats).SegmentChecks,
		"hook_segment_checks":   sum(e.hooksets.Stats).SegmentChecks,
	}
}

// Execute 真实执行一次动作：演算通过则把意图落地为对象状态，并追加真实调用事件。
func (e *Engine) Execute(at Moment, actionType, caller string, params map[string]any, agg FailureAggregation) (*PrecheckResult, error) {
	req := PrecheckRequest{At: at, ActionType: actionType, Caller: caller, Params: params, Aggregation: agg}
	result, plan, err := e.evaluate(req, false)
	if err != nil {
		return nil, err
	}
	ev := Event{
		At:         at,
		Kind:       EvRealCall,
		ActionType: actionType,
		Caller:     caller,
		Params:     deepCopyParams(params),
		Outcome:    string(result.Verdict),
	}
	e.mu.Lock()
	ev.Seq = e.events.append(ev)
	if result.Verdict == VerdictAllowed {
		for _, intent := range result.Intents {
			effect := Event{At: at, Kind: EvObjectUpsert, ObjectKey: intent.ObjectKey, Value: intent.Value}
			if intent.Op == "delete" {
				effect.Kind = EvObjectDelete
			}
			_ = e.route(effect)
			effect.Seq = e.events.append(effect)
		}
	}
	e.mu.Unlock()
	_ = plan
	return result, nil
}

// Precheck 在不改变任何对象状态、钩子版本记录或权限关系的前提下，
// 对历史时刻做一次假设性重新预检。审计日志是唯一允许的可观察输出。
func (e *Engine) Precheck(req PrecheckRequest) (*PrecheckResult, error) {
	result, _, err := e.evaluate(req, true)
	return result, err
}
