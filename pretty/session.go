package pretty

import (
	"fmt"
	"sync"
)

// Session holds the named fragments a document can reference. It is safe for
// concurrent use: registrations and renders may proceed in parallel, and a
// render observes a consistent snapshot of the registered fragments, as if
// all accepted operations ran in some serial order.
type Session struct {
	mu    sync.RWMutex
	frags map[string]Doc
}

// NewSession returns an empty session.
func NewSession() *Session {
	return &Session{frags: make(map[string]Doc)}
}

// Register adds a named fragment. The name must be non-empty and not already
// registered; the fragment's parameters must be valid and every fragment it
// references must already be registered (which makes reference cycles
// impossible). A rejected registration leaves the session unchanged.
//
// Error priority: ErrInvalidArgument, then ErrDuplicateName, then
// ErrUnregisteredRef.
func (s *Session) Register(name string, d Doc) error {
	if name == "" {
		return fmt.Errorf("%w: empty fragment name", ErrInvalidArgument)
	}
	if err := validateParams(d); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.frags[name]; ok {
		return fmt.Errorf("%w: %q", ErrDuplicateName, name)
	}
	for _, ref := range collectRefs(d) {
		if _, ok := s.frags[ref]; !ok {
			return fmt.Errorf("%w: %q referenced by fragment %q", ErrUnregisteredRef, ref, name)
		}
	}
	s.frags[name] = d
	return nil
}

// snapshot returns a consistent copy of the registered fragments.
func (s *Session) snapshot() map[string]Doc {
	s.mu.RLock()
	defer s.mu.RUnlock()
	snap := make(map[string]Doc, len(s.frags))
	for k, v := range s.frags {
		snap[k] = v
	}
	return snap
}

// validateParams checks node-local parameter validity without expanding
// references (referenced fragments were validated when registered).
func validateParams(root Doc) error {
	if root == nil {
		return fmt.Errorf("%w: nil document", ErrInvalidArgument)
	}
	a := &analysis{}
	stack := []Doc{root}
	for len(stack) > 0 {
		d := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		if d == nil {
			a.setInvalid("nil child node")
			continue
		}
		switch n := d.(type) {
		case *indentNode:
			if n.n < 0 {
				a.setInvalid("negative indent increment %d", n.n)
			}
			stack = append(stack, n.body)
		case *alignNode:
			stack = append(stack, n.body)
		case *groupNode:
			stack = append(stack, n.body)
		case *seqNode:
			stack = append(stack, n.parts...)
		case *textNode:
			for _, r := range n.s {
				if r == '\n' {
					a.setInvalid("text contains a newline character: %q", n.s)
					break
				}
			}
		case *condNode:
			for _, r := range n.broken + n.flat {
				if r == '\n' {
					a.setInvalid("conditional text contains a newline character")
					break
				}
			}
		case *refNode:
			if n.name == "" {
				a.setInvalid("empty fragment name in reference")
			}
		}
		if a.invalid != nil {
			return a.invalid
		}
	}
	return nil
}

// collectRefs returns the fragment names referenced directly by d, in
// first-occurrence order, without expanding references.
func collectRefs(root Doc) []string {
	var refs []string
	seen := make(map[string]bool)
	stack := []Doc{root}
	for len(stack) > 0 {
		d := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		switch n := d.(type) {
		case *indentNode:
			stack = append(stack, n.body)
		case *alignNode:
			stack = append(stack, n.body)
		case *groupNode:
			stack = append(stack, n.body)
		case *seqNode:
			stack = append(stack, n.parts...)
		case *refNode:
			if !seen[n.name] {
				seen[n.name] = true
				refs = append(refs, n.name)
			}
		}
	}
	return refs
}
