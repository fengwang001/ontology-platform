package watchnorm

import (
	"errors"
	"fmt"
	"strings"
	"testing"
)

// watcher is the common surface shared by Normalizer and Naive.
type watcher interface {
	Created(now int64, path string, isDir bool) ([]Event, error)
	Deleted(now int64, path string) ([]Event, error)
	MovedFrom(now int64, path string, cookie int64, isDir bool) ([]Event, error)
	MovedTo(now int64, path string, cookie int64, isDir bool) ([]Event, error)
	Overflow(now int64) ([]Event, error)
	Tick(now int64) ([]Event, error)
	WatchedCount() int
	DebugState() string
}

// op is one raw input operation.
type op struct {
	kind   string // "C", "D", "MF", "MT", "O", "T"
	now    int64
	path   string
	cookie int64
	isDir  bool
}

func (o op) String() string {
	switch o.kind {
	case "C":
		return fmt.Sprintf("Created(%d,%q,%v)", o.now, o.path, o.isDir)
	case "D":
		return fmt.Sprintf("Deleted(%d,%q)", o.now, o.path)
	case "MF":
		return fmt.Sprintf("MovedFrom(%d,%q,%d,%v)", o.now, o.path, o.cookie, o.isDir)
	case "MT":
		return fmt.Sprintf("MovedTo(%d,%q,%d,%v)", o.now, o.path, o.cookie, o.isDir)
	case "O":
		return fmt.Sprintf("Overflow(%d)", o.now)
	case "T":
		return fmt.Sprintf("Tick(%d)", o.now)
	}
	return "?"
}

func apply(w watcher, o op) ([]Event, error) {
	switch o.kind {
	case "C":
		return w.Created(o.now, o.path, o.isDir)
	case "D":
		return w.Deleted(o.now, o.path)
	case "MF":
		return w.MovedFrom(o.now, o.path, o.cookie, o.isDir)
	case "MT":
		return w.MovedTo(o.now, o.path, o.cookie, o.isDir)
	case "O":
		return w.Overflow(o.now)
	case "T":
		return w.Tick(o.now)
	}
	panic("bad op kind")
}

func newBoth(t *testing.T, w int, p int64) []watcher {
	t.Helper()
	n, err := New(w, p)
	if err != nil {
		t.Fatalf("New(%d,%d): %v", w, p, err)
	}
	x, err := NewNaive(w, p)
	if err != nil {
		t.Fatalf("NewNaive(%d,%d): %v", w, p, err)
	}
	return []watcher{n, x}
}

func fmtEvents(evs []Event) string {
	if len(evs) == 0 {
		return "[]"
	}
	parts := make([]string, len(evs))
	for i, e := range evs {
		parts[i] = e.String()
	}
	return "[" + strings.Join(parts, " ") + "]"
}

// step is one expected outcome of an op.
type step struct {
	op        op
	wantErr   error
	wantEvent string
}

// replay runs steps against both implementations and checks outputs.
func replay(t *testing.T, w int, p int64, steps []step) {
	t.Helper()
	for _, impl := range newBoth(t, w, p) {
		name := fmt.Sprintf("%T", impl)
		for i, s := range steps {
			evs, err := apply(impl, s.op)
			if !errors.Is(err, s.wantErr) {
				t.Fatalf("%s step %d %s: error = %v, want %v", name, i, s.op, err, s.wantErr)
			}
			if got := fmtEvents(evs); got != s.wantEvent {
				t.Fatalf("%s step %d %s: events = %s, want %s", name, i, s.op, got, s.wantEvent)
			}
			if got := impl.WatchedCount(); got > w {
				t.Fatalf("%s step %d %s: watched count %d exceeds W=%d", name, i, s.op, got, w)
			}
		}
	}
}

func c(now int64, path string, isDir bool) op {
	return op{kind: "C", now: now, path: path, isDir: isDir}
}
func d(now int64, path string) op { return op{kind: "D", now: now, path: path} }
func mf(now int64, path string, cookie int64, isDir bool) op {
	return op{kind: "MF", now: now, path: path, cookie: cookie, isDir: isDir}
}
func mt(now int64, path string, cookie int64, isDir bool) op {
	return op{kind: "MT", now: now, path: path, cookie: cookie, isDir: isDir}
}
func ov(now int64) op { return op{kind: "O", now: now} }
func tk(now int64) op { return op{kind: "T", now: now} }

func ok(o op, events string) step { return step{op: o, wantEvent: events} }
func bad(o op, err error, events string) step {
	return step{op: o, wantErr: err, wantEvent: events}
}

// TestSpecExample replays the worked example from the specification:
// W=2, P=10, with both the paired and the expired MovedTo branches.
func TestSpecExample(t *testing.T) {
	prefix := []step{
		ok(c(0, "a", true), "[Created(a)]"),
		ok(c(0, "b", true), "[Created(b)]"),
		ok(d(1, "a"), "[Deleted(a) Rescan(b)]"),
		ok(mf(2, "b", 7, true), "[]"),
	}
	t.Run("paired", func(t *testing.T) {
		replay(t, 2, 10, append(append([]step{}, prefix...),
			ok(mt(11, "x", 7, true), "[Renamed(b->x)]")))
	})
	t.Run("expired", func(t *testing.T) {
		replay(t, 2, 10, append(append([]step{}, prefix...),
			ok(mt(12, "x", 7, true), "[Deleted(b) Created(x) Rescan(x)]")))
	})
}

// TestPendingSameNameCreate covers creating a new entry at a path that
// is currently held by a pending move record: the create succeeds, and
// the pending record can still pair afterwards.
func TestPendingSameNameCreate(t *testing.T) {
	replay(t, 2, 10, []step{
		ok(c(0, "b", true), "[Created(b)]"), // watched (root+b = 2)
		ok(mf(1, "b", 7, true), "[]"),       // pending, still holds its slot
		ok(c(2, "b", true), "[Created(b)]"), // new b, unwatched (slots full)
		ok(mt(3, "x", 7, true), "[Renamed(b->x)]"),
		// After pairing, slots: root + x(watched) = 2, b stays unwatched.
		ok(d(4, "x"), "[Deleted(x) Rescan(b)]"),
	})
}

// TestExpiryExactlyAtP covers the boundary now-t == P: the record
// expires (>= P), while now-t == P-1 still pairs (< P).
func TestExpiryExactlyAtP(t *testing.T) {
	t.Run("expired_at_P", func(t *testing.T) {
		replay(t, 2, 10, []step{
			ok(c(0, "a", true), "[Created(a)]"),
			ok(mf(1, "a", 7, true), "[]"),
			ok(tk(11), "[Deleted(a)]"), // 11-1 = 10 >= P
			ok(tk(12), "[]"),
		})
	})
	t.Run("pairs_at_P_minus_1", func(t *testing.T) {
		replay(t, 2, 10, []step{
			ok(c(0, "a", true), "[Created(a)]"),
			ok(mf(1, "a", 7, true), "[]"),
			ok(mt(10, "x", 7, true), "[Renamed(a->x)]"), // 10-1 = 9 < P
		})
	})
	t.Run("moveto_at_P_recreates", func(t *testing.T) {
		replay(t, 2, 10, []step{
			ok(c(0, "a", true), "[Created(a)]"),
			ok(mf(1, "a", 7, true), "[]"),
			ok(mt(11, "x", 7, true), "[Deleted(a) Created(x) Rescan(x)]"),
		})
	})
}

// TestPrefixSegmentBoundary covers subtree moves where sibling names
// share a byte prefix: detaching "a/b" must not touch "a/bc".
func TestPrefixSegmentBoundary(t *testing.T) {
	replay(t, 5, 10, []step{
		ok(c(0, "a", true), "[Created(a)]"),
		ok(c(0, "a/b", true), "[Created(a/b)]"),
		ok(c(0, "a/bc", true), "[Created(a/bc)]"),
		ok(c(0, "a/b/f", false), "[Created(a/b/f)]"),
		ok(c(0, "a/bc/f", false), "[Created(a/bc/f)]"),
		ok(mf(1, "a/b", 3, true), "[]"),
		// "a/bc" and its child are still known and watched.
		ok(c(1, "a/bc/g", false), "[Created(a/bc/g)]"),
		ok(mt(2, "x", 3, true), "[Renamed(a/b->x)]"),
		// The moved subtree is intact under its new prefix.
		ok(d(3, "x/f"), "[Deleted(x/f)]"),
		ok(d(3, "a/bc/f"), "[Deleted(a/bc/f)]"),
		ok(d(4, "a/bc"), "[Deleted(a/bc)]"),
		bad(d(5, "a/b"), ErrUnknown, "[]"),
	})
}

// TestBackfillOrderMultiRelease covers multiple slots freed by one
// delete: backfill promotes unwatched directories in byte order.
func TestBackfillOrderMultiRelease(t *testing.T) {
	replay(t, 3, 10, []step{
		ok(c(0, "z", true), "[Created(z)]"),     // watched: "", z
		ok(c(0, "z/m", true), "[Created(z/m)]"), // watched: "", z, z/m (full)
		ok(c(0, "b", true), "[Created(b)]"),     // unwatched
		ok(c(0, "a", true), "[Created(a)]"),     // unwatched
		// Deleting z frees two slots; byte order promotes a then b.
		ok(d(1, "z"), "[Deleted(z) Rescan(a) Rescan(b)]"),
	})
}

// TestOverflowDropsPending covers Overflow: Rescan("") first, pending
// records dropped without Deleted events, slots released, then backfill.
func TestOverflowDropsPending(t *testing.T) {
	replay(t, 2, 10, []step{
		ok(c(0, "a", true), "[Created(a)]"),
		ok(mf(1, "a", 7, true), "[]"),                       // pending, holds a slot
		ok(c(2, "b", true), "[Created(b)]"),                 // unwatched (slots full)
		ok(ov(3), "[Rescan() Rescan(b)]"),                   // pending dropped, b promoted
		ok(tk(100), "[]"),                                   // nothing left to expire
		ok(mt(100, "y", 7, true), "[Created(y) Rescan(y)]"), // cookie record is gone
	})
}

// TestEntryProcessingOnRejection covers entry processing taking effect
// even when the operation itself is then rejected.
func TestEntryProcessingOnRejection(t *testing.T) {
	replay(t, 2, 10, []step{
		ok(c(0, "b", true), "[Created(b)]"),
		ok(mf(1, "b", 7, true), "[]"),
		// Entry processing expires b (Deleted event) even though the
		// delete itself is rejected; the expiry is not rolled back.
		bad(d(11, "nope"), ErrUnknown, "[Deleted(b)]"),
		ok(tk(11), "[]"),
		ok(c(11, "b", false), "[Created(b)]"), // b is really gone
	})
}

// TestErrorPriority covers the documented error ordering.
func TestErrorPriority(t *testing.T) {
	t.Run("clock_before_badarg", func(t *testing.T) {
		replay(t, 2, 10, []step{
			ok(c(5, "a", true), "[Created(a)]"),
			bad(c(4, "/bad", true), ErrClock, "[]"),  // clock wins over bad path
			bad(c(5, "/bad", true), ErrBadArg, "[]"), // now ok, path bad
			bad(c(5, "", true), ErrBadArg, "[]"),     // root path rejected
			bad(d(5, ""), ErrBadArg, "[]"),
			bad(mf(5, "", 1, true), ErrBadArg, "[]"),
			bad(mt(5, "", 1, true), ErrBadArg, "[]"),
			bad(mf(5, "a", 0, true), ErrBadArg, "[]"), // cookie < 1
			bad(mt(5, "a", -3, true), ErrBadArg, "[]"),
			bad(c(5, "a//b", true), ErrBadArg, "[]"),
			bad(c(5, "a/./b", true), ErrBadArg, "[]"),
			bad(c(5, "a/../b", true), ErrBadArg, "[]"),
			bad(c(5, "/a", true), ErrBadArg, "[]"),
			bad(c(5, "a/", true), ErrBadArg, "[]"),
		})
	})
	t.Run("created_noparent_before_exists", func(t *testing.T) {
		replay(t, 3, 10, []step{
			ok(c(0, "a", false), "[Created(a)]"),
			bad(c(0, "q/x", true), ErrNoParent, "[]"),
			bad(c(0, "a", true), ErrExists, "[]"),
			bad(c(0, "a/b", false), ErrNoParent, "[]"), // a is a file
		})
	})
	t.Run("movedfrom_unknown_before_dupcookie", func(t *testing.T) {
		replay(t, 3, 10, []step{
			ok(c(0, "a", true), "[Created(a)]"),
			ok(mf(1, "a", 7, true), "[]"),
			bad(mf(1, "ghost", 7, true), ErrUnknown, "[]"), // unknown wins
			bad(mf(1, "ghost2", 7, false), ErrUnknown, "[]"),
			bad(mf(1, "a", 8, true), ErrUnknown, "[]"), // a is detached now
		})
	})
	t.Run("movedfrom_kind_mismatch_is_unknown", func(t *testing.T) {
		replay(t, 3, 10, []step{
			ok(c(0, "a", true), "[Created(a)]"),
			ok(c(0, "f", false), "[Created(f)]"),
			bad(mf(1, "a", 1, false), ErrUnknown, "[]"), // dir passed as file
			bad(mf(1, "f", 1, true), ErrUnknown, "[]"),  // file passed as dir
		})
	})
	t.Run("moveto_mismatch_first_and_record_kept", func(t *testing.T) {
		replay(t, 3, 10, []step{
			ok(c(0, "a", true), "[Created(a)]"),
			ok(mf(1, "a", 7, true), "[]"),
			// Mismatch outranks ErrNoParent/ErrExists; record is kept.
			bad(mt(2, "q/x", 7, false), ErrMismatch, "[]"),
			bad(mt(2, "x", 7, false), ErrMismatch, "[]"),
			bad(mt(2, "q/x", 7, true), ErrNoParent, "[]"),
			ok(mt(3, "x", 7, true), "[Renamed(a->x)]"),
		})
	})
	t.Run("moveto_pairing_exists", func(t *testing.T) {
		replay(t, 3, 10, []step{
			ok(c(0, "a", true), "[Created(a)]"),
			ok(c(0, "x", false), "[Created(x)]"),
			ok(mf(1, "a", 7, true), "[]"),
			bad(mt(2, "x", 7, true), ErrExists, "[]"),
			ok(mt(3, "y", 7, true), "[Renamed(a->y)]"),
		})
	})
	t.Run("dupcookie", func(t *testing.T) {
		replay(t, 4, 10, []step{
			ok(c(0, "a", true), "[Created(a)]"),
			ok(c(0, "b", true), "[Created(b)]"),
			ok(mf(1, "a", 7, true), "[]"),
			bad(mf(1, "b", 7, true), ErrDupCookie, "[]"),
		})
	})
	t.Run("clock_monotonic_after_errors", func(t *testing.T) {
		replay(t, 2, 10, []step{
			ok(c(5, "a", true), "[Created(a)]"),
			bad(c(9, "/bad", true), ErrBadArg, "[]"), // lastNow advances to 9
			bad(c(7, "b", true), ErrClock, "[]"),
			ok(c(9, "b", true), "[Created(b)]"),
		})
	})
}

// TestUnwatchedSubtreeOps covers events under unwatched directories
// being processed normally.
func TestUnwatchedSubtreeOps(t *testing.T) {
	replay(t, 2, 10, []step{
		ok(c(0, "a", true), "[Created(a)]"),     // watched (full)
		ok(c(0, "u", true), "[Created(u)]"),     // unwatched
		ok(c(1, "u/x", true), "[Created(u/x)]"), // unwatched too
		ok(c(1, "u/x/f", false), "[Created(u/x/f)]"),
		ok(d(2, "u/x/f"), "[Deleted(u/x/f)]"),
		ok(mf(3, "u/x", 5, true), "[]"),
		ok(mt(4, "u/y", 5, true), "[Renamed(u/x->u/y)]"),
	})
}

// TestMovedToFreshCreate covers MovedTo without a matching record.
func TestMovedToFreshCreate(t *testing.T) {
	replay(t, 2, 10, []step{
		ok(mt(0, "d", 9, true), "[Created(d) Rescan(d)]"), // dir: extra Rescan
		ok(mt(0, "f", 9, false), "[Created(f)]"),          // file: no Rescan
		bad(mt(0, "q/x", 9, true), ErrNoParent, "[]"),
		bad(mt(0, "d", 9, true), ErrExists, "[]"),
		// Slots full (root + d): a fresh dir is unwatched but still rescanned.
		ok(mt(0, "e", 9, true), "[Created(e) Rescan(e)]"),
	})
}

// TestMultipleExpiryOrder covers several records expiring at once:
// Deleted events are emitted in (t, cookie) order, then one backfill.
func TestMultipleExpiryOrder(t *testing.T) {
	replay(t, 4, 10, []step{
		ok(c(0, "a", true), "[Created(a)]"),
		ok(c(0, "b", true), "[Created(b)]"),
		ok(c(0, "c", true), "[Created(c)]"),
		ok(mf(1, "a", 8, true), "[]"),
		ok(mf(2, "c", 7, true), "[]"),
		ok(mf(2, "b", 9, true), "[]"),
		// Expire order: (t=1,c=8) a, then (t=2,c=7) c, then (t=2,c=9) b.
		ok(tk(12), "[Deleted(a) Deleted(c) Deleted(b)]"),
	})
}

// TestConstructorBadArgs covers New argument validation.
func TestConstructorBadArgs(t *testing.T) {
	if _, err := New(0, 1); !errors.Is(err, ErrBadArg) {
		t.Fatalf("New(0,1) err = %v", err)
	}
	if _, err := New(1, 0); !errors.Is(err, ErrBadArg) {
		t.Fatalf("New(1,0) err = %v", err)
	}
	if _, err := NewNaive(0, 1); !errors.Is(err, ErrBadArg) {
		t.Fatalf("NewNaive(0,1) err = %v", err)
	}
}

// TestRenameSubtreeWatchPreserved covers watch states inside a renamed
// subtree being preserved, including unwatched inner directories.
func TestRenameSubtreeWatchPreserved(t *testing.T) {
	replay(t, 2, 10, []step{
		ok(c(0, "a", true), "[Created(a)]"),     // watched, slots full
		ok(c(0, "a/u", true), "[Created(a/u)]"), // unwatched inner dir
		ok(mf(1, "a", 4, true), "[]"),
		// Pairing brings back a watched dir; inner u stays unwatched
		// because no slot is free, so no backfill Rescan follows.
		ok(mt(2, "z", 4, true), "[Renamed(a->z)]"),
		ok(d(3, "z"), "[Deleted(z)]"),
	})
}
