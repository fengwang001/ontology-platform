package pitr

import (
	"context"
	"fmt"
	"io"
	"os"
	"sort"
	"sync"
)

// stdLogger writes each decision as "kind fields..." to a writer.
type stdLogger struct {
	w io.Writer
}

func (l stdLogger) Log(kind string, fields map[string]any) {
	keys := make([]string, 0, len(fields))
	for k := range fields {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	fmt.Fprintf(l.w, "[pitr] %s:", kind)
	for _, k := range keys {
		fmt.Fprintf(l.w, " %s=%v", k, fields[k])
	}
	fmt.Fprintln(l.w)
}

// NewRegistry creates a registry seeded with root timeline 1 forked at 0.
func NewRegistry() *Registry {
	return &Registry{
		log:       stdLogger{w: os.Stderr},
		mu:        &sync.RWMutex{},
		timelines: map[TimelineID]Timeline{1: {ID: 1, Parent: 0, Fork: 0}},
		segments:  map[TimelineID][]Segment{},
		backups:   nil,
		maxTLI:    1,
	}
}

// WithLogger returns a shallow registry copy that logs decisions to l.
// The copy shares the catalog, so it is safe to use concurrently with r.
func (r *Registry) WithLogger(l Logger) *Registry {
	cp := *r
	cp.log = l
	return &cp
}

func (r *Registry) emit(kind string, fields map[string]any) {
	if r.log != nil {
		r.log.Log(kind, fields)
	}
}

// forkOf returns the fork position of tli.
func (r *Registry) forkOf(tli TimelineID) Position {
	t, ok := r.timelines[tli]
	if !ok {
		return 0
	}
	return t.Fork
}

// RegisterTimeline registers a pre-existing timeline.
func (r *Registry) RegisterTimeline(ctx context.Context, t Timeline) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	r.emit("register_timeline_input", map[string]any{"id": t.ID, "parent": t.Parent, "fork": t.Fork})

	if t.ID <= 1 {
		r.emit("register_timeline_reject", map[string]any{"reason": "id", "id": t.ID})
		return fmt.Errorf("%w: id %d must exceed 1", ErrInvalidFork, t.ID)
	}
	if _, exists := r.timelines[t.ID]; exists {
		r.emit("register_timeline_reject", map[string]any{"reason": "duplicate", "id": t.ID})
		return fmt.Errorf("%w: timeline %d already registered", ErrInvalidFork, t.ID)
	}
	parent, ok := r.timelines[t.Parent]
	if !ok {
		r.emit("register_timeline_reject", map[string]any{"reason": "parent_missing", "parent": t.Parent})
		return fmt.Errorf("%w: parent timeline %d does not exist", ErrInvalidFork, t.Parent)
	}
	if t.Fork < parent.Fork {
		r.emit("register_timeline_reject", map[string]any{
			"reason":      "fork_before_parent_fork",
			"fork":        t.Fork,
			"parent_fork": parent.Fork,
		})
		return fmt.Errorf("%w: timeline %d forks at %d before parent %d fork at %d",
			ErrInvalidFork, t.ID, t.Fork, t.Parent, parent.Fork)
	}

	r.timelines[t.ID] = t
	if t.ID > r.maxTLI {
		r.maxTLI = t.ID
	}
	r.emit("register_timeline_ok", map[string]any{"id": t.ID, "maxTLI": r.maxTLI})
	return nil
}

// ArchiveSegments stores segments. Segments of one timeline must be
// well-formed half-open intervals and must not overlap each other.
func (r *Registry) ArchiveSegments(ctx context.Context, segs ...Segment) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	for _, s := range segs {
		r.emit("archive_input", map[string]any{"tli": s.TLI, "start": s.Start, "end": s.End})
		if _, ok := r.timelines[s.TLI]; !ok {
			r.emit("archive_reject", map[string]any{"reason": "timeline_missing", "tli": s.TLI})
			return fmt.Errorf("%w: timeline %d does not exist", ErrInvalidSegment, s.TLI)
		}
		if s.Start < 0 || s.End <= s.Start {
			r.emit("archive_reject", map[string]any{"reason": "interval", "start": s.Start, "end": s.End})
			return fmt.Errorf("%w: segment [%d,%d) on tli %d", ErrInvalidSegment, s.Start, s.End, s.TLI)
		}
	}

	for _, s := range segs {
		all := append(append([]Segment(nil), r.segments[s.TLI]...), s)
		sort.Slice(all, func(i, j int) bool { return all[i].Start < all[j].Start })
		for i := 1; i < len(all); i++ {
			if all[i].Start < all[i-1].End {
				r.emit("archive_reject", map[string]any{
					"reason": "overlap", "tli": s.TLI,
					"prev_end": all[i-1].End, "start": all[i].Start,
				})
				return fmt.Errorf("%w: overlap at %d on tli %d", ErrInvalidSegment, all[i].Start, s.TLI)
			}
		}
		r.segments[s.TLI] = all
		r.emit("archive_ok", map[string]any{"tli": s.TLI, "start": s.Start, "end": s.End})
	}
	return nil
}

// RegisterBackups stores base backups. The upper bound must lie within the
// effective interval of the owning timeline.
func (r *Registry) RegisterBackups(ctx context.Context, bs ...Backup) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	for _, b := range bs {
		r.emit("backup_input", map[string]any{"id": b.ID, "tli": b.TLI, "upper": b.Upper})
		t, ok := r.timelines[b.TLI]
		if !ok {
			r.emit("backup_reject", map[string]any{"reason": "timeline_missing", "tli": b.TLI})
			return fmt.Errorf("%w: timeline %d does not exist", ErrInvalidBackup, b.TLI)
		}
		if b.Upper < t.Fork {
			r.emit("backup_reject", map[string]any{
				"reason": "upper_before_fork", "upper": b.Upper, "fork": t.Fork,
			})
			return fmt.Errorf("%w: backup %s upper %d before tli %d fork %d",
				ErrInvalidBackup, b.ID, b.Upper, b.TLI, t.Fork)
		}
		for _, existing := range r.backups {
			if existing.ID == b.ID {
				r.emit("backup_reject", map[string]any{"reason": "duplicate", "id": b.ID})
				return fmt.Errorf("%w: backup %s already registered", ErrInvalidBackup, b.ID)
			}
		}
		r.backups = append(r.backups, b)
		r.emit("backup_ok", map[string]any{"id": b.ID, "tli": b.TLI, "upper": b.Upper})
	}
	return nil
}

// SnapshotAt returns a stable, sorted copy of the catalog.
func (r *Registry) SnapshotAt(ctx context.Context) Snapshot {
	r.mu.RLock()
	defer r.mu.RUnlock()

	tl := make(map[TimelineID]Timeline, len(r.timelines))
	for id, t := range r.timelines {
		tl[id] = t
	}
	sg := make(map[TimelineID][]Segment, len(r.segments))
	for tli, list := range r.segments {
		cp := append([]Segment(nil), list...)
		sort.Slice(cp, func(i, j int) bool { return cp[i].Start < cp[j].Start })
		sg[tli] = cp
	}
	bp := append([]Backup(nil), r.backups...)
	sort.Slice(bp, func(i, j int) bool {
		if bp[i].Upper != bp[j].Upper {
			return bp[i].Upper < bp[j].Upper
		}
		if bp[i].TLI != bp[j].TLI {
			return bp[i].TLI < bp[j].TLI
		}
		return bp[i].ID < bp[j].ID
	})
	return Snapshot{Timelines: tl, Segments: sg, Backups: bp, MaxTLI: r.maxTLI}
}
