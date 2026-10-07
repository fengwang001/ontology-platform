package ontology

import (
	"context"
	"sync"
)

// Logger receives one structured line per decision (input, output, basis).
type Logger interface {
	LogDecision(d Decision)
}

// Gateway is the permission propagation gateway. All methods are safe for
// concurrent use; mutation batches appear to apply in a single global serial
// step, and rejected batches leave both graph and answers untouched.
type Gateway struct {
	mu       sync.RWMutex
	depthCap int
	current  *state
	log      Logger
}

// NewGateway constructs an empty gateway using MaxPropagationDepth as the
// platform depth cap.
func NewGateway(logger Logger) *Gateway {
	empty := newState(MaxPropagationDepth)
	empty.index = buildIndexFor(empty)
	return &Gateway{depthCap: MaxPropagationDepth, current: empty, log: logger}
}

// snapshot returns the currently published immutable state.
func (g *Gateway) snapshot() *state {
	g.mu.RLock()
	s := g.current
	g.mu.RUnlock()
	return s
}

// commit applies a changeset atomically. The candidate snapshot is fully
// validated before publication; on any error the published snapshot and every
// observable answer stay unchanged.
func (g *Gateway) commit(changes []Change) error {
	g.mu.Lock()
	defer g.mu.Unlock()

	candidate := g.current.clone()
	// Track override declarations within this same batch so that two
	// irreconcilable rules on one target are rejected instead of silently
	// last-one-winning.
	batchModes := map[string]OverrideMode{}

	for _, change := range changes {
		switch change.Kind {
		case AddObjects:
			for _, name := range change.Names {
				candidate.objects[name] = true
			}
		case UpsertLinkChange:
			if err := validateLinkShape(change.Link, candidate.depthCap); err != nil {
				return err
			}
			candidate.links[change.Link.Name] = change.Link
		case DeleteLinkChange:
			delete(candidate.links, change.Link.Name)
		case SetOverrideChange:
			if prev, exists := batchModes[change.Object]; exists && prev != change.Override {
				return newError(KindConflictingOverride,
					"target "+change.Object+" receives two incompatible overrides in one batch")
			}
			batchModes[change.Object] = change.Override
			candidate.overrides[change.Object] = change.Override
		case ClearOverrideChange:
			delete(candidate.overrides, change.Object)
		case ApplyGrantChange:
			candidate.grants[grantKey{
				subject: change.Grant.Subject,
				object:  change.Grant.Object,
				action:  change.Grant.Action,
			}] = change.Grant.Effect
		case RevokeGrantChange:
			delete(candidate.grants, grantKey{
				subject: change.Grant.Subject,
				object:  change.Grant.Object,
				action:  change.Grant.Action,
			})
		}
	}

	if err := candidate.validate(); err != nil {
		return err
	}
	g.current = candidate
	return nil
}

func validateLinkShape(link LinkType, depthCap int) error {
	if link.PropagationDepth < 0 {
		return newError(KindInvalidDepth, "link "+link.Name+" declares negative depth")
	}
	if link.PropagationDepth > depthCap {
		return newError(KindInvalidDepth, "link "+link.Name+" depth exceeds platform cap")
	}
	return nil
}

// AddObjectTypes registers object types; unknown names referenced anywhere
// yield KindObjectNotFound.
func (g *Gateway) AddObjectTypes(ctx context.Context, names ...string) error {
	return g.commit([]Change{{Kind: AddObjects, Names: names}})
}

// UpsertLink adds or replaces a directed link type. Invalid depth or unknown
// endpoints are rejected atomically.
func (g *Gateway) UpsertLink(ctx context.Context, link LinkType) error {
	return g.commit([]Change{{Kind: UpsertLinkChange, Link: link}})
}

// DeleteLink removes a link type.
func (g *Gateway) DeleteLink(ctx context.Context, name string) error {
	return g.commit([]Change{{Kind: DeleteLinkChange, Link: LinkType{Name: name}}})
}

// SetOverride declares (or replaces) an object type's override mode.
func (g *Gateway) SetOverride(ctx context.Context, object string, mode OverrideMode) error {
	return g.commit([]Change{{Kind: SetOverrideChange, Object: object, Override: mode}})
}

// ClearOverride removes an object type's override declaration.
func (g *Gateway) ClearOverride(ctx context.Context, object string) error {
	return g.commit([]Change{{Kind: ClearOverrideChange, Object: object}})
}

// ApplyGrant adds or replaces an explicit authorization record.
func (g *Gateway) ApplyGrant(ctx context.Context, grant Grant) error {
	return g.commit([]Change{{Kind: ApplyGrantChange, Grant: grant}})
}

// RevokeGrant removes an explicit authorization record.
func (g *Gateway) RevokeGrant(ctx context.Context, grant Grant) error {
	return g.commit([]Change{{Kind: RevokeGrantChange, Grant: grant}})
}

// Apply performs several changes atomically. Either all apply in one serial
// step or none do.
func (g *Gateway) Apply(ctx context.Context, changes ...Change) error {
	return g.commit(changes)
}

// Decide computes the final permission of subject for action on objectType.
// Explicit records on the target always dominate propagated results; an
// explicit deny on the target therefore dominates an upstream allow.
func (g *Gateway) Decide(ctx context.Context, subject, object, action string) (Decision, error) {
	s := g.snapshot()
	d := Decision{Subject: subject, Object: object, Action: action}
	if !s.objects[object] {
		return d, newError(KindObjectNotFound, "object type missing: "+object)
	}

	// Explicit direct authorization on the target always wins.
	if effect, ok := s.grants[grantKey{subject: subject, object: object, action: action}]; ok {
		d.Allowed = effect == Allow
		if !d.Allowed {
			d.DenyReason = DenialExplicit
		}
		d.Basis = []string{"explicit@" + object}
		g.logDecision(d)
		return d, nil
	}

	// Union of every delivered path originating at the subject's granted
	// object types. Each path is evaluated independently; deny wins the union.
	// Overrides replace (never merge with) the upstream union inside the
	// structural index.
	inflow := s.index.inflow[object]
	deliveredAllow := false
	deliveredDeny := false
	var bases []string
	obstructedBlocked := false
	obstructedReplaced := false
	obstructedDepth := false
	blockedBasis, replacedBasis, depthBasis := "", "", ""

	for key, effect := range s.grants {
		if key.subject != subject || key.action != action {
			continue
		}
		entry, reachable := inflow[key.object]
		if !reachable {
			continue
		}
		if entry.has(bitDeliver) {
			if effect == Allow {
				deliveredAllow = true
			} else {
				deliveredDeny = true
			}
			bases = append(bases, entry.basis)
		}
		if entry.has(bitBlocked) && !obstructedBlocked {
			obstructedBlocked = true
			blockedBasis = entry.basis
		}
		if entry.has(bitReplaced) && !obstructedReplaced {
			obstructedReplaced = true
			replacedBasis = entry.basis
		}
		if entry.has(bitDepth) && !obstructedDepth {
			obstructedDepth = true
			depthBasis = entry.basis
		}
	}

	switch {
	case deliveredDeny:
		d.DenyReason = DenialExplicit
	case deliveredAllow:
		d.Allowed = true
		d.Basis = bases
	case obstructedBlocked:
		d.DenyReason = DenialBlocked
		d.Basis = []string{blockedBasis}
	case obstructedReplaced:
		d.DenyReason = DenialReplaced
		d.Basis = []string{replacedBasis}
	case obstructedDepth:
		d.DenyReason = DenialDepth
		d.Basis = []string{depthBasis}
	default:
		d.DenyReason = DenialNone
	}
	g.logDecision(d)
	return d, nil
}

func (g *Gateway) logDecision(d Decision) {
	if g.log != nil {
		g.log.LogDecision(d)
	}
}
