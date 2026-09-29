package delegation

import (
	"context"
	"fmt"
	"sync"
	"time"
)

// rootGrant is an original, non-revocable authority held by a principal.
type rootGrant struct {
	canDelegate bool
}

// Service stores delegations and answers permission queries. It is safe
// for concurrent use; a single RWMutex makes each operation atomic, so
// concurrent grants and evaluations always observe a consistent graph.
type Service struct {
	mu    sync.RWMutex
	now   func() time.Time
	log   Logger
	next  int64
	edges []*Delegation
	// roots[principal][permission] describes original authorities.
	roots map[Principal]map[Permission]rootGrant
}

// NewService creates a delegation service seeded with root authorities.
// The nested bool is the root's CanDelegate flag. If now is nil the
// wall clock is used; if logger is nil audit logging is disabled.
func NewService(logger Logger, now func() time.Time, root map[Principal]map[Permission]bool) *Service {
	if now == nil {
		now = time.Now
	}
	s := &Service{
		now:   now,
		log:   logger,
		next:  1,
		roots: make(map[Principal]map[Permission]rootGrant),
	}
	for p, perms := range root {
		for perm, canDelegate := range perms {
			s.GrantAuthority(p, perm, canDelegate)
		}
	}
	return s
}

// GrantAuthority seeds an original (root) authority. Root authorities
// never expire and cannot be revoked through the delegation graph.
func (s *Service) GrantAuthority(p Principal, perm Permission, canDelegate bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	perms := s.roots[p]
	if perms == nil {
		perms = make(map[Permission]rootGrant)
		s.roots[p] = perms
	}
	// A later seed may only broaden, never silently narrow, an authority.
	cur := perms[perm]
	perms[perm] = rootGrant{canDelegate: cur.canDelegate || canDelegate}
}

// live reports whether an edge currently counts as part of the graph.
func (s *Service) live(e *Delegation) bool {
	return !e.Revoked && (e.ExpiresAt.IsZero() || !e.ExpiresAt.Before(s.now()))
}

// Grant registers a delegation.
//
// A grant is refused (state left untouched) with:
//   - ReasonNotDelegatable: the delegator currently holds the permission
//     only through a chain whose last edge sets CanDelegate=false.
//   - ReasonCycle: the edge would close a directed cycle of the same
//     permission among forwardable edges.
//
// An edge whose delegator has no permission yet is accepted as a
// "dangling" edge: it activates automatically once an upstream chain is
// registered. This makes the final permission set independent of the
// registration order for any acyclic set of grants.
func (s *Service) Grant(ctx context.Context, req GrantRequest) (*Delegation, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if req.Delegator == req.Delegatee {
		err := &RejectError{
			Reason: ReasonCycle, Delegator: req.Delegator, Delegatee: req.Delegatee,
			Permission: req.Permission, Detail: "self-delegation forms a cycle",
		}
		s.logGrant(nil, "rejected", err.Error(), req)
		return nil, err
	}

	dec := s.canDelegateLocked(req.Delegator, req.Permission)
	if !dec.Allowed && dec.Reason != ReasonNoDelegation {
		// The delegator is connected to an authority but is not allowed
		// to forward the permission (expiry/revocation/non-forwardable).
		err := &RejectError{
			Reason: ReasonNotDelegatable, Delegator: req.Delegator, Delegatee: req.Delegatee,
			Permission: req.Permission, Detail: string(dec.Reason),
		}
		s.logGrant(nil, "rejected", err.Error(), req)
		return nil, err
	}

	if s.createsCycleLocked(req) {
		err := &RejectError{
			Reason: ReasonCycle, Delegator: req.Delegator, Delegatee: req.Delegatee,
			Permission: req.Permission,
			Detail:     fmt.Sprintf("path %s -> %s already exists", req.Delegatee, req.Delegator),
		}
		s.logGrant(nil, "rejected", err.Error(), req)
		return nil, err
	}

	d := &Delegation{
		ID:          s.next,
		Delegator:   req.Delegator,
		Delegatee:   req.Delegatee,
		Permission:  req.Permission,
		ExpiresAt:   req.ExpiresAt,
		CanDelegate: req.CanDelegate,
		CreatedAt:   s.now(),
	}
	s.next++
	s.edges = append(s.edges, d)
	s.logGrant(d, "accepted", "registered", req)
	return d, nil
}

// Revoke revokes the edge and every edge reachable downstream of its
// delegatee for the same permission (cascading chain invalidation).
func (s *Service) Revoke(ctx context.Context, id int64) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	var start *Delegation
	for _, e := range s.edges {
		if e.ID == id {
			start = e
			break
		}
	}
	if start == nil {
		return fmt.Errorf("delegation %d not found", id)
	}

	perm := start.Permission
	revoked := map[int64]bool{start.ID: true}
	frontier := []Principal{start.Delegatee}
	var invalidated []*Delegation
	for len(frontier) > 0 {
		from := frontier[len(frontier)-1]
		frontier = frontier[:len(frontier)-1]
		for _, e := range s.edges {
			if e.Permission != perm || e.Revoked || revoked[e.ID] {
				continue
			}
			if e.Delegator == from {
				e.Revoked = true
				revoked[e.ID] = true
				invalidated = append(invalidated, e)
				frontier = append(frontier, e.Delegatee)
			}
		}
	}
	start.Revoked = true
	for _, e := range invalidated {
		s.logRevoke(e)
	}
	s.logRevoke(start)
	return nil
}

// HasPermission reports whether subject may currently exercise perm.
func (s *Service) HasPermission(ctx context.Context, subject Principal, perm Permission) Decision {
	s.mu.RLock()
	defer s.mu.RUnlock()
	dec := s.hasPermissionLocked(subject, perm)
	s.logEvaluate(subject, perm, dec)
	return dec
}

// CanDelegate reports whether subject may currently pass perm onward.
func (s *Service) CanDelegate(ctx context.Context, subject Principal, perm Permission) Decision {
	s.mu.RLock()
	defer s.mu.RUnlock()
	dec := s.canDelegateLocked(subject, perm)
	s.logEvaluate(subject, perm, dec)
	return dec
}

// ActiveDelegations returns a snapshot copy of non-revoked delegations.
func (s *Service) ActiveDelegations() []*Delegation {
	s.mu.RLock()
	defer s.mu.RUnlock()
	var out []*Delegation
	for _, e := range s.edges {
		if !e.Revoked {
			cp := *e
			out = append(out, &cp)
		}
	}
	return out
}

// hasPermissionLocked performs a BFS from subject over reversed live
// edges, searching for a root authority.
func (s *Service) hasPermissionLocked(subject Principal, perm Permission) Decision {
	if _, ok := s.roots[subject][perm]; ok {
		return Decision{Allowed: true, Reason: ReasonRootAuthority}
	}

	visited := map[Principal]bool{subject: true}
	frontier := []Principal{subject}
	sawExpired, sawRevoked := false, false
	for len(frontier) > 0 {
		node := frontier[0]
		frontier = frontier[1:]
		for _, e := range s.edges {
			if e.Permission != perm || e.Delegatee != node {
				continue
			}
			switch {
			case e.Revoked:
				sawRevoked = true
				continue
			case !e.ExpiresAt.IsZero() && e.ExpiresAt.Before(s.now()):
				sawExpired = true
				continue
			}
			if _, ok := s.roots[e.Delegator][perm]; ok {
				return Decision{Allowed: true, Reason: ReasonDelegated}
			}
			if !visited[e.Delegator] {
				visited[e.Delegator] = true
				frontier = append(frontier, e.Delegator)
			}
		}
	}
	switch {
	case sawRevoked:
		return Decision{Allowed: false, Reason: ReasonChainRevoked}
	case sawExpired:
		return Decision{Allowed: false, Reason: ReasonChainExpired}
	default:
		return Decision{Allowed: false, Reason: ReasonNoDelegation}
	}
}

// canDelegateLocked is like hasPermissionLocked but requires the root
// and every edge along a chain to permit onward delegation.
func (s *Service) canDelegateLocked(subject Principal, perm Permission) Decision {
	if g, ok := s.roots[subject][perm]; ok {
		if g.canDelegate {
			return Decision{Allowed: true, Reason: ReasonRootAuthority}
		}
		return Decision{Allowed: false, Reason: ReasonNotForwardable}
	}

	type node struct {
		p           Principal
		forwardable bool
	}
	visited := map[Principal]bool{subject: true}
	frontier := []node{{p: subject, forwardable: true}}
	sawExpired, sawRevoked, sawBlocked := false, false, false
	for len(frontier) > 0 {
		cur := frontier[0]
		frontier = frontier[1:]
		for _, e := range s.edges {
			if e.Permission != perm || e.Delegatee != cur.p {
				continue
			}
			switch {
			case e.Revoked:
				sawRevoked = true
				continue
			case !e.ExpiresAt.IsZero() && e.ExpiresAt.Before(s.now()):
				sawExpired = true
				continue
			}
			canPass := cur.forwardable && e.CanDelegate
			if g, ok := s.roots[e.Delegator][perm]; ok {
				if g.canDelegate && canPass {
					return Decision{Allowed: true, Reason: ReasonDelegated}
				}
				sawBlocked = true
				continue
			}
			if !visited[e.Delegator] {
				visited[e.Delegator] = true
				frontier = append(frontier, node{p: e.Delegator, forwardable: canPass})
			}
		}
	}
	switch {
	case sawBlocked:
		return Decision{Allowed: false, Reason: ReasonNotForwardable}
	case sawRevoked:
		return Decision{Allowed: false, Reason: ReasonChainRevoked}
	case sawExpired:
		return Decision{Allowed: false, Reason: ReasonChainExpired}
	default:
		return Decision{Allowed: false, Reason: ReasonNoDelegation}
	}
}

// createsCycleLocked asks whether delegatee can already reach delegator
// over live, forwardable edges of the same permission.
func (s *Service) createsCycleLocked(req GrantRequest) bool {
	visited := map[Principal]bool{req.Delegatee: true}
	frontier := []Principal{req.Delegatee}
	for len(frontier) > 0 {
		from := frontier[len(frontier)-1]
		frontier = frontier[:len(frontier)-1]
		if from == req.Delegator {
			return true
		}
		for _, e := range s.edges {
			if e.Permission != req.Permission || !s.live(e) || !e.CanDelegate {
				continue
			}
			if e.Delegator == from && !visited[e.Delegatee] {
				visited[e.Delegatee] = true
				frontier = append(frontier, e.Delegatee)
			}
		}
	}
	return false
}

func (s *Service) logGrant(d *Delegation, decision, reason string, req GrantRequest) {
	if s.log == nil {
		return
	}
	if d == nil {
		d = &Delegation{
			Delegator: req.Delegator, Delegatee: req.Delegatee, Permission: req.Permission,
			ExpiresAt: req.ExpiresAt, CanDelegate: req.CanDelegate, CreatedAt: s.now(),
		}
	}
	s.log.LogGrant(context.Background(), d, decision, reason)
}

func (s *Service) logRevoke(d *Delegation) {
	if s.log != nil {
		s.log.LogRevoke(context.Background(), d)
	}
}

func (s *Service) logEvaluate(subject Principal, perm Permission, dec Decision) {
	if s.log != nil {
		s.log.LogEvaluate(context.Background(), subject, perm, dec.Allowed, string(dec.Reason))
	}
}
