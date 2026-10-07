package shallow

import (
	"fmt"
	"sort"
	"sync"
)

// Repo is a shallow clone: a subset of the remote commit graph plus an
// explicit shallow boundary. All methods are safe for concurrent use and
// are linearizable (serialized on a single mutex).
//
// Invariants maintained after every successful mutation:
//   - boundary commits exist locally;
//   - every non-boundary local commit has all parents local;
//   - refs point at local commits;
//   - reachC/reachO cache exactly the reachable set (see reachableFromRefs).
type Repo struct {
	mu       sync.RWMutex
	remote   Remote
	commits  map[string]Commit
	contents map[string]ContentObject
	boundary map[string]struct{}
	refs     map[string]string

	reachC map[string]struct{} // reachable commits, O(1) point queries
	reachO map[string]struct{} // reachable content objects

	seq uint64 // mutation sequence number; rejected/no-op ops never bump it
}

// NewRepo loads a local state and validates it. Invalid states are
// rejected with ErrInvalidState (or ErrRefNotFound for dangling refs).
func NewRepo(remote Remote, commits []Commit, contents []ContentObject, boundary []string, refs map[string]string) (*Repo, error) {
	r := newRepoUnchecked(remote, commits, contents, boundary, refs)
	if err := r.checkRefsLocked(); err != nil {
		return nil, err
	}
	if err := r.checkStateLocked(); err != nil {
		return nil, err
	}
	r.recomputeReachableLocked()
	return r, nil
}

// newRepoUnchecked builds a Repo without validation; tests use it to
// construct invalid states and verify error precedence.
func newRepoUnchecked(remote Remote, commits []Commit, contents []ContentObject, boundary []string, refs map[string]string) *Repo {
	r := &Repo{
		remote:   remote,
		commits:  make(map[string]Commit, len(commits)),
		contents: make(map[string]ContentObject, len(contents)),
		boundary: make(map[string]struct{}, len(boundary)),
		refs:     make(map[string]string, len(refs)),
		reachC:   make(map[string]struct{}),
		reachO:   make(map[string]struct{}),
	}
	for _, c := range commits {
		r.commits[c.ID] = c
	}
	for _, o := range contents {
		r.contents[o.ID] = o
	}
	for _, id := range boundary {
		r.boundary[id] = struct{}{}
	}
	for k, v := range refs {
		r.refs[k] = v
	}
	return r
}

// checkRefsLocked verifies every ref resolves to a local commit.
func (r *Repo) checkRefsLocked() error {
	names := make([]string, 0, len(r.refs))
	for name := range r.refs {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		if _, ok := r.commits[r.refs[name]]; !ok {
			return fmt.Errorf("%w: ref %q -> %s", ErrRefNotFound, name, r.refs[name])
		}
	}
	return nil
}

// checkStateLocked verifies boundary membership and parent completeness.
func (r *Repo) checkStateLocked() error {
	for id := range r.boundary {
		if _, ok := r.commits[id]; !ok {
			return fmt.Errorf("%w: boundary commit %s not held", ErrInvalidState, id)
		}
	}
	for id, c := range r.commits {
		if _, isBoundary := r.boundary[id]; isBoundary {
			continue
		}
		for _, p := range c.Parents {
			if _, ok := r.commits[p]; !ok {
				return fmt.Errorf("%w: commit %s missing parent %s", ErrInvalidState, id, p)
			}
		}
	}
	return nil
}

// reachableFromRefs computes the reachable set from a set of root commits:
// walk parents but stop (inclusively) at boundary commits.
func reachableFromRefs(commits map[string]Commit, boundary map[string]struct{}, roots []string) (map[string]struct{}, map[string]struct{}) {
	rc := make(map[string]struct{})
	ro := make(map[string]struct{})
	stack := make([]string, 0, len(roots))
	for _, id := range roots {
		if _, ok := commits[id]; ok {
			stack = append(stack, id)
		}
	}
	for len(stack) > 0 {
		id := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		if _, seen := rc[id]; seen {
			continue
		}
		rc[id] = struct{}{}
		c := commits[id]
		for _, oid := range c.Contents {
			ro[oid] = struct{}{}
		}
		if _, stop := boundary[id]; stop {
			continue
		}
		stack = append(stack, c.Parents...)
	}
	return rc, ro
}

// recomputeReachableLocked refreshes the O(1) reachability index. Called
// after every committed mutation; queries themselves never traverse.
func (r *Repo) recomputeReachableLocked() {
	roots := make([]string, 0, len(r.refs))
	for _, target := range r.refs {
		roots = append(roots, target)
	}
	r.reachC, r.reachO = reachableFromRefs(r.commits, r.boundary, roots)
}

// Seq returns the mutation sequence number.
func (r *Repo) Seq() uint64 {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.seq
}

// Boundary returns the sorted ids of the shallow boundary.
func (r *Repo) Boundary() []string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]string, 0, len(r.boundary))
	for id := range r.boundary {
		out = append(out, id)
	}
	sort.Strings(out)
	return out
}

// HasCommit reports whether a commit is held locally.
func (r *Repo) HasCommit(id string) bool {
	r.mu.RLock()
	defer r.mu.RUnlock()
	_, ok := r.commits[id]
	return ok
}

// HasContent reports whether a content object is held locally.
func (r *Repo) HasContent(id string) bool {
	r.mu.RLock()
	defer r.mu.RUnlock()
	_, ok := r.contents[id]
	return ok
}

// Refs returns a snapshot of all refs.
func (r *Repo) Refs() map[string]string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make(map[string]string, len(r.refs))
	for k, v := range r.refs {
		out[k] = v
	}
	return out
}

// IsReachable answers the point query "is this object reachable" in O(1):
// it is a hash lookup into the maintained index and never traverses the
// graph, so its cost does not grow with the number of local objects.
func (r *Repo) IsReachable(id string) bool {
	r.mu.RLock()
	defer r.mu.RUnlock()
	if _, ok := r.reachC[id]; ok {
		return true
	}
	_, ok := r.reachO[id]
	return ok
}

// IsReachableFrom reports reachability from one named ref only.
func (r *Repo) IsReachableFrom(ref, id string) (bool, error) {
	if ref == "" {
		return false, fmt.Errorf("%w: empty ref name", ErrInvalidParam)
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	target, ok := r.refs[ref]
	if !ok {
		return false, fmt.Errorf("%w: %q", ErrRefNotFound, ref)
	}
	rc, ro := reachableFromRefs(r.commits, r.boundary, []string{target})
	if _, ok := rc[id]; ok {
		return true, nil
	}
	_, ok = ro[id]
	return ok, nil
}

// SetRef creates or moves a ref. The target must be held locally.
func (r *Repo) SetRef(name, commitID string) error {
	if name == "" {
		return fmt.Errorf("%w: empty ref name", ErrInvalidParam)
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, ok := r.commits[commitID]; !ok {
		return fmt.Errorf("%w: ref target %s not held locally", ErrInvalidState, commitID)
	}
	r.refs[name] = commitID
	r.seq++
	r.recomputeReachableLocked()
	return nil
}

// DeleteRef removes a ref.
func (r *Repo) DeleteRef(name string) error {
	if name == "" {
		return fmt.Errorf("%w: empty ref name", ErrInvalidParam)
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, ok := r.refs[name]; !ok {
		return fmt.Errorf("%w: %q", ErrRefNotFound, name)
	}
	delete(r.refs, name)
	r.seq++
	r.recomputeReachableLocked()
	return nil
}

// GC deletes every unreachable local commit and content object, except
// that shallow-boundary commits (and the content objects they reference)
// are always protected. It returns the number of deleted objects and the
// total reclaimed bytes (content sizes; commits carry no size).
func (r *Repo) GC() (int, int64, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if err := r.checkRefsLocked(); err != nil {
		return 0, 0, err
	}
	if err := r.checkStateLocked(); err != nil {
		return 0, 0, err
	}

	keepC := make(map[string]struct{}, len(r.reachC)+len(r.boundary))
	for id := range r.reachC {
		keepC[id] = struct{}{}
	}
	for id := range r.boundary {
		keepC[id] = struct{}{}
	}
	keepO := make(map[string]struct{})
	for id := range keepC {
		for _, oid := range r.commits[id].Contents {
			keepO[oid] = struct{}{}
		}
	}

	count := 0
	var bytes int64
	for id := range r.commits {
		if _, keep := keepC[id]; !keep {
			delete(r.commits, id)
			count++
		}
	}
	for id, o := range r.contents {
		if _, keep := keepO[id]; !keep {
			delete(r.contents, id)
			count++
			bytes += o.Size
		}
	}
	r.seq++
	r.recomputeReachableLocked()
	return count, bytes, nil
}
