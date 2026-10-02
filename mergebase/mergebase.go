// Package mergebase implements a commit graph with merge-base selection.
//
// Commits are registered with their parents, forming a DAG. Each commit
// carries a generation number (roots: 1, otherwise 1 + max parent gen),
// which is used to prune traversals so that merge-base queries do not
// scale with the total history length.
package mergebase

import (
	"container/heap"
	"errors"
	"fmt"
	"sort"
	"sync"
	"sync/atomic"
)

// MaxParents is the maximum number of parents a commit may have.
const MaxParents = 8

var (
	// ErrEmptyID is returned when a commit id is empty.
	ErrEmptyID = errors.New("mergebase: commit id is empty")
	// ErrDuplicateID is returned when a commit id is already registered.
	ErrDuplicateID = errors.New("mergebase: commit id already registered")
	// ErrTooManyParents is returned when a commit has more than MaxParents parents.
	ErrTooManyParents = errors.New("mergebase: too many parents")
	// ErrDuplicateParent is returned when a parent list contains duplicates.
	ErrDuplicateParent = errors.New("mergebase: duplicate parent id")
	// ErrUnknownParent is returned when a parent id is not registered.
	ErrUnknownParent = errors.New("mergebase: parent commit not registered")
	// ErrUnknownCommit is returned when a queried commit id is not registered.
	ErrUnknownCommit = errors.New("mergebase: commit not registered")
)

// commit is a registered node of the DAG.
type commit struct {
	id      string
	parents []string
	gen     int
}

// Store is a concurrency-safe commit graph. The zero value is not usable;
// create one with NewStore.
type Store struct {
	mu      sync.RWMutex
	commits map[string]*commit
	// visited counts commits popped from the traversal queue during the
	// most recent MergeBases call. Unexported on purpose: tests use it to
	// prove that traversal size does not depend on total history length.
	visited atomic.Int64
}

// NewStore returns an empty commit graph.
func NewStore() *Store {
	return &Store{commits: make(map[string]*commit)}
}

// Commit registers a new commit with the given id and parents.
//
// Validation reports only the first failure, in this order:
// empty id, id already registered, more than MaxParents parents,
// duplicate parents, unregistered parent (lowest index first).
// A rejected call does not modify the store.
func (s *Store) Commit(id string, parents []string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if id == "" {
		return ErrEmptyID
	}
	if _, ok := s.commits[id]; ok {
		return fmt.Errorf("%w: %q", ErrDuplicateID, id)
	}
	if len(parents) > MaxParents {
		return fmt.Errorf("%w: %d > %d", ErrTooManyParents, len(parents), MaxParents)
	}
	seen := make(map[string]struct{}, len(parents))
	for _, p := range parents {
		if _, ok := seen[p]; ok {
			return fmt.Errorf("%w: %q", ErrDuplicateParent, p)
		}
		seen[p] = struct{}{}
	}
	gen := 1
	for i, p := range parents {
		pc, ok := s.commits[p]
		if !ok {
			return fmt.Errorf("%w: parents[%d]=%q", ErrUnknownParent, i, p)
		}
		if pc.gen >= gen {
			gen = pc.gen + 1
		}
	}
	s.commits[id] = &commit{
		id:      id,
		parents: append([]string(nil), parents...),
		gen:     gen,
	}
	return nil
}

// Gen returns the generation number of a registered commit.
func (s *Store) Gen(id string) (int, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	c, ok := s.commits[id]
	if !ok {
		return 0, fmt.Errorf("%w: %q", ErrUnknownCommit, id)
	}
	return c.gen, nil
}

// IsAncestor reports whether a is an ancestor of b (a == b counts).
func (s *Store) IsAncestor(a, b string) (bool, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	ca, ok := s.commits[a]
	if !ok {
		return false, fmt.Errorf("%w: %q", ErrUnknownCommit, a)
	}
	cb, ok := s.commits[b]
	if !ok {
		return false, fmt.Errorf("%w: %q", ErrUnknownCommit, b)
	}
	return s.reaches(ca, cb), nil
}

// reaches reports whether target is an ancestor of from (or equal),
// pruning any descent into commits whose generation is <= target's.
func (s *Store) reaches(target, from *commit) bool {
	if target == from {
		return true
	}
	if target.gen > from.gen {
		return false
	}
	seen := map[*commit]bool{from: true}
	stack := []*commit{from}
	for len(stack) > 0 {
		c := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		for _, pid := range c.parents {
			p := s.commits[pid]
			if p == target {
				return true
			}
			// Generations strictly decrease along parent edges, so any
			// commit with gen <= target.gen cannot lead to target.
			if p.gen > target.gen && !seen[p] {
				seen[p] = true
				stack = append(stack, p)
			}
		}
	}
	return false
}

// MergeBases returns all best common ancestors of a and b: the maximal
// elements (under ancestry) of the set of common ancestors. The result is
// sorted by id in byte order. Two commits with no common ancestor yield an
// empty slice, not an error.
func (s *Store) MergeBases(a, b string) ([]string, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if _, ok := s.commits[a]; !ok {
		return nil, fmt.Errorf("%w: %q", ErrUnknownCommit, a)
	}
	if _, ok := s.commits[b]; !ok {
		return nil, fmt.Errorf("%w: %q", ErrUnknownCommit, b)
	}
	s.visited.Store(0)
	results := s.paintDownToCommon(a, b)
	results = s.filterRedundant(results)
	sort.Strings(results)
	return results, nil
}

// Flags used by the paint-down traversal.
const (
	flagP1     = 1 << iota // reachable from the first commit
	flagP2                 // reachable from the second commit
	flagStale              // known to be an ancestor of a common ancestor
	flagResult             // already recorded as a result
)

const flagMask = flagP1 | flagP2 | flagStale

// entry is a queued commit in the traversal frontier.
type entry struct {
	id  string
	gen int
}

// frontier is a max-heap ordered by generation (highest first), with id as
// a deterministic tie-breaker so equal-generation pops are reproducible.
type frontier []entry

func (f frontier) Len() int { return len(f) }
func (f frontier) Less(i, j int) bool {
	if f[i].gen != f[j].gen {
		return f[i].gen > f[j].gen
	}
	return f[i].id < f[j].id
}
func (f frontier) Swap(i, j int)       { f[i], f[j] = f[j], f[i] }
func (f *frontier) Push(x interface{}) { *f = append(*f, x.(entry)) }
func (f *frontier) Pop() interface{} {
	old := *f
	n := len(old)
	e := old[n-1]
	*f = old[:n-1]
	return e
}

// interesting reports whether the frontier still holds a commit that is not
// stale, i.e. one that may still turn into a common ancestor.
func (f *frontier) interesting(flags map[string]uint8) bool {
	for _, e := range *f {
		if flags[e.id]&flagStale == 0 {
			return true
		}
	}
	return false
}

// paintDownToCommon propagates P1/P2 flags from a and b towards the roots,
// always expanding the highest-generation frontier commit first. A commit
// reached from both sides is a common ancestor; it is recorded and its
// ancestry is marked stale so the traversal stops instead of walking the
// whole history below the merge bases.
func (s *Store) paintDownToCommon(a, b string) []string {
	flags := make(map[string]uint8)
	queue := &frontier{}
	push := func(id string, f uint8) {
		flags[id] |= f
		heap.Push(queue, entry{id: id, gen: s.commits[id].gen})
	}
	if a == b {
		push(a, flagP1|flagP2)
	} else {
		push(a, flagP1)
		push(b, flagP2)
	}

	var results []string
	for queue.Len() > 0 && queue.interesting(flags) {
		e := heap.Pop(queue).(entry)
		s.visited.Add(1)
		cur := flags[e.id] & flagMask
		if cur == flagP1|flagP2 {
			if flags[e.id]&flagResult == 0 {
				flags[e.id] |= flagResult
				results = append(results, e.id)
			}
			cur |= flagStale
		}
		for _, pid := range s.commits[e.id].parents {
			if flags[pid]&cur == cur {
				continue // parent already carries all these flags
			}
			push(pid, cur)
		}
	}
	return results
}

// filterRedundant keeps only the maximal elements of the candidate set: a
// candidate that is an ancestor of another candidate is dropped. The
// ancestor closure of the candidates equals the full common-ancestor set,
// so the maximal candidates are exactly the merge bases.
func (s *Store) filterRedundant(candidates []string) []string {
	kept := candidates[:0]
	for i, c := range candidates {
		redundant := false
		for j, d := range candidates {
			if i != j && s.reaches(s.commits[c], s.commits[d]) {
				redundant = true
				break
			}
		}
		if !redundant {
			kept = append(kept, c)
		}
	}
	return kept
}
