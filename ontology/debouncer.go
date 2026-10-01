package ontology

import (
	"errors"
	"sort"
	"strings"
	"sync"
)

// EventKind is the kind of a raw file event.
type EventKind int

const (
	Create EventKind = iota + 1
	Modify
	Delete
	DeleteDir
)

// NetKind is the folded kind of a pending entry.
type NetKind int

const (
	NetCreate NetKind = iota + 1 // C
	NetModify                    // M
	NetDelete                    // D
)

func (k NetKind) String() string {
	switch k {
	case NetCreate:
		return "C"
	case NetModify:
		return "M"
	case NetDelete:
		return "D"
	default:
		return "?"
	}
}

// Event is a raw file event: a kind and a relative path.
type Event struct {
	Kind EventKind
	Path string
}

// Entry is a folded, emitted net event.
type Entry struct {
	Path  string
	Kind  NetKind
	First int64
	Last  int64
}

// Debouncer folds raw file events per path and emits net events in batches.
type Debouncer struct {
	mu      sync.Mutex
	q       int64
	w       int64
	cap     int64
	pending map[string]*entry
	maxNow  int64
	hasTime bool
}

type entry struct {
	kind  NetKind
	first int64
	last  int64
}

var (
	ErrInvalidQuietPeriod = errors.New("debouncer: quiet period Q must be >= 1")
	ErrInvalidWaitLimit   = errors.New("debouncer: max wait W must be >= Q")
	ErrInvalidCapacity    = errors.New("debouncer: capacity Cap must be >= 1")
	ErrUnknownKind        = errors.New("debouncer: unknown event kind")
	ErrInvalidPath        = errors.New("debouncer: invalid path")
	ErrClockRewind        = errors.New("debouncer: clock rewind")
	ErrCapacityExceeded   = errors.New("debouncer: pending capacity exceeded")
)

// NewDebouncer creates a Debouncer with quiet period Q, max wait W and capacity Cap (ms).
func NewDebouncer(quietMS, maxWaitMS, capEntries int64) (*Debouncer, error) {
	if quietMS < 1 {
		return nil, ErrInvalidQuietPeriod
	}
	if maxWaitMS < quietMS {
		return nil, ErrInvalidWaitLimit
	}
	if capEntries < 1 {
		return nil, ErrInvalidCapacity
	}
	return &Debouncer{
		q:       quietMS,
		w:       maxWaitMS,
		cap:     capEntries,
		pending: make(map[string]*entry),
	}, nil
}

// Add folds one raw event at time now into the pending set.
func (d *Debouncer) Add(now int64, ev Event) error {
	d.mu.Lock()
	defer d.mu.Unlock()

	if !validKind(ev.Kind) {
		return ErrUnknownKind
	}
	if !validPath(ev.Path) {
		return ErrInvalidPath
	}
	if d.hasTime && now < d.maxNow {
		return ErrClockRewind
	}
	_, exists := d.pending[ev.Path]
	if !exists && int64(len(d.pending)) >= d.cap {
		return ErrCapacityExceeded
	}

	// All validation passed: the operation is accepted and advances the clock.
	d.maxNow = now
	d.hasTime = true

	if ev.Kind == DeleteDir {
		prefix := ev.Path + "/"
		for p := range d.pending {
			if strings.HasPrefix(p, prefix) {
				delete(d.pending, p)
			}
		}
		d.foldDelete(ev.Path, now)
		return nil
	}

	switch ev.Kind {
	case Create:
		d.foldCreate(ev.Path, now)
	case Modify:
		d.foldModify(ev.Path, now)
	case Delete:
		d.foldDelete(ev.Path, now)
	}
	return nil
}

// Flush emits and removes every entry that is due at now, in byte order by path.
func (d *Debouncer) Flush(now int64) ([]Entry, error) {
	d.mu.Lock()
	defer d.mu.Unlock()

	if d.hasTime && now < d.maxNow {
		return nil, ErrClockRewind
	}
	d.maxNow = now
	d.hasTime = true

	due := make([]Entry, 0)
	for p, e := range d.pending {
		if now-e.last >= d.q || now-e.first >= d.w {
			due = append(due, Entry{Path: p, Kind: e.kind, First: e.first, Last: e.last})
		}
	}
	sort.Slice(due, func(i, j int) bool { return due[i].Path < due[j].Path })
	for _, e := range due {
		delete(d.pending, e.Path)
	}
	return due, nil
}

// NextDue returns the earliest time at which any pending entry becomes due.
func (d *Debouncer) NextDue() (int64, bool) {
	d.mu.Lock()
	defer d.mu.Unlock()

	var next int64
	found := false
	for _, e := range d.pending {
		t := e.last + d.q
		if fw := e.first + d.w; fw < t {
			t = fw
		}
		if !found || t < next {
			next = t
			found = true
		}
	}
	return next, found
}

func validKind(k EventKind) bool {
	return k == Create || k == Modify || k == Delete || k == DeleteDir
}

// validPath rejects empty paths, leading/trailing '/' and empty path segments.
func validPath(p string) bool {
	if p == "" || p[0] == '/' || p[len(p)-1] == '/' {
		return false
	}
	if strings.Contains(p, "//") {
		return false
	}
	return true
}

func (d *Debouncer) foldCreate(path string, now int64) {
	e, ok := d.pending[path]
	if !ok {
		d.pending[path] = &entry{kind: NetCreate, first: now, last: now}
		return
	}
	switch e.kind {
	case NetCreate, NetModify:
		// unchanged net kind
	case NetDelete:
		e.kind = NetModify
	}
	e.last = now
}

func (d *Debouncer) foldModify(path string, now int64) {
	e, ok := d.pending[path]
	if !ok {
		d.pending[path] = &entry{kind: NetModify, first: now, last: now}
		return
	}
	// C stays C; M stays M; D stays D.
	e.last = now
}

func (d *Debouncer) foldDelete(path string, now int64) {
	e, ok := d.pending[path]
	if !ok {
		d.pending[path] = &entry{kind: NetDelete, first: now, last: now}
		return
	}
	if e.kind == NetCreate {
		delete(d.pending, path)
		return
	}
	e.kind = NetDelete // M -> D; D stays D
	e.last = now
}
