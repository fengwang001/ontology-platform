// Package watchnorm normalizes raw recursive directory watch events
// (create/delete/move-out/move-in/overflow) into a normalized stream of
// Created, Deleted, Renamed and Rescan events, while enforcing a budget
// on the number of watched directories and pairing renames by cookie
// within a time window.
package watchnorm

import (
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
)

var (
	ErrClock     = errors.New("watchnorm: clock went backwards")
	ErrBadArg    = errors.New("watchnorm: bad argument")
	ErrNoParent  = errors.New("watchnorm: parent directory is not a known directory")
	ErrExists    = errors.New("watchnorm: path already exists")
	ErrUnknown   = errors.New("watchnorm: path is unknown")
	ErrDupCookie = errors.New("watchnorm: duplicate pending move cookie")
	ErrMismatch  = errors.New("watchnorm: move kind mismatch")
)

// EventKind identifies a normalized event kind.
type EventKind int

const (
	EventCreated EventKind = iota
	EventDeleted
	EventRenamed
	EventRescan
)

func (k EventKind) String() string {
	switch k {
	case EventCreated:
		return "Created"
	case EventDeleted:
		return "Deleted"
	case EventRenamed:
		return "Renamed"
	case EventRescan:
		return "Rescan"
	}
	return "Unknown"
}

// Event is a normalized output event. Path is set for Created, Deleted
// and Rescan; From and To are set for Renamed.
type Event struct {
	Kind EventKind
	Path string
	From string
	To   string
}

func (e Event) String() string {
	if e.Kind == EventRenamed {
		return "Renamed(" + e.From + "->" + e.To + ")"
	}
	return e.Kind.String() + "(" + e.Path + ")"
}

// Normalizer is the incremental implementation. It is safe for
// concurrent use; calls behave as if executed in some serial order.
type Normalizer struct {
	mu             sync.Mutex
	w              int
	p              int64
	lastNow        int64
	entries        map[string]bool // path -> isDir; root "" is always present
	watched        map[string]bool // in-tree watched dirs (includes "")
	unwatched      map[string]bool // in-tree unwatched dirs
	pending        map[int64]*pendingRec
	watchedPending int // watched dirs inside pending records
}

// pendingRec is a detached subtree waiting for a rename pairing.
type pendingRec struct {
	t         int64
	cookie    int64
	root      string
	entries   map[string]bool // relative path ("" = root) -> isDir
	watched   map[string]bool // relative paths of watched dirs
	unwatched map[string]bool // relative paths of unwatched dirs
	watchedN  int
}

// New creates a Normalizer with watch budget w (>=1) and pairing
// window p (>=1). The root always exists, is always watched and
// occupies one slot.
func New(w int, p int64) (*Normalizer, error) {
	if w < 1 || p < 1 {
		return nil, ErrBadArg
	}
	n := &Normalizer{
		w:         w,
		p:         p,
		lastNow:   -1,
		entries:   map[string]bool{"": true},
		watched:   map[string]bool{"": true},
		unwatched: map[string]bool{},
		pending:   map[int64]*pendingRec{},
	}
	return n, nil
}

// Created handles a raw create event.
func (n *Normalizer) Created(now int64, path string, isDir bool) ([]Event, error) {
	n.mu.Lock()
	defer n.mu.Unlock()
	evs, err := n.prologue(now, path)
	if err != nil {
		return nil, err
	}
	parent := parentOf(path)
	if !n.isKnownDir(parent) {
		return evs, ErrNoParent
	}
	if _, ok := n.entries[path]; ok {
		return evs, ErrExists
	}
	n.entries[path] = isDir
	evs = append(evs, Event{Kind: EventCreated, Path: path})
	if isDir {
		n.admitDir(path)
	}
	return evs, nil
}

// Deleted handles a raw delete event.
func (n *Normalizer) Deleted(now int64, path string) ([]Event, error) {
	n.mu.Lock()
	defer n.mu.Unlock()
	evs, err := n.prologue(now, path)
	if err != nil {
		return nil, err
	}
	if _, ok := n.entries[path]; !ok {
		return evs, ErrUnknown
	}
	n.dropSubtree(path)
	evs = append(evs, Event{Kind: EventDeleted, Path: path})
	evs = n.backfill(evs)
	return evs, nil
}

// MovedFrom handles a raw move-out event.
func (n *Normalizer) MovedFrom(now int64, path string, cookie int64, isDir bool) ([]Event, error) {
	n.mu.Lock()
	defer n.mu.Unlock()
	evs, err := n.prologueCookie(now, path, cookie)
	if err != nil {
		return nil, err
	}
	actual, ok := n.entries[path]
	if !ok || actual != isDir {
		return evs, ErrUnknown
	}
	if _, dup := n.pending[cookie]; dup {
		return evs, ErrDupCookie
	}
	rec := n.detach(path)
	rec.t = now
	rec.cookie = cookie
	n.pending[cookie] = rec
	n.watchedPending += rec.watchedN
	return evs, nil
}

// MovedTo handles a raw move-in event.
func (n *Normalizer) MovedTo(now int64, path string, cookie int64, isDir bool) ([]Event, error) {
	n.mu.Lock()
	defer n.mu.Unlock()
	evs, err := n.prologueCookie(now, path, cookie)
	if err != nil {
		return nil, err
	}
	if rec, ok := n.pending[cookie]; ok {
		// Pairing: entry processing already expired records with
		// now-t >= P, so this record is within the window.
		if rec.entries[""] != isDir {
			return evs, ErrMismatch
		}
		if !n.isKnownDir(parentOf(path)) {
			return evs, ErrNoParent
		}
		if _, exists := n.entries[path]; exists {
			return evs, ErrExists
		}
		delete(n.pending, cookie)
		n.watchedPending -= rec.watchedN
		n.attach(rec, path)
		evs = append(evs, Event{Kind: EventRenamed, From: rec.root, To: path})
		evs = n.backfill(evs)
		return evs, nil
	}
	// No pending record: treat as a fresh create.
	if !n.isKnownDir(parentOf(path)) {
		return evs, ErrNoParent
	}
	if _, exists := n.entries[path]; exists {
		return evs, ErrExists
	}
	n.entries[path] = isDir
	evs = append(evs, Event{Kind: EventCreated, Path: path})
	if isDir {
		n.admitDir(path)
		evs = append(evs, Event{Kind: EventRescan, Path: path})
	}
	return evs, nil
}

// Overflow handles a raw overflow event.
func (n *Normalizer) Overflow(now int64) ([]Event, error) {
	n.mu.Lock()
	defer n.mu.Unlock()
	if err := n.clock(now); err != nil {
		return nil, err
	}
	evs := n.enter(now)
	evs = append(evs, Event{Kind: EventRescan, Path: ""})
	for cookie, rec := range n.pending {
		n.watchedPending -= rec.watchedN
		delete(n.pending, cookie)
	}
	evs = n.backfill(evs)
	return evs, nil
}

// Tick advances time, performing only entry processing.
func (n *Normalizer) Tick(now int64) ([]Event, error) {
	n.mu.Lock()
	defer n.mu.Unlock()
	if err := n.clock(now); err != nil {
		return nil, err
	}
	return n.enter(now), nil
}

// WatchedCount reports the current number of watched directories,
// including those inside pending subtrees. It is always <= W.
func (n *Normalizer) WatchedCount() int {
	n.mu.Lock()
	defer n.mu.Unlock()
	return len(n.watched) + n.watchedPending
}

// DebugState returns a canonical dump of the internal state, used by
// tests to compare implementations.
func (n *Normalizer) DebugState() string {
	n.mu.Lock()
	defer n.mu.Unlock()
	return n.debugStateLocked()
}

func (n *Normalizer) debugStateLocked() string {
	var b strings.Builder
	paths := make([]string, 0, len(n.entries))
	for p := range n.entries {
		paths = append(paths, p)
	}
	sort.Strings(paths)
	for _, p := range paths {
		kind := "file"
		watch := "-"
		if n.entries[p] {
			kind = "dir"
			if n.watched[p] {
				watch = "watched"
			} else {
				watch = "unwatched"
			}
		}
		fmt.Fprintf(&b, "entry %q %s %s\n", p, kind, watch)
	}
	cookies := make([]int64, 0, len(n.pending))
	for c := range n.pending {
		cookies = append(cookies, c)
	}
	sort.Slice(cookies, func(i, j int) bool { return cookies[i] < cookies[j] })
	for _, c := range cookies {
		rec := n.pending[c]
		rels := make([]string, 0, len(rec.entries))
		for rel := range rec.entries {
			rels = append(rels, rel)
		}
		sort.Strings(rels)
		fmt.Fprintf(&b, "pending cookie=%d t=%d root=%q watchedN=%d\n", c, rec.t, rec.root, rec.watchedN)
		for _, rel := range rels {
			kind := "file"
			watch := "-"
			if rec.entries[rel] {
				kind = "dir"
				if rec.watched[rel] {
					watch = "watched"
				} else {
					watch = "unwatched"
				}
			}
			fmt.Fprintf(&b, "  pending-entry %q %s %s\n", rel, kind, watch)
		}
	}
	fmt.Fprintf(&b, "lastNow=%d watchedCount=%d\n", n.lastNow, len(n.watched)+n.watchedPending)
	return b.String()
}

// prologue runs the common entry sequence: clock check, argument check,
// then expiry of pending move records followed by backfill. State changes
// from entry processing are kept even if the caller is rejected later.
func (n *Normalizer) prologue(now int64, path string) ([]Event, error) {
	if err := n.clock(now); err != nil {
		return nil, err
	}
	if !validPath(path) {
		return nil, ErrBadArg
	}
	return n.enter(now), nil
}

// prologueCookie is prologue plus the cookie >= 1 argument check.
func (n *Normalizer) prologueCookie(now int64, path string, cookie int64) ([]Event, error) {
	if err := n.clock(now); err != nil {
		return nil, err
	}
	if !validPath(path) || cookie < 1 {
		return nil, ErrBadArg
	}
	return n.enter(now), nil
}

// clock validates monotonic time. On success lastNow is updated even if
// the operation is rejected afterwards.
func (n *Normalizer) clock(now int64) error {
	if now < n.lastNow {
		return ErrClock
	}
	n.lastNow = now
	if now < 0 {
		return ErrBadArg
	}
	return nil
}

// enter expires pending records with now-t >= P in (t, cookie) order,
// emitting one Deleted per expired record, then backfills once.
func (n *Normalizer) enter(now int64) []Event {
	var evs []Event
	var expired []*pendingRec
	for _, rec := range n.pending {
		if now-rec.t >= n.p {
			expired = append(expired, rec)
		}
	}
	sortRecs(expired)
	for _, rec := range expired {
		delete(n.pending, rec.cookie)
		n.watchedPending -= rec.watchedN
		evs = append(evs, Event{Kind: EventDeleted, Path: rec.root})
	}
	if len(expired) > 0 {
		evs = n.backfill(evs)
	}
	return evs
}

// backfill promotes unwatched in-tree directories to watched in byte
// order while slots are free, emitting a Rescan per promotion.
func (n *Normalizer) backfill(evs []Event) []Event {
	for len(n.watched)+n.watchedPending < n.w && len(n.unwatched) > 0 {
		min := ""
		first := true
		for p := range n.unwatched {
			if first || p < min {
				min, first = p, false
			}
		}
		delete(n.unwatched, min)
		n.watched[min] = true
		evs = append(evs, Event{Kind: EventRescan, Path: min})
	}
	return evs
}

// admitDir registers a freshly created directory as watched when a slot
// is free, otherwise as unwatched.
func (n *Normalizer) admitDir(path string) {
	if len(n.watched)+n.watchedPending < n.w {
		n.watched[path] = true
	} else {
		n.unwatched[path] = true
	}
}

// dropSubtree removes path and everything below it from the in-memory
// tree, releasing watch slots held by watched directories inside.
func (n *Normalizer) dropSubtree(root string) {
	prefix := root + "/"
	for p, isDir := range n.entries {
		if p != root && !strings.HasPrefix(p, prefix) {
			continue
		}
		if isDir {
			delete(n.watched, p)
			delete(n.unwatched, p)
		}
		delete(n.entries, p)
	}
}

// detach removes the subtree rooted at root from the tree and returns it
// as a pending record. Watched directories inside keep occupying slots.
func (n *Normalizer) detach(root string) *pendingRec {
	rec := &pendingRec{
		root:      root,
		entries:   map[string]bool{},
		watched:   map[string]bool{},
		unwatched: map[string]bool{},
	}
	prefix := root + "/"
	for p, isDir := range n.entries {
		if p != root && !strings.HasPrefix(p, prefix) {
			continue
		}
		rel := strings.TrimPrefix(p[len(root):], "/")
		rec.entries[rel] = isDir
		if isDir {
			switch {
			case n.watched[p]:
				rec.watched[rel] = true
				rec.watchedN++
				delete(n.watched, p)
			case n.unwatched[p]:
				rec.unwatched[rel] = true
				delete(n.unwatched, p)
			}
		}
		delete(n.entries, p)
	}
	return rec
}

// attach re-inserts a pending subtree at root, preserving watch states.
func (n *Normalizer) attach(rec *pendingRec, root string) {
	for rel, isDir := range rec.entries {
		p := root
		if rel != "" {
			p = root + "/" + rel
		}
		n.entries[p] = isDir
		if isDir {
			switch {
			case rec.watched[rel]:
				n.watched[p] = true
			case rec.unwatched[rel]:
				n.unwatched[p] = true
			}
		}
	}
}

func (n *Normalizer) isKnownDir(path string) bool {
	isDir, ok := n.entries[path]
	return ok && isDir
}

func parentOf(path string) string {
	i := strings.LastIndexByte(path, '/')
	if i < 0 {
		return ""
	}
	return path[:i]
}

// validPath reports whether path is a valid non-root relative path.
func validPath(path string) bool {
	if path == "" || strings.HasPrefix(path, "/") || strings.HasSuffix(path, "/") {
		return false
	}
	for _, seg := range strings.Split(path, "/") {
		if seg == "" || seg == "." || seg == ".." {
			return false
		}
	}
	return true
}

func sortRecs(recs []*pendingRec) {
	for i := 1; i < len(recs); i++ {
		for j := i; j > 0; j-- {
			a, b := recs[j-1], recs[j]
			if a.t < b.t || (a.t == b.t && a.cookie < b.cookie) {
				break
			}
			recs[j-1], recs[j] = b, a
		}
	}
}
