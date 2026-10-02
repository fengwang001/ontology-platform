// Package debounce implements a coalescing debouncer for file-watch events.
//
// Raw file events are folded per path into net events (C/M/D) and flushed
// in batches once the quiet period elapses or the max wait is exceeded.
// All methods are safe for concurrent use; the result is equivalent to
// some serial order.
package debounce

import (
	"errors"
	"sort"
	"strings"
	"sync"
)

// Kind is the kind of a raw file event.
type Kind int

const (
	Create Kind = iota
	Modify
	Delete
	DeleteDir
)

func (k Kind) valid() bool {
	return k >= Create && k <= DeleteDir
}

func (k Kind) String() string {
	switch k {
	case Create:
		return "Create"
	case Modify:
		return "Modify"
	case Delete:
		return "Delete"
	case DeleteDir:
		return "DeleteDir"
	}
	return "Unknown"
}

// Event is one raw file event.
type Event struct {
	Kind Kind
	Path string
}

// NetKind is the folded net event type.
type NetKind byte

const (
	NetCreate NetKind = 'C'
	NetModify NetKind = 'M'
	NetDelete NetKind = 'D'
)

func (n NetKind) String() string { return string(n) }

// Entry is one net event emitted by Flush.
type Entry struct {
	Path  string
	Net   NetKind
	First int64
	Last  int64
}

// Distinguishable error reasons.
var (
	ErrQuietTooSmall   = errors.New("debounce: quiet period Q must be >= 1")
	ErrMaxWaitTooSmall = errors.New("debounce: max wait W must be >= Q")
	ErrCapTooSmall     = errors.New("debounce: cap must be >= 1")
	ErrUnknownKind     = errors.New("debounce: unknown event kind")
	ErrInvalidPath     = errors.New("debounce: invalid path")
	ErrClockBackwards  = errors.New("debounce: clock moved backwards")
	ErrCapFull         = errors.New("debounce: pending capacity reached and path has no entry")
)

// Debouncer coalesces raw file events into net events.
type Debouncer struct {
	mu      sync.Mutex
	quiet   int64
	maxWait int64
	cap     int
	maxNow  int64
	hasNow  bool
	pending map[string]*Entry
}

// New builds a Debouncer. quiet is the quiet period in ms, maxWait the
// maximum wait in ms, cap the pending-entry limit. Validation order:
// quiet >= 1, maxWait >= quiet, cap >= 1; only the first error is reported.
func New(quiet, maxWait int64, cap int) (*Debouncer, error) {
	if quiet < 1 {
		return nil, ErrQuietTooSmall
	}
	if maxWait < quiet {
		return nil, ErrMaxWaitTooSmall
	}
	if cap < 1 {
		return nil, ErrCapTooSmall
	}
	return &Debouncer{
		quiet:   quiet,
		maxWait: maxWait,
		cap:     cap,
		pending: make(map[string]*Entry),
	}, nil
}

// Add accepts one raw event at time now (integer ms).
// Validation order: unknown kind, invalid path, clock backwards,
// capacity full with no existing entry for the path. Any failure
// rejects the whole operation without changing any state.
func (d *Debouncer) Add(now int64, ev Event) error {
	d.mu.Lock()
	defer d.mu.Unlock()

	if !ev.Kind.valid() {
		return ErrUnknownKind
	}
	if !validPath(ev.Path) {
		return ErrInvalidPath
	}
	if d.hasNow && now < d.maxNow {
		return ErrClockBackwards
	}
	if _, ok := d.pending[ev.Path]; !ok && len(d.pending) >= d.cap {
		return ErrCapFull
	}

	d.accept(now)
	d.fold(now, ev)
	return nil
}

// Flush emits all due entries (now-last >= Q or now-first >= W) in
// ascending path byte order and removes them from the pending set.
// The only possible failure is a backwards clock, which changes nothing.
func (d *Debouncer) Flush(now int64) ([]Entry, error) {
	d.mu.Lock()
	defer d.mu.Unlock()

	if d.hasNow && now < d.maxNow {
		return nil, ErrClockBackwards
	}
	d.accept(now)

	var out []Entry
	for p, e := range d.pending {
		if now-e.Last >= d.quiet || now-e.First >= d.maxWait {
			out = append(out, *e)
			delete(d.pending, p)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Path < out[j].Path })
	return out, nil
}

// NextDue returns the earliest due time, i.e. the minimum of
// min(last+Q, first+W) over all pending entries. The second return
// value is false when there are no pending entries.
func (d *Debouncer) NextDue() (int64, bool) {
	d.mu.Lock()
	defer d.mu.Unlock()

	var best int64
	found := false
	for _, e := range d.pending {
		due := e.Last + d.quiet
		if t := e.First + d.maxWait; t < due {
			due = t
		}
		if !found || due < best {
			best = due
			found = true
		}
	}
	return best, found
}

// Len returns the current number of pending entries.
func (d *Debouncer) Len() int {
	d.mu.Lock()
	defer d.mu.Unlock()
	return len(d.pending)
}

func (d *Debouncer) accept(now int64) {
	if !d.hasNow || now > d.maxNow {
		d.maxNow = now
		d.hasNow = true
	}
}

// fold merges an event into the pending set per the fold table.
// All validation must have passed before calling fold.
func (d *Debouncer) fold(now int64, ev Event) {
	if ev.Kind == DeleteDir {
		prefix := ev.Path + "/"
		for p := range d.pending {
			if strings.HasPrefix(p, prefix) {
				delete(d.pending, p)
			}
		}
	}

	e, ok := d.pending[ev.Path]
	if !ok {
		net := NetDelete
		switch ev.Kind {
		case Create:
			net = NetCreate
		case Modify:
			net = NetModify
		}
		d.pending[ev.Path] = &Entry{Path: ev.Path, Net: net, First: now, Last: now}
		return
	}

	switch e.Net {
	case NetCreate:
		switch ev.Kind {
		case Delete, DeleteDir:
			delete(d.pending, ev.Path)
			return
		}
	case NetModify:
		switch ev.Kind {
		case Delete, DeleteDir:
			e.Net = NetDelete
		}
	case NetDelete:
		if ev.Kind == Create {
			e.Net = NetModify
		}
	}
	e.Last = now
}

func validPath(p string) bool {
	if p == "" || p[0] == '/' || p[len(p)-1] == '/' {
		return false
	}
	return !strings.Contains(p, "//")
}
