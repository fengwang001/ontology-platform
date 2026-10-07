// Package refmodel 是独立于 ontology 内部实现的分层串行参照模型。
package refmodel

import (
	"fmt"

	"ontology/ontology"
)

// ObjectState 是参照模型重放得到的单实例终态。
type ObjectState struct {
	Version int64
	Clock   uint64
	Props   map[string]any
}

// Preemption 记录一次抢占终止及其证据（造成抢占的更高权限写入）。
type Preemption struct {
	ActionID   string
	ByActionID string
	Seq        uint64 // 抢占终止事件的日志位置
	BySeq      uint64 // 证据写入的日志位置，严格小于 Seq
}

// Report 是参照模型的验证结论。
type Report struct {
	Final       map[string]ObjectState // 按对象 ID 的重放终态
	Preemptions []Preemption
	Errors      []string
}

func (r *Report) errf(format string, args ...any) {
	r.Errors = append(r.Errors, fmt.Sprintf(format, args...))
}

// objectReplay 是参照模型内部的单实例重放状态。
type objectReplay struct {
	version int64
	clock   uint64
	props   map[string]any
	// 独立实现：直接用 map 加线性扫描，不复用 ontology 的水位线结构。
	high map[ontology.Priority]int64
}

func (o *objectReplay) higherWriteBeyond(p ontology.Priority, baseline int64) bool {
	for wp, v := range o.high {
		if wp > p && v > baseline {
			return true
		}
	}
	return false
}

type commitRecord struct {
	actionID string
	seq      uint64
	priority ontology.Priority
	version  int64
}

// Validate 串行重放 events（判定日志），并交叉核验 results（动作的完整判定记录）。
// initial 提供每个对象的初始属性；actions 提供每个动作 ID 的定义。
func Validate(initial map[string]map[string]any, events []ontology.Event, results []ontology.Result, actions map[string]ontology.Action) *Report {
	rep := &Report{Final: make(map[string]ObjectState)}
	objects := make(map[string]*objectReplay)
	get := func(id string) *objectReplay {
		o := objects[id]
		if o == nil {
			props := make(map[string]any)
			for k, v := range initial[id] {
				props[k] = v
			}
			o = &objectReplay{props: props, high: make(map[ontology.Priority]int64)}
			objects[id] = o
		}
		return o
	}

	commitsByObject := make(map[string][]commitRecord)
	eventsByAction := make(map[string][]ontology.Event)

	for i, ev := range events {
		if ev.Seq != uint64(i) {
			rep.errf("event seq gap: index %d has seq %d", i, ev.Seq)
		}
		eventsByAction[ev.ActionID] = append(eventsByAction[ev.ActionID], ev)
		o := get(ev.ObjectID)
		switch ev.Kind {
		case ontology.EventConflict:
			if o.version == ev.Baseline {
				rep.errf("event %d: conflict but version %d matches baseline", ev.Seq, o.version)
			}
			if o.higherWriteBeyond(ev.Priority, ev.Baseline) {
				rep.errf("event %d: action %s conflicted but should have been preempted", ev.Seq, ev.ActionID)
			}
		case ontology.EventPreempted:
			if !o.higherWriteBeyond(ev.Priority, ev.Baseline) {
				rep.errf("event %d: action %s preempted without higher-priority write beyond baseline", ev.Seq, ev.ActionID)
				continue
			}
			var evidence *commitRecord
			for j := range commitsByObject[ev.ObjectID] {
				c := &commitsByObject[ev.ObjectID][j]
				if c.priority > ev.Priority && c.version > ev.Baseline {
					if evidence == nil || c.seq < evidence.seq {
						evidence = c
					}
				}
			}
			if evidence == nil {
				rep.errf("event %d: preemption evidence missing in commit log", ev.Seq)
				continue
			}
			if evidence.seq >= ev.Seq {
				rep.errf("event %d: preemption evidence seq %d not earlier", ev.Seq, evidence.seq)
			}
			rep.Preemptions = append(rep.Preemptions, Preemption{
				ActionID: ev.ActionID, ByActionID: evidence.actionID,
				Seq: ev.Seq, BySeq: evidence.seq,
			})
		case ontology.EventCommit:
			if o.higherWriteBeyond(ev.Priority, ev.Baseline) {
				rep.errf("event %d: action %s committed despite higher-priority write beyond baseline", ev.Seq, ev.ActionID)
			}
			if o.version != ev.Baseline {
				rep.errf("event %d: action %s committed on stale baseline %d, current version %d", ev.Seq, ev.ActionID, ev.Baseline, o.version)
				continue
			}
			for k, v := range ev.Mutation.Set {
				o.props[k] = v
			}
			for _, k := range ev.Mutation.Del {
				delete(o.props, k)
			}
			o.version++
			o.clock++
			if o.version != ev.Version {
				rep.errf("event %d: replayed version %d != logged %d", ev.Seq, o.version, ev.Version)
			}
			if o.clock != ev.Clock {
				rep.errf("event %d: replayed clock %d != logged %d", ev.Seq, o.clock, ev.Clock)
			}
			if ev.Version > o.high[ev.Priority] {
				o.high[ev.Priority] = ev.Version
			}
			commitsByObject[ev.ObjectID] = append(commitsByObject[ev.ObjectID], commitRecord{
				actionID: ev.ActionID, seq: ev.Seq, priority: ev.Priority, version: ev.Version,
			})
		}
	}

	// 交叉核验每个动作的完整判定记录。
	for _, res := range results {
		a, ok := actions[res.ActionID]
		if !ok {
			rep.errf("result for unknown action %s", res.ActionID)
			continue
		}
		evs := eventsByAction[res.ActionID]
		if len(evs) != len(res.Attempts) {
			rep.errf("action %s: %d attempts but %d events", res.ActionID, len(res.Attempts), len(evs))
			continue
		}
		conflicts := 0
		for i, att := range res.Attempts {
			ev := evs[i]
			if ev.Priority != a.Priority {
				rep.errf("action %s attempt %d: event priority %d != action priority %d", res.ActionID, i, ev.Priority, a.Priority)
			}
			if ev.Baseline != att.Baseline {
				rep.errf("action %s attempt %d: event baseline %d != recorded %d", res.ActionID, i, ev.Baseline, att.Baseline)
			}
			if i > 0 && att.Baseline <= res.Attempts[i-1].Baseline {
				rep.errf("action %s attempt %d: baseline %d not advanced beyond previous %d", res.ActionID, i, att.Baseline, res.Attempts[i-1].Baseline)
			}
			var want ontology.EventKind
			switch att.Outcome {
			case ontology.AttemptCommitted:
				want = ontology.EventCommit
			case ontology.AttemptConflict:
				want = ontology.EventConflict
			case ontology.AttemptPreempted:
				want = ontology.EventPreempted
			}
			if ev.Kind != want {
				rep.errf("action %s attempt %d: event kind %v != outcome %v", res.ActionID, i, ev.Kind, att.Outcome)
			}
			last := i == len(res.Attempts)-1
			switch att.Outcome {
			case ontology.AttemptConflict:
				conflicts++
				if last && res.Final != ontology.RetriesExhausted {
					rep.errf("action %s: last attempt conflict but final %v", res.ActionID, res.Final)
				}
			case ontology.AttemptCommitted:
				if !last || res.Final != ontology.Committed {
					rep.errf("action %s: committed attempt not final or final %v", res.ActionID, res.Final)
				}
				if res.Version != ev.Version {
					rep.errf("action %s: result version %d != commit event version %d", res.ActionID, res.Version, ev.Version)
				}
			case ontology.AttemptPreempted:
				if !last || res.Final != ontology.Preempted {
					rep.errf("action %s: preempted attempt not final or final %v", res.ActionID, res.Final)
				}
			}
			if !last && att.Outcome != ontology.AttemptConflict {
				rep.errf("action %s attempt %d: non-conflict outcome %v before last attempt", res.ActionID, i, att.Outcome)
			}
		}
		if res.Final == ontology.RetriesExhausted {
			if conflicts != a.MaxRetries+1 {
				rep.errf("action %s: exhausted with %d conflicts, budget %d", res.ActionID, conflicts, a.MaxRetries)
			}
		} else if conflicts > a.MaxRetries {
			rep.errf("action %s: %d conflicts exceed budget %d without exhaustion", res.ActionID, conflicts, a.MaxRetries)
		}
	}

	for id, o := range objects {
		props := make(map[string]any, len(o.props))
		for k, v := range o.props {
			props[k] = v
		}
		rep.Final[id] = ObjectState{Version: o.version, Clock: o.clock, Props: props}
	}
	return rep
}
