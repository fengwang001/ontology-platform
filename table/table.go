// Package table is the public symbol table, chaining the other packages.
package table

import (
	"errors"
	"fmt"
	"sync"

	"ontology/capture"
	"ontology/name"
	"ontology/resolve"
	"ontology/scope"
)

var (
	ErrEmptyName = errors.New("table: empty name")
	ErrLeaveRoot = errors.New("table: leave at outermost scope")
	ErrMaxDepth  = errors.New("table: nesting depth limit exceeded")
	ErrMaxDecls  = errors.New("table: declarations per scope limit exceeded")
	ErrCorrupt   = errors.New("table: self-check failed")
)

type refRec struct {
	name string
	pos  int
	hit  resolve.Hit
}

// Table is a scoped symbol table in process memory. Methods are safe
// for concurrent use.
type Table struct {
	mu       sync.Mutex
	stack    []*scope.Scope
	refs     [][]refRec // refs[i] were recorded while stack[i] was innermost
	maxDepth int
	maxDecls int
	res      *resolve.Resolver
}

// New creates a table whose outermost scope already exists.
func New(maxDepth, maxDecls int) *Table {
	return &Table{
		stack:    []*scope.Scope{scope.New(nil, 0)},
		refs:     [][]refRec{nil},
		maxDepth: maxDepth,
		maxDecls: maxDecls,
		res:      &resolve.Resolver{},
	}
}

func (t *Table) depth() int { return len(t.stack) - 1 }

// Enter pushes a new inner scope; rejected beyond maxDepth, atomically.
func (t *Table) Enter() error {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.depth()+1 > t.maxDepth {
		return ErrMaxDepth
	}
	t.stack = append(t.stack, scope.New(t.stack[len(t.stack)-1], t.depth()+1))
	t.refs = append(t.refs, nil)
	return nil
}

// Leave pops the innermost scope. Outer scopes are never mutated by
// inner declarations, so popping restores them field-by-field.
func (t *Table) Leave() error {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.depth() == 0 {
		return ErrLeaveRoot
	}
	t.stack = t.stack[:len(t.stack)-1]
	t.refs = t.refs[:len(t.refs)-1]
	return nil
}

// Declare adds a declaration to the innermost scope; limits are checked
// before any state changes.
func (t *Table) Declare(n string, k name.Kind, pos int) error {
	t.mu.Lock()
	defer t.mu.Unlock()
	if n == "" {
		return ErrEmptyName
	}
	cur := t.stack[len(t.stack)-1]
	if cur.Len() >= t.maxDecls {
		return ErrMaxDecls
	}
	return cur.Declare(name.Decl{Name: n, Kind: k, Pos: pos})
}

// Ref resolves n at pos, records it, and returns the declaration and depth.
func (t *Table) Ref(n string, pos int) (name.Decl, int, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if n == "" {
		return name.Decl{}, 0, ErrEmptyName
	}
	hit, err := t.res.Resolve(t.stack[len(t.stack)-1], n, pos)
	if err != nil {
		return name.Decl{}, 0, err
	}
	t.refs[len(t.refs)-1] = append(t.refs[len(t.refs)-1], refRec{n, pos, hit})
	return hit.Decl, hit.Depth, nil
}

// Captures lists the outer declarations referenced from the innermost
// scope: exactly the hits that landed strictly outside it, deduplicated.
func (t *Table) Captures() []resolve.Hit {
	t.mu.Lock()
	defer t.mu.Unlock()
	var hits []resolve.Hit
	for _, r := range t.refs[len(t.refs)-1] {
		hits = append(hits, r.hit)
	}
	return capture.Outer(hits, t.depth())
}

// SelfCheck verifies: the scope chain is acyclic and matches the
// Enter/Leave balance, no scope exceeds its declaration limit, and
// every recorded reference still resolves to the same declaration.
func (t *Table) SelfCheck() error {
	t.mu.Lock()
	defer t.mu.Unlock()
	seen := make(map[*scope.Scope]bool)
	depth := t.depth()
	for s := t.stack[len(t.stack)-1]; s != nil; s = s.Parent() {
		if seen[s] {
			return fmt.Errorf("%w: cycle in scope chain", ErrCorrupt)
		}
		seen[s] = true
		if s.Depth() != depth || s.Len() > t.maxDecls {
			return fmt.Errorf("%w: bad depth or decl count", ErrCorrupt)
		}
		depth--
	}
	if depth != -1 || len(seen) != len(t.stack) {
		return fmt.Errorf("%w: chain length mismatch", ErrCorrupt)
	}
	for lvl, recs := range t.refs {
		for _, r := range recs {
			hit, err := t.res.Resolve(t.stack[lvl], r.name, r.pos)
			if err != nil || hit != r.hit {
				return fmt.Errorf("%w: stale reference %q", ErrCorrupt, r.name)
			}
		}
	}
	return nil
}
