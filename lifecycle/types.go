package lifecycle

import (
	"fmt"
	"time"
)

type (
	ObjectTypeID string
	InstanceID   string
	StateID      string
	TransitionID string
)

type TransitionKind int

const (
	KindActionTransition TransitionKind = iota
	KindTimeTransition
)

// GuardContext is passed to guard predicates during precondition evaluation.
type GuardContext struct {
	store guardStore
	Self  InstanceID
}

// guardStore is implemented by both the lazy Engine and the independent
// Scanner reference model, so one guard declaration drives both. Guards run
// while the implementing model holds its own lock.
type guardStore interface {
	propOf(id InstanceID, key string) (any, bool)
	hasLink(from, to InstanceID) bool
}

// Prop reads one of the guarded instance's own properties.
func (c GuardContext) Prop(key string) (any, bool) {
	if c.store == nil {
		return nil, false
	}
	return c.store.propOf(c.Self, key)
}

// Ref reads a property that names another instance.
func (c GuardContext) Ref(key string) (InstanceID, bool) {
	v, ok := c.Prop(key)
	if !ok {
		return "", false
	}
	switch ref := v.(type) {
	case InstanceID:
		return ref, true
	case string:
		return InstanceID(ref), true
	default:
		return "", false
	}
}

// HasLink reports a directed link from the guarded instance to target.
func (c GuardContext) HasLink(target InstanceID) bool {
	if c.store == nil {
		return false
	}
	return c.store.hasLink(c.Self, target)
}

// PropOf reads a property of a linked/other instance, enabling guards that
// reference attributes of related objects.
func (c GuardContext) PropOf(other InstanceID, key string) (any, bool) {
	if c.store == nil {
		return nil, false
	}
	return c.store.propOf(other, key)
}

type GuardFunc func(c GuardContext) bool

// Effect lists optional state-local side effects applied atomically with a transition.
type Effect struct {
	SetProps    map[string]any
	AddLinks    []InstanceID
	RemoveLinks []InstanceID
}

// CascadeSpec declares a forced transition on another instance once the
// owning time transition fires.
type CascadeSpec struct {
	Target     func(c GuardContext) (InstanceID, bool)
	Transition TransitionID
}

type Transition struct {
	ID       TransitionID
	From     StateID
	To       StateID
	Kind     TransitionKind
	Duration time.Duration
	Guard    GuardFunc
	Cascade  *CascadeSpec
	Effect   *Effect
}

type ObjectType struct {
	ID          ObjectTypeID
	States      []StateID
	Initial     StateID
	Transitions []Transition
}

func (t *ObjectType) Validate() error {
	if t.ID == "" {
		return fmt.Errorf("object type has empty id")
	}
	stateSet := make(map[StateID]bool, len(t.States))
	for _, s := range t.States {
		if s == "" {
			return fmt.Errorf("object type %s declares an empty state id", t.ID)
		}
		if stateSet[s] {
			return fmt.Errorf("object type %s declares duplicate state %s", t.ID, s)
		}
		stateSet[s] = true
	}
	if !stateSet[t.Initial] {
		return fmt.Errorf("object type %s initial state %s is not declared", t.ID, t.Initial)
	}
	ids := make(map[TransitionID]bool, len(t.Transitions))
	timeOut := make(map[StateID]bool)
	for _, tr := range t.Transitions {
		if tr.ID == "" {
			return fmt.Errorf("object type %s declares a transition with empty id", t.ID)
		}
		if ids[tr.ID] {
			return fmt.Errorf("object type %s declares duplicate transition %s", t.ID, tr.ID)
		}
		ids[tr.ID] = true
		if !stateSet[tr.From] || !stateSet[tr.To] {
			return fmt.Errorf("transition %s of %s references undeclared states %s->%s", tr.ID, t.ID, tr.From, tr.To)
		}
		switch tr.Kind {
		case KindTimeTransition:
			if tr.Duration <= 0 {
				return fmt.Errorf("time transition %s of %s requires a positive duration", tr.ID, t.ID)
			}
			// At most one automatic time transition may leave a state, so the
			// lazy chain is deterministic without scanning history.
			if timeOut[tr.From] {
				return fmt.Errorf("state %s of %s has more than one time transition", tr.From, t.ID)
			}
			timeOut[tr.From] = true
			if tr.Cascade != nil && tr.Cascade.Transition == "" {
				return fmt.Errorf("cascade of time transition %s of %s lacks a transition id", tr.ID, t.ID)
			}
		case KindActionTransition:
			// Actions never declare cascades; cross-instance chains are
			// anchored on time transitions only.
			if tr.Cascade != nil {
				return fmt.Errorf("action transition %s of %s must not declare a cascade", tr.ID, t.ID)
			}
		default:
			return fmt.Errorf("transition %s of %s has unknown kind", tr.ID, t.ID)
		}
	}
	return nil
}

func (t *ObjectType) TimeTransitionFrom(s StateID) *Transition {
	for i := range t.Transitions {
		tr := &t.Transitions[i]
		if tr.Kind == KindTimeTransition && tr.From == s {
			return tr
		}
	}
	return nil
}

func (t *ObjectType) FindTransition(id TransitionID) *Transition {
	for i := range t.Transitions {
		if t.Transitions[i].ID == id {
			return &t.Transitions[i]
		}
	}
	return nil
}
