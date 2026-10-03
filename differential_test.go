package watchnorm

import (
	"fmt"
	"math/rand"
	"reflect"
	"sort"
	"strings"
	"testing"
)

type refNormalizer struct {
	limit   int
	window  int64
	lastNow int64
	known   map[string]bool
	watched map[string]bool
	pending map[int64]*refPending
}

type refPending struct {
	time    int64
	cookie  int64
	from    string
	isDir   bool
	entries map[string]bool
	watched map[string]bool
}

type stateSnapshot struct {
	known   map[string]bool
	watched map[string]bool
	pending map[int64]pendingSnapshot
}

type pendingSnapshot struct {
	time    int64
	from    string
	isDir   bool
	entries map[string]bool
	watched map[string]bool
}

type randomOp struct {
	name   string
	now    int64
	path   string
	cookie int64
	isDir  bool
}

func newReference(limit, window int) *refNormalizer {
	return &refNormalizer{
		limit:   limit,
		window:  int64(window),
		lastNow: -1,
		known:   map[string]bool{"": true},
		watched: map[string]bool{"": true},
		pending: map[int64]*refPending{},
	}
}

func (r *refNormalizer) Created(now int64, path string, isDir bool) ([]Event, error) {
	if now < r.lastNow {
		return nil, ErrClock
	}
	if now < 0 || !refValidPath(path) {
		return nil, ErrBadArg
	}
	events := r.expireAndFill(now)
	r.lastNow = now
	if !r.known[refParent(path)] {
		return events, ErrNoParent
	}
	if _, exists := r.known[path]; exists {
		return events, ErrExists
	}
	r.known[path] = isDir
	events = append(events, CreatedEvent(path))
	if isDir && len(r.watched) < r.limit {
		r.watched[path] = true
	}
	return events, nil
}

func (r *refNormalizer) Deleted(now int64, path string) ([]Event, error) {
	if now < r.lastNow {
		return nil, ErrClock
	}
	if now < 0 || !refValidPath(path) {
		return nil, ErrBadArg
	}
	events := r.expireAndFill(now)
	r.lastNow = now
	isDir, exists := r.known[path]
	if !exists {
		return events, ErrUnknown
	}
	refDeleteSubtree(r, path, isDir)
	events = append(events, DeletedEvent(path))
	return r.fill(events), nil
}

func (r *refNormalizer) MovedFrom(now int64, path string, cookie int64, isDir bool) ([]Event, error) {
	if now < r.lastNow {
		return nil, ErrClock
	}
	if now < 0 || !refValidPath(path) || cookie < 1 {
		return nil, ErrBadArg
	}
	events := r.expireAndFill(now)
	r.lastNow = now
	actualIsDir, exists := r.known[path]
	if !exists || actualIsDir != isDir {
		return events, ErrUnknown
	}
	if _, exists := r.pending[cookie]; exists {
		return events, ErrDupCookie
	}
	record := &refPending{time: now, cookie: cookie, from: path, isDir: isDir, entries: map[string]bool{}, watched: map[string]bool{}}
	prefix := path + "/"
	for entryPath, entryIsDir := range r.known {
		if entryPath == path || strings.HasPrefix(entryPath, prefix) {
			record.entries[entryPath] = entryIsDir
			delete(r.known, entryPath)
			if r.watched[entryPath] {
				record.watched[entryPath] = true
			}
		}
	}
	r.pending[cookie] = record
	return events, nil
}

func refDeleteSubtree(r *refNormalizer, path string, isDir bool) {
	delete(r.known, path)
	delete(r.watched, path)
	if !isDir {
		return
	}
	prefix := path + "/"
	for entryPath := range r.known {
		if strings.HasPrefix(entryPath, prefix) {
			delete(r.known, entryPath)
			delete(r.watched, entryPath)
		}
	}
}

func (r *refNormalizer) MovedTo(now int64, path string, cookie int64, isDir bool) ([]Event, error) {
	if now < r.lastNow {
		return nil, ErrClock
	}
	if now < 0 || !refValidPath(path) || cookie < 1 {
		return nil, ErrBadArg
	}
	events := r.expireAndFill(now)
	r.lastNow = now
	record := r.pending[cookie]
	if record != nil {
		if record.isDir != isDir {
			return events, ErrMismatch
		}
		if !r.known[refParent(path)] {
			return events, ErrNoParent
		}
		if _, exists := r.known[path]; exists {
			return events, ErrExists
		}
		from := record.from
		for oldPath, entryIsDir := range record.entries {
			r.known[refReplace(oldPath, from, path)] = entryIsDir
		}
		for oldPath := range record.watched {
			delete(r.watched, oldPath)
			r.watched[refReplace(oldPath, from, path)] = true
		}
		delete(r.pending, cookie)
		events = append(events, RenamedEvent(from, path))
		return r.fill(events), nil
	}
	if !r.known[refParent(path)] {
		return events, ErrNoParent
	}
	if _, exists := r.known[path]; exists {
		return events, ErrExists
	}
	r.known[path] = isDir
	events = append(events, CreatedEvent(path))
	if isDir && len(r.watched) < r.limit {
		r.watched[path] = true
	}
	if isDir {
		events = append(events, RescanEvent(path))
	}
	return events, nil
}

func (r *refNormalizer) Overflow(now int64) ([]Event, error) {
	if now < r.lastNow {
		return nil, ErrClock
	}
	if now < 0 {
		return nil, ErrBadArg
	}
	events := append(r.expireAndFill(now), RescanEvent(""))
	r.lastNow = now
	for cookie, record := range r.pending {
		delete(r.pending, cookie)
		for path := range record.watched {
			delete(r.watched, path)
		}
	}
	return r.fill(events), nil
}

func (r *refNormalizer) Tick(now int64) ([]Event, error) {
	if now < r.lastNow {
		return nil, ErrClock
	}
	if now < 0 {
		return nil, ErrBadArg
	}
	events := r.expireAndFill(now)
	r.lastNow = now
	return events, nil
}

func refReplace(path, oldPrefix, newPrefix string) string {
	if path == oldPrefix {
		return newPrefix
	}
	return newPrefix + path[len(oldPrefix):]
}

func refValidPath(path string) bool {
	if path == "" || strings.HasPrefix(path, "/") || strings.HasSuffix(path, "/") {
		return false
	}
	for _, segment := range strings.Split(path, "/") {
		if segment == "" || segment == "." || segment == ".." {
			return false
		}
	}
	return true
}

func refParent(path string) string {
	if index := strings.LastIndexByte(path, '/'); index >= 0 {
		return path[:index]
	}
	return ""
}

func (r *refNormalizer) expireAndFill(now int64) []Event {
	var expired []*refPending
	for _, record := range r.pending {
		if now-record.time >= r.window {
			expired = append(expired, record)
		}
	}
	sort.Slice(expired, func(i, j int) bool {
		if expired[i].time != expired[j].time {
			return expired[i].time < expired[j].time
		}
		return expired[i].cookie < expired[j].cookie
	})
	var events []Event
	for _, record := range expired {
		events = append(events, DeletedEvent(record.from))
		for path := range record.watched {
			delete(r.watched, path)
		}
		delete(r.pending, record.cookie)
	}
	return r.fill(events)
}

func (r *refNormalizer) fill(events []Event) []Event {
	for len(r.watched) < r.limit {
		var candidates []string
		for path, isDir := range r.known {
			if isDir && !r.watched[path] {
				candidates = append(candidates, path)
			}
		}
		if len(candidates) == 0 {
			return events
		}
		sort.Strings(candidates)
		r.watched[candidates[0]] = true
		events = append(events, RescanEvent(candidates[0]))
	}
	return events
}

func TestRandomDifferentialAgainstNaiveModel(t *testing.T) {
	paths := []string{"a", "b", "c", "ab", "a/b", "a/bc", "a/b/c", "a/c", "x", "x/y", "z", "bad/", "/bad", "a//b", ".", ".."}
	names := []string{"Created", "Deleted", "MovedFrom", "MovedTo", "Overflow", "Tick"}
	for seed := int64(1); seed <= 2000; seed++ {
		rng := rand.New(rand.NewSource(seed))
		limit := 1 + rng.Intn(5)
		window := 1 + rng.Intn(12)
		actual, err := New(limit, window)
		if err != nil {
			t.Fatal(err)
		}
		naive := newReference(limit, window)
		current := int64(0)
		var inputs, outputs []string

		for step := 0; step < 24; step++ {
			op := randomOp{
				name:   names[rng.Intn(len(names))],
				path:   paths[rng.Intn(len(paths))],
				cookie: int64(1 + rng.Intn(6)),
				isDir:  rng.Intn(4) != 0,
			}
			switch {
			case current == 0 && rng.Intn(10) == 0:
				op.now = -1
			case rng.Intn(8) == 0:
				op.now = current - 1 - int64(rng.Intn(2))
			default:
				op.now = current + int64(rng.Intn(5))
			}
			if rng.Intn(12) == 0 {
				op.cookie = 0
			}

			actualEvents, actualErr := dispatchActual(actual, op)
			refEvents, refErr := dispatchReference(naive, op)
			actualState := snapshotActual(actual)
			refState := snapshotReference(naive)
			inputs = append(inputs, fmt.Sprintf("%02d:%s", step, formatOp(op)))
			outputs = append(outputs, fmt.Sprintf(
				"%02d: actual=%s err=%v; naive=%s err=%v; stateMatch=%t; watched=%d/%d",
				step, formatEvents(actualEvents), actualErr, formatEvents(refEvents), refErr,
				reflect.DeepEqual(actualState, refState), len(actual.watched), limit,
			))

			if !reflect.DeepEqual(actualEvents, refEvents) ||
				!sameError(actualErr, refErr) ||
				!reflect.DeepEqual(actualState, refState) ||
				len(actual.watched) > limit {
				t.Fatalf("seed=%d W=%d P=%d mismatch at step %d\ninputs:\n%s\noutputs:\n%s\nactual state=%#v\nnaive state=%#v",
					seed, limit, window, step, strings.Join(inputs, "\n"), strings.Join(outputs, "\n"), actualState, refState)
			}
			if op.now > current && (actualErr == nil || actualErr != ErrClock && actualErr != ErrBadArg) {
				current = op.now
			}
		}
		t.Logf("seed=%d W=%d P=%d; input/output basis:\n%s\n%s", seed, limit, window, strings.Join(inputs, "\n"), strings.Join(outputs, "\n"))
	}
}

func sameError(actual, want error) bool {
	if actual == nil || want == nil {
		return actual == want
	}
	return actual.Error() == want.Error()
}

func dispatchActual(n *Normalizer, op randomOp) ([]Event, error) {
	switch op.name {
	case "Created":
		return n.Created(op.now, op.path, op.isDir)
	case "Deleted":
		return n.Deleted(op.now, op.path)
	case "MovedFrom":
		return n.MovedFrom(op.now, op.path, op.cookie, op.isDir)
	case "MovedTo":
		return n.MovedTo(op.now, op.path, op.cookie, op.isDir)
	case "Overflow":
		return n.Overflow(op.now)
	default:
		return n.Tick(op.now)
	}
}

func dispatchReference(r *refNormalizer, op randomOp) ([]Event, error) {
	switch op.name {
	case "Created":
		return r.Created(op.now, op.path, op.isDir)
	case "Deleted":
		return r.Deleted(op.now, op.path)
	case "MovedFrom":
		return r.MovedFrom(op.now, op.path, op.cookie, op.isDir)
	case "MovedTo":
		return r.MovedTo(op.now, op.path, op.cookie, op.isDir)
	case "Overflow":
		return r.Overflow(op.now)
	default:
		return r.Tick(op.now)
	}
}

func snapshotActual(n *Normalizer) stateSnapshot {
	known := map[string]bool{}
	for path, isDir := range n.entries {
		known[path] = isDir
	}
	watched := map[string]bool{}
	for path := range n.watched {
		watched[path] = true
	}
	pending := map[int64]pendingSnapshot{}
	for cookie, record := range n.pending {
		watchedCopy := map[string]bool{}
		for path := range record.watched {
			watchedCopy[path] = true
		}
		pending[cookie] = makePendingSnapshot(record.time, record.from, record.isDir, record.entries, watchedCopy)
	}
	return stateSnapshot{known: known, watched: watched, pending: pending}
}

func snapshotReference(r *refNormalizer) stateSnapshot {
	known := map[string]bool{}
	for path, isDir := range r.known {
		known[path] = isDir
	}
	watched := map[string]bool{}
	for path, value := range r.watched {
		watched[path] = value
	}
	pending := map[int64]pendingSnapshot{}
	for cookie, record := range r.pending {
		pending[cookie] = makePendingSnapshot(record.time, record.from, record.isDir, record.entries, record.watched)
	}
	return stateSnapshot{known: known, watched: watched, pending: pending}
}

func makePendingSnapshot(now int64, from string, isDir bool, entries map[string]bool, watched map[string]bool) pendingSnapshot {
	entryCopy := map[string]bool{}
	for path, value := range entries {
		entryCopy[path] = value
	}
	watchedCopy := map[string]bool{}
	for path, value := range watched {
		watchedCopy[path] = value
	}
	return pendingSnapshot{time: now, from: from, isDir: isDir, entries: entryCopy, watched: watchedCopy}
}

func formatOp(op randomOp) string {
	return fmt.Sprintf("%s(now=%d,path=%q,cookie=%d,isDir=%t)", op.name, op.now, op.path, op.cookie, op.isDir)
}

func formatEvents(events []Event) string {
	parts := make([]string, 0, len(events))
	for _, event := range events {
		switch event.Kind {
		case EventRenamed:
			parts = append(parts, "Renamed("+event.From+"->"+event.To+")")
		case EventCreated:
			parts = append(parts, "Created("+event.Path+")")
		case EventDeleted:
			parts = append(parts, "Deleted("+event.Path+")")
		default:
			parts = append(parts, "Rescan("+event.Path+")")
		}
	}
	return "[" + strings.Join(parts, ",") + "]"
}
