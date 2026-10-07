package shallow

import (
	"fmt"
	"math"
	"sort"
)

// staging holds objects fetched during one deepen operation. Nothing is
// merged into the Repo until the whole operation succeeds, so any failure
// (remote down, missing object) rolls the operation back for free.
type staging struct {
	commits  map[string]Commit
	contents map[string]ContentObject
}

func newStaging() *staging {
	return &staging{
		commits:  make(map[string]Commit),
		contents: make(map[string]ContentObject),
	}
}

// commitLocked returns commit id from the local store or the staging area,
// fetching it from the remote on first use. Cost is proportional to the
// view being deepened, never to unreachable parts of the remote graph.
func (r *Repo) commitLocked(st *staging, id string) (Commit, error) {
	if c, ok := r.commits[id]; ok {
		return c, nil
	}
	if c, ok := st.commits[id]; ok {
		return c, nil
	}
	c, err := r.remote.FetchCommit(id)
	if err != nil {
		return Commit{}, classifyRemote(err)
	}
	st.commits[id] = c
	return c, nil
}

// fetchContentsLocked fetches every missing content object of every
// newly fetched commit. It runs after all commit metadata is fetched, so
// a missing remote object (ErrObjectNotFound) is always reported before a
// content fetch failure (ErrFetchFailed).
func (r *Repo) fetchContentsLocked(st *staging) error {
	ids := make([]string, 0, len(st.commits))
	for id := range st.commits {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	for _, id := range ids {
		for _, oid := range st.commits[id].Contents {
			if _, ok := r.contents[oid]; ok {
				continue
			}
			if _, ok := st.contents[oid]; ok {
				continue
			}
			o, err := r.remote.FetchContent(oid)
			if err != nil {
				return classifyRemote(err)
			}
			st.contents[oid] = o
		}
	}
	return nil
}

// computeBoundary derives the boundary of a local commit set: commits
// present in the view with at least one parent absent from the view.
func computeBoundary(commits map[string]Commit) map[string]struct{} {
	nb := make(map[string]struct{})
	for id, c := range commits {
		for _, p := range c.Parents {
			if _, ok := commits[p]; !ok {
				nb[id] = struct{}{}
				break
			}
		}
	}
	return nb
}

func equalSet(a, b map[string]struct{}) bool {
	if len(a) != len(b) {
		return false
	}
	for id := range a {
		if _, ok := b[id]; !ok {
			return false
		}
	}
	return true
}

// applyLocked merges the staging area and recomputes the boundary. If the
// operation would change nothing (no fetched objects, boundary unchanged)
// it is a side-effect-free success: the sequence number is not bumped.
func (r *Repo) applyLocked(st *staging) {
	for id, c := range st.commits {
		r.commits[id] = c
	}
	for id, o := range st.contents {
		r.contents[id] = o
	}
	nb := computeBoundary(r.commits)
	if len(st.commits) == 0 && len(st.contents) == 0 && equalSet(nb, r.boundary) {
		return
	}
	r.boundary = nb
	r.seq++
	r.recomputeReachableLocked()
}

// minHeldDepthLocked returns the depth (in commits, ref target = 1) already
// held along every path from every ref, i.e. the distance to the nearest
// boundary commit. MaxInt when no boundary is reachable from any ref.
func (r *Repo) minHeldDepthLocked() int {
	best := math.MaxInt
	visited := make(map[string]bool)
	type item struct {
		id string
		d  int
	}
	queue := make([]item, 0, len(r.refs))
	for _, target := range r.refs {
		queue = append(queue, item{target, 1})
	}
	for len(queue) > 0 {
		it := queue[0]
		queue = queue[1:]
		if visited[it.id] {
			continue
		}
		visited[it.id] = true
		c, ok := r.commits[it.id]
		if !ok {
			continue
		}
		if _, isBoundary := r.boundary[it.id]; isBoundary {
			if it.d < best {
				best = it.d
			}
			continue
		}
		for _, p := range c.Parents {
			queue = append(queue, item{p, it.d + 1})
		}
	}
	return best
}

// depthViewLocked stages every commit within depth commits of any ref
// (per-path, over the full remote graph).
func (r *Repo) depthViewLocked(depth int, st *staging) error {
	best := make(map[string]int)
	type item struct {
		id string
		d  int
	}
	queue := make([]item, 0, len(r.refs))
	for _, target := range r.refs {
		queue = append(queue, item{target, 1})
	}
	for len(queue) > 0 {
		it := queue[0]
		queue = queue[1:]
		if it.d > depth {
			continue
		}
		if b, ok := best[it.id]; ok && b <= it.d {
			continue
		}
		best[it.id] = it.d
		c, err := r.commitLocked(st, it.id)
		if err != nil {
			return err
		}
		for _, p := range c.Parents {
			queue = append(queue, item{p, it.d + 1})
		}
	}
	return nil
}

// timeViewLocked stages every commit reachable from any ref along paths of
// commits created at or after since. The first commit older than since on
// each path stops that path and is itself excluded.
func (r *Repo) timeViewLocked(since int64, st *staging) error {
	seen := make(map[string]bool)
	queue := make([]string, 0, len(r.refs))
	for _, target := range r.refs {
		queue = append(queue, target)
	}
	for len(queue) > 0 {
		id := queue[0]
		queue = queue[1:]
		if seen[id] {
			continue
		}
		seen[id] = true
		c, err := r.commitLocked(st, id)
		if err != nil {
			return err
		}
		if c.Time < since {
			delete(st.commits, id) // excluded from the view
			continue
		}
		queue = append(queue, c.Parents...)
	}
	return nil
}

// unshallowViewLocked stages the full ancestor closure of every local
// commit (refs included, since refs always point at local commits).
func (r *Repo) unshallowViewLocked(st *staging) error {
	seen := make(map[string]bool, len(r.commits))
	queue := make([]string, 0, len(r.commits))
	for id := range r.commits {
		queue = append(queue, id)
	}
	for i := 0; i < len(queue); i++ {
		id := queue[i]
		if seen[id] {
			continue
		}
		seen[id] = true
		c, err := r.commitLocked(st, id)
		if err != nil {
			return err
		}
		queue = append(queue, c.Parents...)
	}
	return nil
}

// DeepenDepth advances the boundary so that every ref holds at least depth
// commits along every parent path. Deepening to a depth already held by
// every ref is a side-effect-free success. Any failure rolls the whole
// operation back.
func (r *Repo) DeepenDepth(depth int) error {
	if depth <= 0 {
		return fmt.Errorf("%w: depth %d", ErrInvalidParam, depth)
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if err := r.checkRefsLocked(); err != nil {
		return err
	}
	if depth <= r.minHeldDepthLocked() {
		return nil // already held: no side effects, no seq bump
	}
	st := newStaging()
	if err := r.depthViewLocked(depth, st); err != nil {
		return err
	}
	if err := r.fetchContentsLocked(st); err != nil {
		return err
	}
	if err := r.checkStateLocked(); err != nil {
		return err
	}
	r.applyLocked(st)
	return nil
}

// DeepenTime advances the boundary to include every commit created at or
// after since, stopping per path at the first older commit (excluded).
func (r *Repo) DeepenTime(since int64) error {
	if since < 0 {
		return fmt.Errorf("%w: time %d", ErrInvalidParam, since)
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if err := r.checkRefsLocked(); err != nil {
		return err
	}
	st := newStaging()
	if err := r.timeViewLocked(since, st); err != nil {
		return err
	}
	if err := r.fetchContentsLocked(st); err != nil {
		return err
	}
	if err := r.checkStateLocked(); err != nil {
		return err
	}
	r.applyLocked(st)
	return nil
}

// Unshallow fetches all ancestors of all local commits; the boundary
// becomes empty. Afterwards any deepen is a side-effect-free success.
func (r *Repo) Unshallow() error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if err := r.checkRefsLocked(); err != nil {
		return err
	}
	st := newStaging()
	if err := r.unshallowViewLocked(st); err != nil {
		return err
	}
	if err := r.fetchContentsLocked(st); err != nil {
		return err
	}
	if err := r.checkStateLocked(); err != nil {
		return err
	}
	r.applyLocked(st)
	return nil
}
