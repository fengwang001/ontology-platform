package watchnorm

import (
	"errors"
	"sort"
	"strings"
	"sync"
)

var (
	ErrBadArg    = errors.New("watchnorm: bad argument")
	ErrClock     = errors.New("watchnorm: clock moved backwards")
	ErrExists    = errors.New("watchnorm: path already exists")
	ErrMismatch  = errors.New("watchnorm: moved entry type does not match")
	ErrNoParent  = errors.New("watchnorm: parent directory does not exist")
	ErrDupCookie = errors.New("watchnorm: duplicate move cookie")
	ErrUnknown   = errors.New("watchnorm: unknown path")
)

type EventKind int

const (
	EventCreated EventKind = iota + 1
	EventDeleted
	EventRenamed
	EventRescan
)

type Event struct {
	Kind EventKind
	Path string
	From string
	To   string
}

type Normalizer struct {
	mu      sync.Mutex
	limit   int
	window  int64
	lastNow int64
	entries map[string]bool
	watched map[string]struct{}
	pending map[int64]*pendingMove
}

type pendingMove struct {
	time    int64
	cookie  int64
	from    string
	isDir   bool
	entries map[string]bool
	watched map[string]struct{}
}

func CreatedEvent(path string) Event {
	return Event{Kind: EventCreated, Path: path}
}

func DeletedEvent(path string) Event {
	return Event{Kind: EventDeleted, Path: path}
}

func RenamedEvent(from, to string) Event {
	return Event{Kind: EventRenamed, From: from, To: to}
}

func RescanEvent(path string) Event {
	return Event{Kind: EventRescan, Path: path}
}

func New(limit int, window int) (*Normalizer, error) {
	if limit < 1 || window < 1 {
		return nil, ErrBadArg
	}
	return &Normalizer{
		limit:   limit,
		window:  int64(window),
		lastNow: -1,
		entries: map[string]bool{"": true},
		watched: map[string]struct{}{"": {}},
		pending: make(map[int64]*pendingMove),
	}, nil
}

func (n *Normalizer) Created(now int64, path string, isDir bool) ([]Event, error) {
	n.mu.Lock()
	defer n.mu.Unlock()

	if now < n.lastNow {
		return nil, ErrClock
	}
	if now < 0 || !validPath(path) {
		return nil, ErrBadArg
	}
	events := n.expireAndFill(now)
	n.lastNow = now

	parent := parentPath(path)
	if !n.entries[parent] {
		return events, ErrNoParent
	}
	if _, exists := n.entries[path]; exists {
		return events, ErrExists
	}

	n.entries[path] = isDir
	events = append(events, CreatedEvent(path))
	if isDir && len(n.watched) < n.limit {
		n.watched[path] = struct{}{}
	}
	return events, nil
}

func (n *Normalizer) Deleted(now int64, path string) ([]Event, error) {
	n.mu.Lock()
	defer n.mu.Unlock()

	if now < n.lastNow {
		return nil, ErrClock
	}
	if now < 0 || !validPath(path) {
		return nil, ErrBadArg
	}
	events := n.expireAndFill(now)
	n.lastNow = now

	isDir, exists := n.entries[path]
	if !exists {
		return events, ErrUnknown
	}

	n.removeSubtree(path, isDir)
	events = append(events, DeletedEvent(path))
	events = n.fill(events)
	return events, nil
}

func (n *Normalizer) MovedFrom(now int64, path string, cookie int64, isDir bool) ([]Event, error) {
	n.mu.Lock()
	defer n.mu.Unlock()

	if now < n.lastNow {
		return nil, ErrClock
	}
	if now < 0 || !validPath(path) || cookie < 1 {
		return nil, ErrBadArg
	}
	events := n.expireAndFill(now)
	n.lastNow = now

	actualIsDir, exists := n.entries[path]
	if !exists || actualIsDir != isDir {
		return events, ErrUnknown
	}
	if _, exists := n.pending[cookie]; exists {
		return events, ErrDupCookie
	}

	record := &pendingMove{
		time:    now,
		cookie:  cookie,
		from:    path,
		isDir:   isDir,
		entries: make(map[string]bool),
		watched: make(map[string]struct{}),
	}
	n.detach(path, record)
	n.pending[cookie] = record
	return events, nil
}

func (n *Normalizer) MovedTo(now int64, path string, cookie int64, isDir bool) ([]Event, error) {
	n.mu.Lock()
	defer n.mu.Unlock()

	if now < n.lastNow {
		return nil, ErrClock
	}
	if now < 0 || !validPath(path) || cookie < 1 {
		return nil, ErrBadArg
	}
	events := n.expireAndFill(now)
	n.lastNow = now

	record := n.pending[cookie]
	if record != nil {
		if record.isDir != isDir {
			return events, ErrMismatch
		}
		parent := parentPath(path)
		if !n.entries[parent] {
			return events, ErrNoParent
		}
		if _, exists := n.entries[path]; exists {
			return events, ErrExists
		}

		from := record.from
		n.attach(record, path)
		delete(n.pending, cookie)
		events = append(events, RenamedEvent(from, path))
		events = n.fill(events)
		return events, nil
	}

	parent := parentPath(path)
	if !n.entries[parent] {
		return events, ErrNoParent
	}
	if _, exists := n.entries[path]; exists {
		return events, ErrExists
	}

	n.entries[path] = isDir
	events = append(events, CreatedEvent(path))
	if isDir && len(n.watched) < n.limit {
		n.watched[path] = struct{}{}
	}
	if isDir {
		events = append(events, RescanEvent(path))
	}
	return events, nil
}

func (n *Normalizer) Overflow(now int64) ([]Event, error) {
	n.mu.Lock()
	defer n.mu.Unlock()

	if now < n.lastNow {
		return nil, ErrClock
	}
	if now < 0 {
		return nil, ErrBadArg
	}
	events := n.expireAndFill(now)
	n.lastNow = now

	events = append(events, RescanEvent(""))
	for cookie, record := range n.pending {
		delete(n.pending, cookie)
		for watchedPath := range record.watched {
			delete(n.watched, watchedPath)
		}
	}
	events = n.fill(events)
	return events, nil
}

func (n *Normalizer) Tick(now int64) ([]Event, error) {
	n.mu.Lock()
	defer n.mu.Unlock()

	if now < n.lastNow {
		return nil, ErrClock
	}
	if now < 0 {
		return nil, ErrBadArg
	}
	events := n.expireAndFill(now)
	n.lastNow = now
	return events, nil
}

func (n *Normalizer) expireAndFill(now int64) []Event {
	var events []Event
	for _, record := range n.expiredMoves(now) {
		events = append(events, DeletedEvent(record.from))
		for watchedPath := range record.watched {
			delete(n.watched, watchedPath)
		}
		delete(n.pending, record.cookie)
	}
	return n.fill(events)
}

func (n *Normalizer) expiredMoves(now int64) []*pendingMove {
	var result []*pendingMove
	for _, record := range n.pending {
		if now-record.time >= n.window {
			result = append(result, record)
		}
	}
	sort.Slice(result, func(i, j int) bool {
		if result[i].time != result[j].time {
			return result[i].time < result[j].time
		}
		return result[i].cookie < result[j].cookie
	})
	return result
}

func (n *Normalizer) fill(events []Event) []Event {
	for len(n.watched) < n.limit {
		candidate, ok := n.leastUnwatchedDir()
		if !ok {
			break
		}
		n.watched[candidate] = struct{}{}
		events = append(events, RescanEvent(candidate))
	}
	return events
}

func (n *Normalizer) leastUnwatchedDir() (string, bool) {
	best := ""
	found := false
	for path, isDir := range n.entries {
		if !isDir {
			continue
		}
		if _, isWatched := n.watched[path]; isWatched {
			continue
		}
		if !found || path < best {
			best = path
			found = true
		}
	}
	return best, found
}

func (n *Normalizer) removeSubtree(path string, isDir bool) {
	delete(n.entries, path)
	delete(n.watched, path)
	if !isDir {
		return
	}
	prefix := path + "/"
	for entryPath := range n.entries {
		if strings.HasPrefix(entryPath, prefix) {
			delete(n.entries, entryPath)
			delete(n.watched, entryPath)
		}
	}
}

func (n *Normalizer) detach(path string, record *pendingMove) {
	prefix := path + "/"
	for entryPath, isDir := range n.entries {
		if entryPath == path || strings.HasPrefix(entryPath, prefix) {
			record.entries[entryPath] = isDir
			delete(n.entries, entryPath)
			if _, watched := n.watched[entryPath]; watched {
				record.watched[entryPath] = struct{}{}
			}
		}
	}
}

func (n *Normalizer) attach(record *pendingMove, destination string) {
	for oldPath, isDir := range record.entries {
		newPath := replacePrefix(oldPath, record.from, destination)
		n.entries[newPath] = isDir
	}
	for oldPath := range record.watched {
		newPath := replacePrefix(oldPath, record.from, destination)
		n.watched[newPath] = struct{}{}
		delete(n.watched, oldPath)
	}
}

func replacePrefix(path, oldPrefix, newPrefix string) string {
	if path == oldPrefix {
		return newPrefix
	}
	return newPrefix + path[len(oldPrefix):]
}

func parentPath(path string) string {
	index := strings.LastIndexByte(path, '/')
	if index < 0 {
		return ""
	}
	return path[:index]
}

func validPath(path string) bool {
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
