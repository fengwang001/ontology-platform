package quota

import (
	"errors"
	"testing"
)

func errIs(t *testing.T, err error, targets ...error) {
	t.Helper()
	for _, tg := range targets {
		if !errors.Is(err, tg) {
			t.Fatalf("error %v does not match %v", err, tg)
		}
	}
}

// Usage exactly at the limit is allowed; one byte / one entry over is denied.
func TestLimitEquality(t *testing.T) {
	l := New()
	mustOK(t, l.SetQuota(0, 10, 1))
	_, err := l.AddFile(0, 10)
	mustOK(t, err)
	_, err = l.AddFile(0, 1) // both bytes and entries over; bytes first
	errIs(t, err, ErrBytesQuota)

	l = New()
	mustOK(t, l.SetQuota(0, -1, 1))
	_, err = l.Mkdir(0)
	mustOK(t, err)
	_, err = l.Mkdir(0)
	errIs(t, err, ErrEntriesQuota)
	if qe, ok := err.(*QuotaError); !ok || qe.Dir != 0 {
		t.Fatalf("want quota error at 0, got %v", err)
	}
}

// Nearest ancestor first; within one directory bytes before entries.
func TestCheckOrdering(t *testing.T) {
	l := New()
	mustOK(t, l.SetQuota(0, 5, -1))
	d, err := l.Mkdir(0)
	mustOK(t, err)
	mustOK(t, l.SetQuota(d, -1, 0))
	// Nearest directory d is checked first: its entries limit fires even
	// though root's bytes limit would also be violated.
	_, err = l.AddFile(d, 6)
	qe, _ := err.(*QuotaError)
	if qe.Dir != d || !errors.Is(err, ErrEntriesQuota) {
		t.Fatalf("want entries at nearest child, got %v", err)
	}

	// Same directory, both dimensions violated: bytes is reported first.
	l = New()
	mustOK(t, l.SetQuota(0, 5, 0))
	_, err = l.AddFile(d, 1)
	_ = err
	_, err = l.AddFile(0, 6)
	qe, _ = err.(*QuotaError)
	if qe.Dir != 0 || !errors.Is(err, ErrBytesQuota) {
		t.Fatalf("want bytes before entries at root, got %v", err)
	}
}

// AddFile consumes reserve: net increment equality / over-by-one, and reserve
// larger than size is only partially consumed.
func TestAddFileReserveOffset(t *testing.T) {
	l := New()
	mustOK(t, l.SetQuota(0, 100, -1))
	d, _ := l.Mkdir(0)
	mustOK(t, l.Reserve(d, 60))
	// size 50 <= reserve 60: consume exactly 50, reserve left 10, E unchanged.
	_, err := l.AddFile(d, 50)
	mustOK(t, err)
	b, _, r := lUsage(l, 0)
	if b != 50 || r != 10 || b+r != 60 {
		t.Fatalf("got bytes=%d r=%d", b, r)
	}
	// size 50, reserve 10 -> net 40; E(0)=60+40=100 exactly: allowed.
	_, err = l.AddFile(d, 50)
	mustOK(t, err)
	b, _, r = lUsage(l, 0)
	if b != 100 || r != 0 {
		t.Fatalf("got bytes=%d r=%d", b, r)
	}
	// Reserve larger than size: only `size` is consumed.
	l = New()
	mustOK(t, l.SetQuota(0, 10, -1))
	mustOK(t, l.Reserve(0, 10))
	_, err = l.AddFile(0, 3)
	mustOK(t, err)
	b, _, r = lUsage(l, 0)
	if b != 3 || r != 7 {
		t.Fatalf("partial consume: bytes=%d r=%d", b, r)
	}
	// One over the net increment: limit 10, r 7, size 4 -> net -3? no; size 8
	// -> net 1, E=3+7+1=11 > 10.
	_, err = l.AddFile(0, 8)
	errIs(t, err, ErrBytesQuota)
}

func TestReserveReleaseBoundaries(t *testing.T) {
	l := New()
	mustOK(t, l.SetQuota(0, 10, -1))
	mustOK(t, l.Reserve(0, 10)) // exactly at limit
	err := l.Reserve(0, 1)
	errIs(t, err, ErrBytesQuota)

	mustOK(t, l.Release(0, 10)) // exactly r
	err = l.Release(0, 1)
	errIs(t, err, ErrInsuffReserve)

	err = l.Reserve(0, 0)
	errIs(t, err, ErrInvalid)
	err = l.Release(0, -1)
	errIs(t, err, ErrInvalid)
}

// Rename: common ancestors are never rechecked; only the new chain carries
// the payload. Moving a directory takes all its inner entries, bytes and
// reserves with it; the old chain is refunded level by level.
func TestRenameChains(t *testing.T) {
	l := New()
	// Structure: root / a(1) / sub(3) , root / b(2)
	mustOK(t, l.SetQuota(0, 1000, -1))
	a, _ := l.Mkdir(0)
	b, _ := l.Mkdir(0)
	mustOK(t, l.Reserve(a, 30))
	sub, _ := l.Mkdir(a)
	f, err := l.AddFile(sub, 40)
	mustOK(t, err)

	// Tighten root to exactly the current E (70): rename into b also touches
	// root (common ancestor) only — must still succeed.
	mustOK(t, l.SetQuota(0, 70, -1))
	mustOK(t, l.Rename(sub, b))

	// b now carries sub: check b's aggregates.
	bb, be, br := lUsage(l, b)
	if bb != 40 || be != 2 || br != 0 {
		t.Fatalf("b usage bytes=%d entries=%d r=%d", bb, be, br)
	}
	// a keeps its own reserve 30 but lost the subtree bytes/entries.
	ab, ae, ar := lUsage(l, a)
	if ab != 0 || ae != 0 || ar != 30 {
		t.Fatalf("a usage bytes=%d entries=%d r=%d", ab, ae, ar)
	}
	// sub itself moved intact.
	sb, se, _ := lUsage(l, sub)
	if sb != 40 || se != 1 {
		t.Fatalf("sub usage bytes=%d entries=%d", sb, se)
	}

	// New chain limit short by one: sub currently holds f (40 bytes).
	// Set sub's bytes limit to 40; moving another 40-byte file into sub must
	// fail at sub (the new-only directory on the rename chain).
	mustOK(t, l.SetQuota(0, -1, -1))
	g, err := l.AddFile(b, 40)
	mustOK(t, err)
	mustOK(t, l.SetQuota(sub, 40, -1))
	err = l.Rename(g, sub)
	errIs(t, err, ErrBytesQuota)
	if qe, _ := err.(*QuotaError); qe.Dir != sub {
		t.Fatalf("want violation at sub, got %v", err)
	}

	// No-op rename to current parent.
	mustOK(t, l.Rename(f, sub))

	// Structural errors.
	l2 := New()
	a2, _ := l2.Mkdir(0)
	sub2, _ := l2.Mkdir(a2)
	errIs(t, l2.Rename(0, a2), ErrRoot)
	errIs(t, l2.Rename(a2, sub2), ErrBadStructure) // into a descendant
	errIs(t, l2.Rename(a2, a2), ErrBadStructure)   // into itself

	// Move whole reserved directory: reserve travels and refunds old chain.
}

func TestRenameReserveTravels(t *testing.T) {
	l := New()
	mustOK(t, l.SetQuota(0, 100, -1))
	a, _ := l.Mkdir(0)
	b, _ := l.Mkdir(0)
	mustOK(t, l.Reserve(a, 20))
	f, _ := l.AddFile(a, 20) // consumes all reserve; E=20
	// Give a fresh reserve then move the file (reserve stays on a).
	mustOK(t, l.Reserve(a, 10))
	mustOK(t, l.Rename(f, b))
	_, _, ar := lUsage(l, a)
	_, _, br := lUsage(l, b)
	if ar != 10 || br != 0 {
		t.Fatalf("reserve should stay with dir: a r=%d b r=%d", ar, br)
	}

	// Move directory b (empty) under a: no bytes; entries move.
	mustOK(t, l.Rename(b, a))
	ab, ae, ar := lUsage(l, a)
	if ab != 20 || ae != 2 || ar != 10 {
		t.Fatalf("a should contain b subtree: bytes=%d entries=%d r=%d", ab, ae, ar)
	}
	bb, be, _ := lUsage(l, b)
	if bb != 20 || be != 1 {
		t.Fatalf("b keeps its subtree: bytes=%d entries=%d", bb, be)
	}
}

// Resize uses only the difference and ignores reserve; shrinking at a full
// limit is always allowed.
func TestResizeDelta(t *testing.T) {
	l := New()
	mustOK(t, l.SetQuota(0, 10, -1))
	f, _ := l.AddFile(0, 10)
	err := l.Resize(f, 11)
	errIs(t, err, ErrBytesQuota)
	mustOK(t, l.Resize(f, 5))
	mustOK(t, l.Resize(f, 10)) // back to exactly the limit
	mustOK(t, l.Resize(f, 10)) // same size: delta 0

	err = l.Resize(f, -1)
	errIs(t, err, ErrInvalid)
	d, _ := l.Mkdir(0)
	err = l.Resize(d, 1)
	errIs(t, err, ErrWrongType)
}

func TestSetQuotaBoundaries(t *testing.T) {
	l := New()
	mustOK(t, l.SetQuota(0, 10, -1))
	mustOK(t, l.Reserve(0, 10)) // E = 10
	mustOK(t, l.SetQuota(0, 10, -1))
	err := l.SetQuota(0, 9, -1)
	errIs(t, err, ErrBelowUsage)
	err = l.SetQuota(0, -2, -1)
	errIs(t, err, ErrInvalid)
}

// Removing nodes refunds bytes/entries; removing a directory refunds its
// own reserve too (once it is empty).
func TestRemoveRefund(t *testing.T) {
	l := New()
	mustOK(t, l.SetQuota(0, 100, 5))
	d, _ := l.Mkdir(0)
	mustOK(t, l.Reserve(d, 40))
	f, _ := l.AddFile(d, 40) // reserve fully consumed; E(0)=40
	mustOK(t, l.Reserve(d, 10))
	b, e, r := lUsage(l, 0)
	if b != 40 || e != 2 || r != 10 {
		t.Fatalf("before remove: b=%d e=%d r=%d", b, e, r)
	}
	// Non-empty directory cannot be removed.
	err := l.Remove(d)
	errIs(t, err, ErrNotEmpty)

	mustOK(t, l.Remove(f))
	b, e, r = lUsage(l, 0)
	if b != 0 || e != 1 || r != 10 {
		t.Fatalf("after file remove: b=%d e=%d r=%d", b, e, r)
	}
	mustOK(t, l.Remove(d))
	b, e, r = lUsage(l, 0)
	if b != 0 || e != 0 || r != 0 {
		t.Fatalf("after dir remove: b=%d e=%d r=%d", b, e, r)
	}

	// Root cannot be removed; missing nodes are rejected.
	errIs(t, l.Remove(0), ErrRoot)
	errIs(t, l.Remove(99), ErrNotFound)
}
