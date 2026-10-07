// Package orphan implements a generational orphan reclamation subsystem for
// the ontology platform.
package orphan

import (
	"errors"
	"fmt"
	"sort"
)

// Clock returns logical time in milliseconds.
type Clock interface {
	NowMs() int64
}

// Kind is the configured contribution a link type makes to retention.
type Kind int

const (
	// Independent: any single incoming edge of this type retains the object.
	Independent Kind = iota + 1
	// Joint: retains the object only when every member type of its joint
	// group simultaneously has at least one incoming edge.
	Joint
)

// TypeConfig configures one link type.
type TypeConfig struct {
	Kind     Kind
	Requires []string // joint partners (Joint only); ignored for Independent
}

// Config is the whole subsystem configuration.
type Config struct {
	Types       map[string]TypeConfig
	GraceGen1Ms int64
	GraceGen2Ms int64
}

// Sentinel errors. When more than one error condition applies at once, only
// the first class in this fixed order is ever reported:
//
//  1. ErrObjectNotFound          (target object instance does not exist)
//  2. ErrTypeNotConfigured       (link type has no contribution configured)
//  3. ErrUndefinedJointRef       (joint config references an unknown type)
//  4. ErrNonPositiveGrace        (a grace period is not a positive number)
//
// The order is part of the subsystem contract and does not depend on the
// internal representation or iteration order of maps.
var (
	ErrObjectNotFound    = errors.New("orphan: target object instance does not exist")
	ErrTypeNotConfigured = errors.New("orphan: link type has no retention contribution configured")
	ErrUndefinedJointRef = errors.New("orphan: joint retention config references an undefined link type")
	ErrNonPositiveGrace  = errors.New("orphan: grace period duration must be a positive number")
)

// jointGroup is the normalized, self-closed closure of one or more Joint
// types. An object is retained by joint retention iff, for ANY group, it has
// >=1 incoming edge of EVERY member type of that group.
// Normalization makes symmetric ("A requires B", "B requires A") and
// asymmetric ("A requires B,C") declarations converge on one group id, so the
// evaluation rule stays identical regardless of declaration direction.
type jointGroup struct {
	id      string
	members []string // sorted
	extras  []string // sorted; independent types any member additionally requires
}

// validatedConfig holds config plus derived structures used by the System.
type validatedConfig struct {
	cfg       Config
	groups    map[string]*jointGroup // type name -> owning group (joint types only)
	groupList []*jointGroup          // sorted by id, for deterministic checks/logs
	indep     []string               // independent types, sorted
	joint     []string               // joint types, sorted
	known     map[string]bool
}

// validateConfig performs the configuration-level checks in the fixed error
// order (classes 2, 3, 4; class 1 is instance-level and is checked at call
// sites). The returned error is guaranteed to be the highest-priority error
// class currently applicable.
func validateConfig(cfg Config) (*validatedConfig, error) {
	known := make(map[string]bool, len(cfg.Types))
	// Class 2, deterministically: any type whose Kind is unset/zero.
	var badType string
	for name, tc := range cfg.Types {
		if tc.Kind != Independent && tc.Kind != Joint {
			if badType == "" || name < badType {
				badType = name
			}
		}
	}
	if badType != "" {
		return nil, fmt.Errorf("%w: %q", ErrTypeNotConfigured, badType)
	}
	for name := range cfg.Types {
		known[name] = true
	}
	// Class 3, deterministically: first undefined reference by (type, ref).
	type ref struct{ from, to string }
	var refs []ref
	for name, tc := range cfg.Types {
		if tc.Kind == Joint {
			for _, r := range tc.Requires {
				refs = append(refs, ref{name, r})
			}
		}
	}
	sort.Slice(refs, func(i, j int) bool {
		if refs[i].from != refs[j].from {
			return refs[i].from < refs[j].from
		}
		return refs[i].to < refs[j].to
	})
	for _, r := range refs {
		if !known[r.to] {
			return nil, fmt.Errorf("%w: %q requires undefined %q", ErrUndefinedJointRef, r.from, r.to)
		}
	}
	// Class 4.
	if cfg.GraceGen1Ms <= 0 || cfg.GraceGen2Ms <= 0 {
		return nil, ErrNonPositiveGrace
	}

	vc := &validatedConfig{cfg: cfg, known: known}
	vc.buildGroups()
	return vc, nil
}

func allNames(m map[string]TypeConfig) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// buildGroups computes undirected connected components of the "requires"
// graph restricted to Joint types. A requires-edge to an Independent type is
// allowed by class-3 validation but plays no part in grouping: such an edge
// simply means "this joint type also needs an independent-typed edge", which
// is structurally redundant (the independent edge alone already retains), so
// it is recorded as an extra per-type requirement instead.
func (vc *validatedConfig) buildGroups() {
	names := allNames(vc.cfg.Types)
	parent := map[string]string{}
	for _, n := range names {
		parent[n] = n
	}
	var find func(string) string
	find = func(x string) string {
		if parent[x] != x {
			parent[x] = find(parent[x])
		}
		return parent[x]
	}
	union := func(a, b string) {
		ra, rb := find(a), find(b)
		if ra != rb {
			parent[rb] = ra
		}
	}
	for _, t := range names {
		tc := vc.cfg.Types[t]
		if tc.Kind != Joint {
			continue
		}
		for _, r := range tc.Requires {
			if vc.cfg.Types[r].Kind == Joint {
				union(t, r)
			}
		}
	}
	comp := map[string][]string{}
	var compIDs []string
	for _, t := range names {
		if vc.cfg.Types[t].Kind != Joint {
			continue
		}
		root := find(t)
		if _, ok := comp[root]; !ok {
			compIDs = append(compIDs, root)
		}
		comp[root] = append(comp[root], t)
	}
	sort.Strings(compIDs)
	vc.groups = map[string]*jointGroup{}
	for _, cid := range compIDs {
		members := comp[cid]
		sort.Strings(members)
		extrasSet := map[string]struct{}{}
		for _, m := range members {
			for _, x := range vc.extraIndepRequirement(m) {
				extrasSet[x] = struct{}{}
			}
		}
		var extras []string
		for x := range extrasSet {
			extras = append(extras, x)
		}
		sort.Strings(extras)
		g := &jointGroup{id: cid, members: members, extras: extras}
		vc.groupList = append(vc.groupList, g)
		for _, m := range members {
			vc.groups[m] = g
			vc.joint = append(vc.joint, m)
		}
	}
	for _, t := range names {
		if vc.cfg.Types[t].Kind == Independent {
			vc.indep = append(vc.indep, t)
		}
	}
}

// extraIndepRequirement returns independent types explicitly required by a
// joint declaration. Because an independent edge always retains by itself,
// honoring this can never change a retained verdict into a different one; it
// is kept only so the configured edge combination is logged faithfully.
func (vc *validatedConfig) extraIndepRequirement(t string) []string {
	tc := vc.cfg.Types[t]
	var out []string
	for _, r := range tc.Requires {
		if vc.cfg.Types[r].Kind == Independent {
			out = append(out, r)
		}
	}
	sort.Strings(out)
	return out
}
