// Package delegation implements time-bounded, revocable permission
// delegation with controlled redelegation and acyclic delegation chains.
package delegation

import (
	"context"
	"time"
)

// Principal is a security subject that may hold or delegate permissions.
type Principal string

// Permission identifies an permission over an action or resource.
type Permission string

// Reason explains why an evaluation decision was made.
type Reason string

// RejectReason identifies why a Grant operation was refused.
type RejectReason int

const (
	ReasonUnknown RejectReason = iota
	// ReasonNotDelegatable means the delegator holds the permission only
	// through an edge whose CanDelegate flag is false.
	ReasonNotDelegatable
	// ReasonCycle means the new edge would close a directed cycle among
	// active, redelegatable edges of the same permission.
	ReasonCycle
)

func (r RejectReason) String() string {
	switch r {
	case ReasonNotDelegatable:
		return "not_delegatable"
	case ReasonCycle:
		return "cycle_detected"
	default:
		return "unknown"
	}
}

// RejectError is returned when a Grant is refused. Rejected grants never
// mutate delegation state.
type RejectError struct {
	Reason     RejectReason
	Delegator  Principal
	Delegatee  Principal
	Permission Permission
	Detail     string
}

func (e *RejectError) Error() string {
	return "delegation rejected: " + e.Reason.String() + ": " + e.Detail
}

// Decision is the result of a permission evaluation.
type Decision struct {
	Allowed bool
	Reason  Reason
}

// GrantRequest describes a proposed delegation.
type GrantRequest struct {
	Delegator   Principal
	Delegatee   Principal
	Permission  Permission
	ExpiresAt   time.Time
	CanDelegate bool
}

// Delegation is a recorded delegation edge.
type Delegation struct {
	ID          int64
	Delegator   Principal
	Delegatee   Principal
	Permission  Permission
	ExpiresAt   time.Time
	CanDelegate bool
	Revoked     bool
	CreatedAt   time.Time
}

// Logger receives structured audit events.
type Logger interface {
	LogGrant(ctx context.Context, d *Delegation, decision string, reason string)
	LogRevoke(ctx context.Context, d *Delegation)
	LogEvaluate(ctx context.Context, subject Principal, perm Permission, allowed bool, reason string)
}

// Evaluation reasons returned in Decision.Reason.
const (
	// ReasonRootAuthority: the subject is an original authority.
	ReasonRootAuthority Reason = "root_authority"
	// ReasonDelegated: an unexpired, unrevoked chain reaches the subject.
	ReasonDelegated Reason = "active_delegation_chain"
	// ReasonNoDelegation: no valid path from any authority exists.
	ReasonNoDelegation Reason = "no_active_delegation"
	// ReasonChainExpired: every reaching chain contains an expired edge.
	ReasonChainExpired Reason = "delegation_chain_expired"
	// ReasonChainRevoked: every reaching chain passes through a revoked edge.
	ReasonChainRevoked Reason = "delegation_chain_revoked"
	// ReasonNotForwardable: reachable only via an edge that forbids onward delegation.
	ReasonNotForwardable Reason = "upstream_delegation_not_delegatable"
)
