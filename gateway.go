package ontology

import (
	"errors"
	"sync"

	"ontology/authz"
	"ontology/rewrite"
	"ontology/route"
)

var (
	ErrInvalidArgument = errors.New("invalid argument")
	ErrInvalidPath     = errors.New("invalid path")
	ErrUnauthorized    = errors.New("unauthorized")
	ErrHopLimit        = errors.New("hop limit exceeded")
	ErrLoop            = errors.New("rewrite loop")
	ErrNoRoute         = errors.New("no route")
)

const (
	StageArgument = "argument"
	StagePath     = "path"
	StageOriginal = "original"
	StageRewrite  = "rewrite"
	StageFinal    = "final"
)

type Error struct {
	Kind    string
	Stage   string
	Version uint64
	Audit   uint64
	Path    string
	Hops    int
	Chain   []string
	Cause   error
}

func (e *Error) Error() string {
	if e.Cause == nil {
		return e.Kind
	}
	return e.Kind + ": " + e.Cause.Error()
}

func (e *Error) Unwrap() error { return e.Cause }

type Router struct {
	mu        sync.RWMutex
	k         int
	routes    *route.Table
	rules     *rewrite.RuleSet
	version   uint64
	nextAudit uint64
}

func NewRouter(maxHops int) (*Router, error) {
	if maxHops < 0 || maxHops > 32 {
		return nil, ErrInvalidArgument
	}
	emptyRoutes, err := route.NewTable(nil)
	if err != nil {
		return nil, err
	}
	emptyRules, err := rewrite.NewRuleSet(nil)
	if err != nil {
		return nil, err
	}
	return &Router{k: maxHops, routes: emptyRoutes, rules: emptyRules}, nil
}

func (r *Router) SetRoutes(routes []route.Route) error {
	table, err := route.NewTable(routes)
	if err != nil {
		return err
	}
	r.mu.Lock()
	r.routes = table
	r.version++
	r.mu.Unlock()
	return nil
}

func (r *Router) SetRules(rules []rewrite.Rule) error {
	ruleSet, err := rewrite.NewRuleSet(rules)
	if err != nil {
		return err
	}
	r.mu.Lock()
	r.rules = ruleSet
	r.version++
	r.mu.Unlock()
	return nil
}

type Result struct {
	OriginalPath string
	FinalPath    string
	Backend      string
	Hops         int
	Chain        []string
	Version      uint64
	Audit        uint64
}

func (r *Router) Handle(path string, scopes []string) (Result, error) {
	if err := authz.ValidateScopes(scopes); err != nil {
		return Result{}, &Error{Kind: "invalid_argument", Stage: StageArgument, Cause: err}
	}

	original, err := route.Normalize(path)
	if err != nil {
		return Result{}, &Error{Kind: "invalid_path", Stage: StagePath, Path: path, Cause: err}
	}

	r.mu.Lock()
	defer r.mu.Unlock()
	routes := r.routes
	rules := r.rules
	version := r.version

	originalMatch, routed := routes.Lookup(original)
	if routed && !authz.Allowed(originalMatch.Route.Scope, scopes) {
		return Result{}, &Error{Kind: "unauthorized", Stage: StageOriginal, Version: version, Path: original, Chain: []string{original}}
	}

	outcome, status := rules.Rewrite(original, r.k)
	if status != rewrite.StatusComplete {
		failure := &Error{Version: version, Path: outcome.Path, Hops: outcome.Hops, Chain: append([]string(nil), outcome.Chain...)}
		switch status {
		case rewrite.StatusHopLimit:
			failure.Kind = "hop_limit"
			failure.Stage = StageRewrite
			failure.Cause = ErrHopLimit
		case rewrite.StatusLoop:
			failure.Kind = "loop"
			failure.Stage = StageRewrite
			failure.Cause = ErrLoop
		default:
			failure.Kind = "invalid_path"
			failure.Stage = StageRewrite
			failure.Cause = ErrInvalidPath
		}
		return Result{}, failure
	}

	finalMatch, ok := routes.Lookup(outcome.Path)
	if !ok {
		return Result{}, &Error{Kind: "no_route", Stage: StageFinal, Version: version, Path: outcome.Path, Hops: outcome.Hops, Chain: append([]string(nil), outcome.Chain...), Cause: ErrNoRoute}
	}
	if !authz.Allowed(finalMatch.Route.Scope, scopes) {
		return Result{}, &Error{Kind: "unauthorized", Stage: StageFinal, Version: version, Path: outcome.Path, Hops: outcome.Hops, Chain: append([]string(nil), outcome.Chain...), Cause: ErrUnauthorized}
	}

	r.nextAudit++
	audit := r.nextAudit

	return Result{
		OriginalPath: original,
		FinalPath:    outcome.Path,
		Backend:      finalMatch.Route.Backend,
		Hops:         outcome.Hops,
		Chain:        append([]string(nil), outcome.Chain...),
		Version:      version,
		Audit:        audit,
	}, nil
}
