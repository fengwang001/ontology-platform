// Package table is the public scoped symbol table.
package table

import (
	"errors"
	"sync"

	"ontology/capture"
	"ontology/name"
	"ontology/resolve"
	"ontology/scope"
)

var (
	// ErrNoScope rejects operations with no open scope.
	ErrNoScope = errors.New("table: no open scope")
	// ErrDepthLimit rejects Enter beyond the configured nesting limit.
	ErrDepthLimit = errors.New("table: nesting depth limit exceeded")
	// ErrDeclLimit rejects Declare beyond the per-scope limit.
	ErrDeclLimit = errors.New("table: declaration count limit exceeded")
	// ErrCycle: scope chain contains a cycle.
	ErrCycle = errors.New("table: scope chain has a cycle")
	// ErrDepthMismatch: chain depth disagrees with Enter/Leave pairing.
	ErrDepthMismatch = errors.New("table: scope depth mismatch")
	// ErrDupDecl: a scope's declaration table holds a repeated name.
	ErrDupDecl = errors.New("table: duplicate declaration in scope")
	// ErrRefUnstable: a recorded reference no longer resolves the same.
	ErrRefUnstable = errors.New("table: recorded reference changed resolution")
)

// Options configures resource limits; non-positive values get defaults.
type Options struct {
	MaxDepth int
	MaxDecls int
}

type refKey struct {
	s    *scope.Scope
	name string
	pos  int
}

// Table is the facade over scope/resolve/capture. A single mutex makes
// the read-only operations safe for concurrent use after construction.
type Table struct {
	mu       sync.Mutex
	cur      *scope.Scope
	maxDepth int
	maxDecls int
	res      *resolve.Resolver
	refs     []resolve.Record
	seen     map[refKey]bool
	opens    int
}

// New returns an empty table with the given limits.
func New(o Options) *Table {
	if o.MaxDepth <= 0 {
		o.MaxDepth = 1024
	}
	if o.MaxDecls <= 0 {
		o.MaxDecls = 4096
	}
	return &Table{maxDepth: o.MaxDepth, maxDecls: o.MaxDecls, res: resolve.New(), seen: make(map[refKey]bool)}
}

// Depth returns the current scope depth, or -1 when no scope is open.
func (t *Table) Depth() int {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.depthLocked()
}

func (t *Table) depthLocked() int {
	if t.cur == nil {
		return -1
	}
	return t.cur.Depth()
}

// Enter opens a child scope; over-limit is rejected without a half-entered scope.
func (t *Table) Enter() error {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.depthLocked()+1 >= t.maxDepth {
		return ErrDepthLimit
	}
	t.cur = scope.Enter(t.cur)
	t.opens++
	return nil
}

// Leave closes the current scope; leaving the outermost is an error.
func (t *Table) Leave() error {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.cur == nil {
		return scope.ErrLeaveRoot
	}
	parent, err := t.cur.Leave()
	if err != nil {
		return err
	}
	t.cur = parent
	t.opens--
	return nil
}

// Declare adds a declaration at source position pos.
func (t *Table) Declare(text string, kind name.Kind, pos int) error {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.cur == nil {
		return ErrNoScope
	}
	n, err := name.New(text)
	if err != nil {
		return err
	}
	if t.cur.Len() >= t.maxDecls {
		return ErrDeclLimit
	}
	return t.cur.Declare(name.NewDecl(n, kind, pos))
}

// Ref resolves a reference at source position pos and records hits.
func (t *Table) Ref(text string, pos int) (resolve.Result, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.cur == nil {
		return resolve.Result{}, ErrNoScope
	}
	n, err := name.New(text)
	if err != nil {
		return resolve.Result{}, err
	}
	r, err := t.res.Resolve(t.cur, n, pos)
	if err != nil {
		return resolve.Result{}, err
	}
	k := refKey{s: t.cur, name: text, pos: pos}
	if !t.seen[k] {
		t.seen[k] = true
		t.refs = append(t.refs, resolve.Record{Scope: t.cur, Name: n, Pos: pos, Result: r})
	}
	return r, nil
}

// Captures lists the distinct outer declarations referenced from the
// current scope.
func (t *Table) Captures() ([]capture.Item, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.cur == nil {
		return nil, ErrNoScope
	}
	return capture.Of(t.cur, t.refs), nil
}

// SelfCheck verifies chain acyclicity, depth pairing, per-scope name
// uniqueness, and that every recorded reference still resolves the same.
func (t *Table) SelfCheck() error {
	t.mu.Lock()
	defer t.mu.Unlock()
	chain := map[*scope.Scope]bool{}
	depth := 0
	for s := t.cur; s != nil; s = s.Parent() {
		if chain[s] {
			return ErrCycle
		}
		chain[s] = true
		depth++
	}
	if depth != t.opens || (t.cur != nil && depth != t.cur.Depth()+1) {
		return ErrDepthMismatch
	}
	for s := range chain {
		uniq := make(map[string]bool, s.Len())
		for _, d := range s.Decls() {
			key := d.Name().String()
			if uniq[key] {
				return ErrDupDecl
			}
			uniq[key] = true
		}
	}
	for _, rec := range t.refs {
		got, err := t.res.Resolve(rec.Scope, rec.Name, rec.Pos)
		if err != nil || got != rec.Result {
			return ErrRefUnstable
		}
	}
	return nil
}
