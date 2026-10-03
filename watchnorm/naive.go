package watchnorm

import (
	"fmt"
	"sort"
	"strings"
	"sync"
)

// Naive is a deliberately simple reference implementation of the same
// rules, written independently from Normalizer: it keeps the known tree
// as nested nodes and recomputes watch counts by full walks. Tests
// replay identical operation sequences against both and require
// identical outputs and state.
type Naive struct {
	mu      sync.Mutex
	w       int
	p       int64
	lastNow int64
	root    *nnode
	pend    []*npend
}

type nnode struct {
	isDir   bool
	watched bool
	kids    map[string]*nnode
}

type npend struct {
	t      int64
	cookie int64
	path   string
	node   *nnode
}

// NewNaive creates a Naive with watch budget w (>=1) and pairing
// window p (>=1).
func NewNaive(w int, p int64) (*Naive, error) {
	if w < 1 || p < 1 {
		return nil, ErrBadArg
	}
	return &Naive{
		w:       w,
		p:       p,
		lastNow: -1,
		root:    &nnode{isDir: true, watched: true, kids: map[string]*nnode{}},
	}, nil
}

func (x *Naive) Created(now int64, path string, isDir bool) ([]Event, error) {
	x.mu.Lock()
	defer x.mu.Unlock()
	evs, err := x.prologue(now, path)
	if err != nil {
		return nil, err
	}
	parent, _ := x.lookup(parentOf(path))
	if parent == nil || !parent.isDir {
		return evs, ErrNoParent
	}
	if node, _ := x.lookup(path); node != nil {
		return evs, ErrExists
	}
	node := &nnode{isDir: isDir, kids: map[string]*nnode{}}
	if isDir && x.watchedTotal() < x.w {
		node.watched = true
	}
	parent.kids[lastSeg(path)] = node
	evs = append(evs, Event{Kind: EventCreated, Path: path})
	return evs, nil
}

func (x *Naive) Deleted(now int64, path string) ([]Event, error) {
	x.mu.Lock()
	defer x.mu.Unlock()
	evs, err := x.prologue(now, path)
	if err != nil {
		return nil, err
	}
	parent, _ := x.lookup(parentOf(path))
	if parent == nil || parent.kids[lastSeg(path)] == nil {
		return evs, ErrUnknown
	}
	delete(parent.kids, lastSeg(path))
	evs = append(evs, Event{Kind: EventDeleted, Path: path})
	evs = x.backfill(evs)
	return evs, nil
}

func (x *Naive) MovedFrom(now int64, path string, cookie int64, isDir bool) ([]Event, error) {
	x.mu.Lock()
	defer x.mu.Unlock()
	evs, err := x.prologueCookie(now, path, cookie)
	if err != nil {
		return nil, err
	}
	node, _ := x.lookup(path)
	if node == nil || node.isDir != isDir {
		return evs, ErrUnknown
	}
	for _, rec := range x.pend {
		if rec.cookie == cookie {
			return evs, ErrDupCookie
		}
	}
	parent, _ := x.lookup(parentOf(path))
	delete(parent.kids, lastSeg(path))
	x.pend = append(x.pend, &npend{t: now, cookie: cookie, path: path, node: node})
	return evs, nil
}

func (x *Naive) MovedTo(now int64, path string, cookie int64, isDir bool) ([]Event, error) {
	x.mu.Lock()
	defer x.mu.Unlock()
	evs, err := x.prologueCookie(now, path, cookie)
	if err != nil {
		return nil, err
	}
	for i, rec := range x.pend {
		if rec.cookie != cookie {
			continue
		}
		// Pairing: entry processing already expired stale records.
		if rec.node.isDir != isDir {
			return evs, ErrMismatch
		}
		parent, _ := x.lookup(parentOf(path))
		if parent == nil || !parent.isDir {
			return evs, ErrNoParent
		}
		if node, _ := x.lookup(path); node != nil {
			return evs, ErrExists
		}
		parent.kids[lastSeg(path)] = rec.node
		x.pend = append(x.pend[:i], x.pend[i+1:]...)
		evs = append(evs, Event{Kind: EventRenamed, From: rec.path, To: path})
		evs = x.backfill(evs)
		return evs, nil
	}
	// No pending record: treat as a fresh create.
	parent, _ := x.lookup(parentOf(path))
	if parent == nil || !parent.isDir {
		return evs, ErrNoParent
	}
	if node, _ := x.lookup(path); node != nil {
		return evs, ErrExists
	}
	node := &nnode{isDir: isDir, kids: map[string]*nnode{}}
	if isDir && x.watchedTotal() < x.w {
		node.watched = true
	}
	parent.kids[lastSeg(path)] = node
	evs = append(evs, Event{Kind: EventCreated, Path: path})
	if isDir {
		evs = append(evs, Event{Kind: EventRescan, Path: path})
	}
	return evs, nil
}

func (x *Naive) Overflow(now int64) ([]Event, error) {
	x.mu.Lock()
	defer x.mu.Unlock()
	if err := x.clock(now); err != nil {
		return nil, err
	}
	evs := x.enter(now)
	evs = append(evs, Event{Kind: EventRescan, Path: ""})
	x.pend = nil
	evs = x.backfill(evs)
	return evs, nil
}

func (x *Naive) Tick(now int64) ([]Event, error) {
	x.mu.Lock()
	defer x.mu.Unlock()
	if err := x.clock(now); err != nil {
		return nil, err
	}
	return x.enter(now), nil
}

func (x *Naive) WatchedCount() int {
	x.mu.Lock()
	defer x.mu.Unlock()
	return x.watchedTotal()
}

func (x *Naive) DebugState() string {
	x.mu.Lock()
	defer x.mu.Unlock()
	var b strings.Builder
	var paths []string
	var walk func(node *nnode, path string)
	walk = func(node *nnode, path string) {
		paths = append(paths, path)
		for name, kid := range node.kids {
			kidPath := name
			if path != "" {
				kidPath = path + "/" + name
			}
			walk(kid, kidPath)
		}
	}
	walk(x.root, "")
	sort.Strings(paths)
	for _, p := range paths {
		node, _ := x.lookup(p)
		kind := "file"
		watch := "-"
		if node.isDir {
			kind = "dir"
			if node.watched {
				watch = "watched"
			} else {
				watch = "unwatched"
			}
		}
		fmt.Fprintf(&b, "entry %q %s %s\n", p, kind, watch)
	}
	pend := append([]*npend(nil), x.pend...)
	sort.Slice(pend, func(i, j int) bool { return pend[i].cookie < pend[j].cookie })
	for _, rec := range pend {
		var rels []string
		var rwalk func(node *nnode, rel string)
		rwalk = func(node *nnode, rel string) {
			rels = append(rels, rel)
			for name, kid := range node.kids {
				kidRel := name
				if rel != "" {
					kidRel = rel + "/" + name
				}
				rwalk(kid, kidRel)
			}
		}
		rwalk(rec.node, "")
		sort.Strings(rels)
		fmt.Fprintf(&b, "pending cookie=%d t=%d root=%q watchedN=%d\n",
			rec.cookie, rec.t, rec.path, countWatched(rec.node))
		for _, rel := range rels {
			node := rec.node
			if rel != "" {
				for _, seg := range strings.Split(rel, "/") {
					node = node.kids[seg]
				}
			}
			kind := "file"
			watch := "-"
			if node.isDir {
				kind = "dir"
				if node.watched {
					watch = "watched"
				} else {
					watch = "unwatched"
				}
			}
			fmt.Fprintf(&b, "  pending-entry %q %s %s\n", rel, kind, watch)
		}
	}
	fmt.Fprintf(&b, "lastNow=%d watchedCount=%d\n", x.lastNow, x.watchedTotal())
	return b.String()
}

func (x *Naive) prologue(now int64, path string) ([]Event, error) {
	if err := x.clock(now); err != nil {
		return nil, err
	}
	if !validPath(path) {
		return nil, ErrBadArg
	}
	return x.enter(now), nil
}

func (x *Naive) prologueCookie(now int64, path string, cookie int64) ([]Event, error) {
	if err := x.clock(now); err != nil {
		return nil, err
	}
	if !validPath(path) || cookie < 1 {
		return nil, ErrBadArg
	}
	return x.enter(now), nil
}

func (x *Naive) clock(now int64) error {
	if now < x.lastNow {
		return ErrClock
	}
	x.lastNow = now
	if now < 0 {
		return ErrBadArg
	}
	return nil
}

// enter expires pending records with now-t >= P in (t, cookie) order,
// then backfills once.
func (x *Naive) enter(now int64) []Event {
	var evs []Event
	var expired []*npend
	for _, rec := range x.pend {
		if now-rec.t >= x.p {
			expired = append(expired, rec)
		}
	}
	sort.Slice(expired, func(i, j int) bool {
		if expired[i].t != expired[j].t {
			return expired[i].t < expired[j].t
		}
		return expired[i].cookie < expired[j].cookie
	})
	if len(expired) == 0 {
		return nil
	}
	gone := map[*npend]bool{}
	for _, rec := range expired {
		gone[rec] = true
		evs = append(evs, Event{Kind: EventDeleted, Path: rec.path})
	}
	kept := x.pend[:0]
	for _, rec := range x.pend {
		if !gone[rec] {
			kept = append(kept, rec)
		}
	}
	x.pend = kept
	return x.backfill(evs)
}

// backfill promotes the byte-order smallest unwatched in-tree directory
// while slots are free, emitting a Rescan per promotion.
func (x *Naive) backfill(evs []Event) []Event {
	for x.watchedTotal() < x.w {
		dirs := x.unwatchedDirs()
		if len(dirs) == 0 {
			break
		}
		sort.Strings(dirs)
		node, _ := x.lookup(dirs[0])
		node.watched = true
		evs = append(evs, Event{Kind: EventRescan, Path: dirs[0]})
	}
	return evs
}

// lookup walks the tree; it returns the node at path, or nil.
func (x *Naive) lookup(path string) (*nnode, string) {
	if path == "" {
		return x.root, ""
	}
	node := x.root
	for _, seg := range strings.Split(path, "/") {
		node = node.kids[seg]
		if node == nil {
			return nil, ""
		}
	}
	return node, path
}

// watchedTotal counts watched directories in the tree and in all
// pending subtrees by full traversal.
func (x *Naive) watchedTotal() int {
	total := countWatched(x.root)
	for _, rec := range x.pend {
		total += countWatched(rec.node)
	}
	return total
}

func countWatched(node *nnode) int {
	n := 0
	if node.isDir && node.watched {
		n++
	}
	for _, kid := range node.kids {
		n += countWatched(kid)
	}
	return n
}

// unwatchedDirs lists full paths of in-tree directories not watched.
func (x *Naive) unwatchedDirs() []string {
	var out []string
	var walk func(node *nnode, path string)
	walk = func(node *nnode, path string) {
		if node.isDir && !node.watched {
			out = append(out, path)
		}
		for name, kid := range node.kids {
			kidPath := name
			if path != "" {
				kidPath = path + "/" + name
			}
			walk(kid, kidPath)
		}
	}
	walk(x.root, "")
	return out
}

func lastSeg(path string) string {
	i := strings.LastIndexByte(path, '/')
	return path[i+1:]
}
