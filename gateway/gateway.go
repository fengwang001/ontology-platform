// Package gateway combines route, rewrite and authz into a gateway
// router that authorizes at the original path, rewrites hop by hop,
// and authorizes again at the terminal route.
package gateway

import (
	"errors"
	"fmt"
	"sync"
	"sync/atomic"

	"ontology/authz"
	"ontology/rewrite"
	"ontology/route"
)

// MaxHops is the largest allowed rewrite hop budget K.
const MaxHops = 32

// Kind classifies failures of Router operations.
type Kind int

const (
	KindBadArgument Kind = iota // 参数非法
	KindBadPath                 // 路径非法
	KindDenied                  // 无权限
	KindNoRoute                 // 无路由
	KindHopLimit                // 跳数超限
	KindLoop                    // 环路
)

func (k Kind) String() string {
	switch k {
	case KindBadArgument:
		return "bad-argument"
	case KindBadPath:
		return "bad-path"
	case KindDenied:
		return "denied"
	case KindNoRoute:
		return "no-route"
	case KindHopLimit:
		return "hop-limit"
	case KindLoop:
		return "loop"
	}
	return "unknown"
}

// Stage identifies the authorization phase of a denial.
type Stage int

const (
	StageNone   Stage = iota
	StageOrigin       // 原始路径鉴权
	StageFinal        // 终点路由鉴权
)

func (s Stage) String() string {
	switch s {
	case StageOrigin:
		return "origin"
	case StageFinal:
		return "final"
	}
	return "none"
}

// Error is the single error type returned by Router operations.
type Error struct {
	Kind  Kind
	Stage Stage // set only for KindDenied
	Msg   string
}

func (e *Error) Error() string { return e.Msg }

// Result of a successful Handle call.
type Result struct {
	P0      string   // normalized original path
	Final   string   // terminal path after rewriting
	Backend string   // backend of the terminal route
	Hops    int      // number of rewrites applied (<= K)
	Chain   []string // P0 through Final, no duplicates
	Version uint64   // config version of the snapshot used
	Audit   uint64   // audit sequence number (successes only, from 1)
}

// snapshot is an immutable (routes, rules, version) triple.
type snapshot struct {
	routes  *route.Table
	rules   *rewrite.RuleSet
	version uint64
}

// Router is safe for concurrent use; every operation behaves as if
// executed in some serial order.
type Router struct {
	k int

	mu      sync.Mutex // serializes SetRoutes/SetRules and version bumps
	version uint64
	snap    atomic.Pointer[snapshot]
	audit   atomic.Uint64
}

// NewRouter creates a Router with rewrite hop budget k (0..32).
func NewRouter(k int) (*Router, error) {
	if k < 0 || k > MaxHops {
		return nil, &Error{Kind: KindBadArgument, Msg: fmt.Sprintf("gateway: K=%d out of range [0,%d]", k, MaxHops)}
	}
	routes, _ := route.NewTable(nil)
	rules, _ := rewrite.NewRuleSet(nil)
	r := &Router{k: k}
	r.snap.Store(&snapshot{routes: routes, rules: rules, version: 0})
	return r, nil
}

// K returns the rewrite hop budget.
func (r *Router) K() int { return r.k }

// Version returns the current config version (shared by routes and
// rules, initially 0, +1 per successful Set).
func (r *Router) Version() uint64 {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.version
}

// SetRoutes atomically replaces the whole routing table. On validation
// failure (first reason only) nothing changes.
func (r *Router) SetRoutes(entries []route.Entry) error {
	t, err := route.NewTable(entries)
	if err != nil {
		return &Error{Kind: KindBadArgument, Msg: err.Error()}
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.version++
	old := r.snap.Load()
	r.snap.Store(&snapshot{routes: t, rules: old.rules, version: r.version})
	return nil
}

// SetRules atomically replaces the whole rewrite rule set. On validation
// failure (first reason only) nothing changes.
func (r *Router) SetRules(rules []rewrite.Rule) error {
	rs, err := rewrite.NewRuleSet(rules)
	if err != nil {
		return &Error{Kind: KindBadArgument, Msg: err.Error()}
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.version++
	old := r.snap.Load()
	r.snap.Store(&snapshot{routes: old.routes, rules: rs, version: r.version})
	return nil
}

// Handle authorizes the original path, rewrites hop by hop, then
// authorizes the terminal route. It uses the (routes, rules) snapshot
// paired at call start. Failures change neither config, version, nor
// the audit sequence.
func (r *Router) Handle(path string, scopes []string) (Result, error) {
	if !authz.ValidScopes(scopes) {
		return Result{}, &Error{Kind: KindBadArgument, Msg: "gateway: scopes must not contain empty entries"}
	}
	p0, err := route.Normalize(path)
	if err != nil {
		return Result{}, &Error{Kind: KindBadPath, Msg: fmt.Sprintf("gateway: invalid path %q", path)}
	}
	snap := r.snap.Load()
	if e, ok := snap.routes.LongestPrefix(p0); ok && !authz.Allowed(e.Scope, scopes) {
		return Result{}, &Error{Kind: KindDenied, Stage: StageOrigin,
			Msg: fmt.Sprintf("gateway: denied at origin: %q requires scope %q", p0, e.Scope)}
	}
	out, err := snap.rules.Rewrite(p0, r.k)
	if err != nil {
		var hl *rewrite.HopLimitError
		var lp *rewrite.LoopError
		switch {
		case errors.As(err, &hl):
			return Result{}, &Error{Kind: KindHopLimit, Msg: err.Error()}
		case errors.As(err, &lp):
			return Result{}, &Error{Kind: KindLoop, Msg: err.Error()}
		default:
			return Result{}, &Error{Kind: KindBadPath, Msg: err.Error()} // unreachable
		}
	}
	e, ok := snap.routes.LongestPrefix(out.Final)
	if !ok {
		return Result{}, &Error{Kind: KindNoRoute,
			Msg: fmt.Sprintf("gateway: no route for terminal path %q", out.Final)}
	}
	if !authz.Allowed(e.Scope, scopes) {
		return Result{}, &Error{Kind: KindDenied, Stage: StageFinal,
			Msg: fmt.Sprintf("gateway: denied at final: %q requires scope %q", out.Final, e.Scope)}
	}
	return Result{
		P0:      p0,
		Final:   out.Final,
		Backend: e.Backend,
		Hops:    out.Hops,
		Chain:   out.Chain,
		Version: snap.version,
		Audit:   r.audit.Add(1),
	}, nil
}
