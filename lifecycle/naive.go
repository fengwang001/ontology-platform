package lifecycle

import (
	"fmt"
	"time"
)

// naiveEvent 是朴素模型记录的一条已接受事件。
// 朴素模型不做任何物化：每次可见性/状态判定都从首条事件
// 开始逐条回放，其触及的历史记录条数随历史长度线性增长，
// 刻意与生产实现的 O(1) 物化状态形成对照。
type naiveEvent struct {
	time     time.Time
	kind     string // "delete" | "undo" | "freeze" | "archive"
	deadline time.Time
}

type naiveObject struct {
	attrs  Attrs
	events []naiveEvent
}

// NaiveReference 是朴素参考实现：保留全部转换历史，
// 每次判定都从头回放历史。仅供随机对账测试使用。
type NaiveReference struct {
	clock   Clock
	objects map[string]*naiveObject
	edges   map[string][]Edge
}

func NewNaiveReference(clock Clock) *NaiveReference {
	return &NaiveReference{
		clock:   clock,
		objects: make(map[string]*naiveObject),
		edges:   make(map[string][]Edge),
	}
}

// stateAt 逐条回放事件：先依次应用全部事件（每条应用前结算到
// 其自身时刻），再结算到 now。返回结算前状态、结算后状态与
// [宽限截止, 冻结截止]。
func (n *NaiveReference) stateAt(o *naiveObject, now time.Time) (State, State, [2]time.Time) {
	state := StateAlive
	var graceDeadline, freezeDeadline time.Time
	for _, e := range o.events {
		state, graceDeadline, freezeDeadline = naiveSettle(state, graceDeadline, freezeDeadline, e.time)
		switch e.kind {
		case "delete":
			state = StateGrace
			graceDeadline = e.deadline
		case "undo":
			if state == StateGrace {
				state = StateAlive
				graceDeadline = time.Time{}
			}
		case "freeze":
			state = StateFrozen
			freezeDeadline = e.deadline
		case "archive":
			state = StateArchived
			graceDeadline = time.Time{}
			freezeDeadline = time.Time{}
		}
	}
	beforeSettle := state
	state, graceDeadline, freezeDeadline = naiveSettle(state, graceDeadline, freezeDeadline, now)
	return beforeSettle, state, [2]time.Time{graceDeadline, freezeDeadline}
}

func naiveSettle(state State, grace, freeze time.Time, now time.Time) (State, time.Time, time.Time) {
	switch state {
	case StateGrace:
		if !grace.After(now) {
			return StateArchived, time.Time{}, time.Time{}
		}
	case StateFrozen:
		if !freeze.After(now) {
			return StateArchived, time.Time{}, time.Time{}
		}
	}
	return state, grace, freeze
}

func copyAttrs(a Attrs) Attrs {
	out := make(Attrs, len(a))
	for k, v := range a {
		out[k] = v
	}
	return out
}

func (n *NaiveReference) CreateObject(id string, attrs Attrs) error {
	if _, ok := n.objects[id]; ok {
		return ErrDuplicateObject
	}
	n.objects[id] = &naiveObject{attrs: copyAttrs(attrs)}
	return nil
}

func (n *NaiveReference) AddEdge(e Edge) error {
	if _, ok := n.objects[e.SourceID]; !ok {
		return fmt.Errorf("%w: source %q", ErrObjectNotFound, e.SourceID)
	}
	if _, ok := n.objects[e.TargetID]; !ok {
		return fmt.Errorf("%w: target %q", ErrObjectNotFound, e.TargetID)
	}
	n.edges[e.SourceID] = append(n.edges[e.SourceID], e)
	return nil
}

func (n *NaiveReference) Delete(id string, actor Identity, graceDeadline time.Time) error {
	now := n.clock.Now()
	o, ok := n.objects[id]
	if !ok {
		return ErrObjectNotFound
	}
	_, state, _ := n.stateAt(o, now)
	if state != StateAlive {
		return ErrInvalidTransition
	}
	if !graceDeadline.After(now) {
		return ErrInvalidTime
	}
	o.events = append(o.events, naiveEvent{time: now, kind: "delete", deadline: graceDeadline})
	return nil
}

func (n *NaiveReference) Undo(id string, actor Identity) error {
	now := n.clock.Now()
	o, ok := n.objects[id]
	if !ok {
		return ErrObjectNotFound
	}
	_, state, _ := n.stateAt(o, now)
	switch state {
	case StateGrace:
		o.events = append(o.events, naiveEvent{time: now, kind: "undo"})
		return nil
	case StateFrozen:
		return ErrFrozenNotExpired
	default:
		return ErrInvalidTransition
	}
}

func (n *NaiveReference) Freeze(id string, actor Identity, d time.Duration) error {
	now := n.clock.Now()
	o, ok := n.objects[id]
	if !ok {
		return ErrObjectNotFound
	}
	_, state, _ := n.stateAt(o, now)
	if state != StateAlive {
		return ErrInvalidTransition
	}
	if d <= 0 {
		return ErrInvalidTime
	}
	o.events = append(o.events, naiveEvent{
		time: now, kind: "freeze", deadline: now.Add(d),
	})
	return nil
}

func (n *NaiveReference) Archive(id string, actor Identity) error {
	now := n.clock.Now()
	o, ok := n.objects[id]
	if !ok {
		return ErrObjectNotFound
	}
	_, state, _ := n.stateAt(o, now)
	switch {
	case state == StateArchived:
		// 归档是终态：显式归档幂等成功（含恰好到期触发的结算）。
		return nil
	case state == StateFrozen:
		return ErrFrozenNotExpired
	default:
		return ErrInvalidTransition
	}
}

func (n *NaiveReference) GetView(id string, actor Identity) View {
	now := n.clock.Now()
	o, ok := n.objects[id]
	if !ok {
		return View{ID: id}
	}
	_, state, deadlines := n.stateAt(o, now)
	view := View{ID: id, Exists: true, State: state}
	switch actor {
	case IdentityUser:
		view.Visible = state == StateAlive
	case IdentityAdmin:
		view.Visible = true
		view.GraceDeadline = deadlines[0]
		view.FreezeDeadline = deadlines[1]
	default:
		return view
	}
	if !view.Visible {
		return view
	}
	if actor == IdentityAdmin && state == StateFrozen {
		return view
	}
	view.Attrs = copyAttrs(o.attrs)
	return view
}

func (n *NaiveReference) ViewEdges(sourceID string, actor Identity) []Edge {
	now := n.clock.Now()
	o, ok := n.objects[sourceID]
	if !ok {
		return nil
	}
	_, state, _ := n.stateAt(o, now)
	visible := false
	switch actor {
	case IdentityUser:
		visible = state == StateAlive
	case IdentityAdmin:
		visible = true
	}
	if !visible {
		return nil
	}
	src := n.edges[sourceID]
	out := make([]Edge, len(src))
	copy(out, src)
	return out
}
