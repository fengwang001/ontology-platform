// Package fibmgr implements a capacity-limited forwarding table manager.
//
// The manager keeps a control plane (the full set of routes) and a data
// plane (an aggregated forwarding table). The data plane is kept
// semantically identical to the control plane for every one of the 2^32
// addresses while using the minimum possible number of entries. Updates
// that would push the minimum entry count above the configured capacity
// are rejected as a whole, leaving all state untouched.
package fibmgr

import (
	"errors"
	"fmt"
)

// Distinguishable error kinds, in fixed priority order:
// invalid argument, then withdraw target missing, then capacity exceeded.
var (
	ErrInvalidArgument  = errors.New("fibmgr: invalid argument")
	ErrNotFound         = errors.New("fibmgr: prefix not found")
	ErrCapacityExceeded = errors.New("fibmgr: capacity exceeded")
)

// NextHop is the target of a route: either a non-empty string nexthop or
// the special blackhole value. The zero value is invalid.
type NextHop struct {
	name      string
	blackhole bool
	valid     bool
}

// Nexthop returns a string nexthop. An empty name yields an invalid
// (zero) NextHop.
func Nexthop(name string) NextHop {
	return NextHop{name: name, valid: name != ""}
}

// Blackhole returns the special blackhole nexthop. Addresses matching a
// route with this nexthop are dropped; it differs from "no route".
func Blackhole() NextHop {
	return NextHop{blackhole: true, valid: true}
}

// Valid reports whether the nexthop is usable in a write operation.
func (n NextHop) Valid() bool { return n.valid }

// IsBlackhole reports whether the nexthop is the blackhole value.
func (n NextHop) IsBlackhole() bool { return n.valid && n.blackhole }

// Name returns the nexthop string ("" for the blackhole value).
func (n NextHop) Name() string { return n.name }

func (n NextHop) String() string {
	switch {
	case !n.valid:
		return "<invalid>"
	case n.blackhole:
		return "<blackhole>"
	default:
		return n.name
	}
}

// Prefix is an IPv4 prefix: an address plus a length in [0, 32]. Bits
// beyond the length must be zero, otherwise the prefix is invalid.
type Prefix struct {
	Addr uint32
	Len  int
}

// Valid reports whether the prefix length is in range and all bits
// beyond the prefix length are zero.
func (p Prefix) Valid() bool {
	switch {
	case p.Len < 0 || p.Len > 32:
		return false
	case p.Len == 0:
		return p.Addr == 0
	case p.Len == 32:
		return true
	default:
		return p.Addr&((uint32(1)<<uint(32-p.Len))-1) == 0
	}
}

func (p Prefix) String() string {
	return fmt.Sprintf("%d.%d.%d.%d/%d",
		p.Addr>>24, p.Addr>>16&0xff, p.Addr>>8&0xff, p.Addr&0xff, p.Len)
}

// OpKind selects the kind of a batch operation.
type OpKind int

const (
	// OpWrite installs or overwrites a route.
	OpWrite OpKind = iota
	// OpWithdraw removes an existing route.
	OpWithdraw
)

// Op is a single update inside a batch.
type Op struct {
	Kind    OpKind
	Prefix  Prefix
	Nexthop NextHop // only used by OpWrite
}

// Write builds a write operation.
func Write(p Prefix, nh NextHop) Op { return Op{Kind: OpWrite, Prefix: p, Nexthop: nh} }

// Withdraw builds a withdraw operation.
func Withdraw(p Prefix) Op { return Op{Kind: OpWithdraw, Prefix: p} }

func (op Op) valid() bool {
	if !op.Prefix.Valid() {
		return false
	}
	switch op.Kind {
	case OpWrite:
		return op.Nexthop.Valid()
	case OpWithdraw:
		return true
	default:
		return false
	}
}

// Outcome is the result class of looking up one address on one plane.
type Outcome int

const (
	// NoRoute means no entry covers the address.
	NoRoute Outcome = iota
	// Drop means the address matches a blackhole nexthop.
	Drop
	// Via means the address matches a string nexthop.
	Via
)

// PlaneResult is the outcome of querying one address on one plane.
type PlaneResult struct {
	Outcome Outcome
	Nexthop string // set only when Outcome == Via
}

func (r PlaneResult) String() string {
	switch r.Outcome {
	case NoRoute:
		return "no-route"
	case Drop:
		return "blackhole"
	default:
		return "via " + r.Nexthop
	}
}

// Entry is one data plane entry. Outcome may be NoRoute (an explicit
// no-route entry, which still occupies a slot), Drop or Via.
type Entry struct {
	Prefix  Prefix
	Outcome Outcome
	Nexthop string
}

func (e Entry) String() string {
	return fmt.Sprintf("%s -> %s", e.Prefix, PlaneResult{Outcome: e.Outcome, Nexthop: e.Nexthop})
}
