package fdb_test

import (
	"errors"
	"fmt"
	"math/rand"
	"reflect"
	"sync"
	"sync/atomic"
	"testing"

	"ontology/fdb"
)

// ---------------------------------------------------------------------------
// helpers
// ---------------------------------------------------------------------------

// umac returns the unicast MAC 02:00:00:00:00:b.
func umac(b byte) fdb.MAC { return fdb.MAC{0x02, 0, 0, 0, 0, b} }

// mcastMAC returns a multicast (but not broadcast) MAC.
func mcastMAC(b byte) fdb.MAC { return fdb.MAC{0x01, 0x00, 0x5e, 0, 0, b} }

var broadcast = fdb.MAC{0xff, 0xff, 0xff, 0xff, 0xff, 0xff}

func mustNew(t *testing.T, n int, aging int64, capacity int) *fdb.FDB {
	t.Helper()
	sw, err := fdb.New(n, aging, capacity)
	if err != nil {
		t.Fatalf("New(%d, %d, %d): %v", n, aging, capacity, err)
	}
	return sw
}

func mustFrame(t *testing.T, sw *fdb.FDB, p int, s, d fdb.MAC, v uint16, ts int64) []int {
	t.Helper()
	out, err := sw.Frame(p, s, d, v, ts)
	if err != nil {
		t.Fatalf("Frame(p=%d s=%v d=%v v=%d t=%d): %v", p, s, d, v, ts, err)
	}
	return out
}

func checkOut(t *testing.T, what string, got, want []int) {
	t.Helper()
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("%s: got %v, want %v", what, got, want)
	}
}

func checkInvariant(t *testing.T, sw *fdb.FDB) {
	t.Helper()
	c := sw.Counters()
	lhs := uint64(sw.Len()) + c.Evictions + c.Expired + c.Flushed + c.Overridden
	if lhs != c.Learned {
		t.Fatalf("invariant broken: Len(%d)+Evictions(%d)+Expired(%d)+Flushed(%d)+Overridden(%d)=%d != Learned(%d)",
			sw.Len(), c.Evictions, c.Expired, c.Flushed, c.Overridden, lhs, c.Learned)
	}
}

// ---------------------------------------------------------------------------
// unit tests
// ---------------------------------------------------------------------------

// Aging is left-closed: an entry is expired exactly when t-seen >= A.
func TestAgingBoundary(t *testing.T) {
	const A = 100
	sw := mustNew(t, 4, A, 8)
	a, b := umac(0x0a), umac(0x0b)

	checkOut(t, "t=0 learn a, unknown b floods",
		mustFrame(t, sw, 0, a, b, 1, 0), []int{1, 2, 3})
	checkOut(t, "t=0 learn b, a known",
		mustFrame(t, sw, 1, b, a, 1, 0), []int{0})

	// t-seen = A-1: entry still valid, both for Lookup and forwarding.
	if port, ok := sw.Lookup(1, a, A-1); !ok || port != 0 {
		t.Fatalf("Lookup at A-1: got (%d,%v), want (0,true)", port, ok)
	}
	checkOut(t, "t=A-1 a still valid",
		mustFrame(t, sw, 1, b, a, 1, A-1), []int{0})

	// Lookup is read-only: expired entries count as miss but are not
	// purged, so Len still includes them.
	if _, ok := sw.Lookup(1, a, A); ok {
		t.Fatalf("Lookup at A: want miss")
	}
	if got := sw.Len(); got != 2 {
		t.Fatalf("Len before purge: got %d, want 2 (expired not yet purged)", got)
	}

	// t-seen = A: the mutating call purges a first, then floods.
	checkOut(t, "t=A a expired, flood",
		mustFrame(t, sw, 1, b, a, 1, A), []int{0, 2, 3})
	if got := sw.Len(); got != 1 {
		t.Fatalf("Len after purge: got %d, want 1", got)
	}
	c := sw.Counters()
	if c.Expired != 1 || c.Learned != 2 || c.Floods != 2 {
		t.Fatalf("counters: %+v", c)
	}
	checkInvariant(t, sw)
}

// Re-learning on the same port refreshes seen without counting a move;
// learning on a different port moves the entry.
func TestRefreshSamePortAndMove(t *testing.T) {
	const A = 100
	sw := mustNew(t, 4, A, 8)
	x, mc := umac(0x01), mcastMAC(0x01)

	mustFrame(t, sw, 0, x, mc, 1, 0)
	mustFrame(t, sw, 0, x, mc, 1, 50) // same port: refresh only
	if got := sw.Counters().Moves; got != 0 {
		t.Fatalf("Moves after same-port refresh: got %d, want 0", got)
	}
	// Without the refresh at t=50 the entry would have expired at t=100.
	mustFrame(t, sw, 2, x, mc, 1, 120) // move to port 2
	c := sw.Counters()
	if c.Moves != 1 || c.Learned != 1 || c.Expired != 0 {
		t.Fatalf("counters: %+v, want Moves=1 Learned=1 Expired=0", c)
	}
	if port, ok := sw.Lookup(1, x, 219); !ok || port != 2 {
		t.Fatalf("Lookup at 219: got (%d,%v), want (2,true)", port, ok)
	}
	if _, ok := sw.Lookup(1, x, 220); ok {
		t.Fatalf("Lookup at 220: want miss (seen=120, A=100)")
	}
	checkInvariant(t, sw)
}

// Static entries are protected: learning never modifies them, a frame
// from the static MAC on a foreign port is dropped, static entries never
// expire, and AddStatic updates / overrides as specified.
func TestStaticProtection(t *testing.T) {
	sw := mustNew(t, 4, 100, 8)
	s, other := umac(0x0a), umac(0x0b)

	if err := sw.AddStatic(1, s, 3, 0); err != nil {
		t.Fatalf("AddStatic: %v", err)
	}
	// Frame from the static MAC on its own port: learned side is a no-op,
	// forwarding proceeds (unknown unicast dest -> flood).
	checkOut(t, "static src on own port",
		mustFrame(t, sw, 3, s, other, 1, 1), []int{0, 1, 2})
	// Frame from the static MAC on a foreign port: dropped, no forwarding.
	checkOut(t, "static src on foreign port dropped",
		mustFrame(t, sw, 1, s, other, 1, 2), []int{})
	if got := sw.Counters().SecurityDrops; got != 1 {
		t.Fatalf("SecurityDrops: got %d, want 1", got)
	}
	// The drop must not have modified the static entry.
	if port, ok := sw.Lookup(1, s, 2); !ok || port != 3 {
		t.Fatalf("static entry after drop: got (%d,%v), want (3,true)", port, ok)
	}
	// Static entries never expire.
	checkOut(t, "static dest reachable far in the future",
		mustFrame(t, sw, 0, other, s, 1, 100000), []int{3})

	// AddStatic on an existing static entry only changes the port.
	if err := sw.AddStatic(1, s, 2, 100001); err != nil {
		t.Fatalf("AddStatic update: %v", err)
	}
	if port, ok := sw.Lookup(1, s, 100001); !ok || port != 2 {
		t.Fatalf("static port update: got (%d,%v), want (2,true)", port, ok)
	}
	if got := sw.Counters().Overridden; got != 0 {
		t.Fatalf("Overridden after static->static: got %d, want 0", got)
	}

	// AddStatic over an existing dynamic entry overrides it.
	d := umac(0x0c)
	mustFrame(t, sw, 0, d, s, 1, 100002) // learns d dynamically on port 0
	if got := sw.Len(); got != 2 {
		t.Fatalf("Len before override: got %d, want 2", got)
	}
	if err := sw.AddStatic(1, d, 1, 100003); err != nil {
		t.Fatalf("AddStatic override: %v", err)
	}
	c := sw.Counters()
	if c.Overridden != 1 {
		t.Fatalf("Overridden: got %d, want 1", c.Overridden)
	}
	if got := sw.Len(); got != 1 {
		t.Fatalf("Len after override: got %d, want 1 (static does not count)", got)
	}
	if port, ok := sw.Lookup(1, d, 100003); !ok || port != 1 {
		t.Fatalf("overridden entry: got (%d,%v), want (1,true)", port, ok)
	}
	checkInvariant(t, sw)
}

// At capacity, learning a new entry evicts the dynamic entry with the
// smallest seen; ties break by (VLAN, MAC) in byte order.
func TestEviction(t *testing.T) {
	// Oldest-seen eviction.
	sw := mustNew(t, 4, 1000, 2)
	a, b, c := umac(0x0a), umac(0x0b), umac(0x0c)
	mc := mcastMAC(0x01)
	mustFrame(t, sw, 0, a, mc, 1, 0)
	mustFrame(t, sw, 1, b, mc, 1, 1)
	mustFrame(t, sw, 2, c, mc, 1, 2) // evicts a (oldest)
	if got := sw.Counters().Evictions; got != 1 {
		t.Fatalf("Evictions: got %d, want 1", got)
	}
	if _, ok := sw.Lookup(1, a, 2); ok {
		t.Fatalf("a should have been evicted")
	}
	if _, ok := sw.Lookup(1, b, 2); !ok {
		t.Fatalf("b should survive")
	}
	if _, ok := sw.Lookup(1, c, 2); !ok {
		t.Fatalf("c should be present")
	}
	checkInvariant(t, sw)

	// Tie on seen: smallest (VLAN, MAC) byte order wins; VLAN is the
	// most significant part of the key.
	sw2 := mustNew(t, 4, 1000, 2)
	smallMAC, bigMAC := umac(0x01), umac(0xfe)
	mustFrame(t, sw2, 0, smallMAC, mc, 2, 5) // (v=2, small mac), seen 5
	mustFrame(t, sw2, 1, bigMAC, mc, 1, 5)   // (v=1, big mac), seen 5
	mustFrame(t, sw2, 2, umac(0x77), mc, 3, 5)
	if _, ok := sw2.Lookup(1, bigMAC, 5); ok {
		t.Fatalf("tie-break: (v=1, big mac) should be evicted (smaller VLAN)")
	}
	if _, ok := sw2.Lookup(2, smallMAC, 5); !ok {
		t.Fatalf("tie-break: (v=2, small mac) should survive")
	}

	// Tie on seen and VLAN: smallest MAC wins.
	sw3 := mustNew(t, 4, 1000, 2)
	mustFrame(t, sw3, 0, umac(0x10), mc, 1, 0)
	mustFrame(t, sw3, 1, umac(0x20), mc, 1, 0)
	mustFrame(t, sw3, 2, umac(0x30), mc, 1, 0)
	if _, ok := sw3.Lookup(1, umac(0x10), 0); ok {
		t.Fatalf("tie-break: smallest MAC should be evicted")
	}
	if _, ok := sw3.Lookup(1, umac(0x20), 0); !ok {
		t.Fatalf("tie-break: larger MAC should survive")
	}
	checkInvariant(t, sw3)
}

// Multicast (and broadcast) sources are never learned, but their frames
// are still forwarded.
func TestMulticastSourceNotLearned(t *testing.T) {
	sw := mustNew(t, 4, 100, 8)
	dst := umac(0x0d)
	checkOut(t, "multicast source, unknown dest floods",
		mustFrame(t, sw, 0, mcastMAC(0x01), dst, 1, 0), []int{1, 2, 3})
	checkOut(t, "broadcast source, unknown dest floods",
		mustFrame(t, sw, 1, broadcast, dst, 1, 1), []int{0, 2, 3})
	if got := sw.Len(); got != 0 {
		t.Fatalf("Len: got %d, want 0 (multicast sources not learned)", got)
	}
	c := sw.Counters()
	if c.Learned != 0 || c.Floods != 2 {
		t.Fatalf("counters: %+v, want Learned=0 Floods=2", c)
	}
	checkInvariant(t, sw)
}

// Unknown unicast and multicast destinations flood to every port except
// the ingress port, in ascending order.
func TestFloodSets(t *testing.T) {
	sw := mustNew(t, 4, 100, 8)
	src, unknown := umac(0x01), umac(0x02)
	checkOut(t, "unknown unicast floods",
		mustFrame(t, sw, 2, src, unknown, 1, 0), []int{0, 1, 3})
	checkOut(t, "multicast dest floods",
		mustFrame(t, sw, 2, src, mcastMAC(0x09), 1, 1), []int{0, 1, 3})
	checkOut(t, "broadcast dest floods",
		mustFrame(t, sw, 0, src, broadcast, 1, 2), []int{1, 2, 3})
	if got := sw.Counters().Floods; got != 3 {
		t.Fatalf("Floods: got %d, want 3", got)
	}

	// Single-port switch floods to the empty set.
	sw1 := mustNew(t, 1, 100, 8)
	checkOut(t, "single port flood is empty",
		mustFrame(t, sw1, 0, src, unknown, 1, 0), []int{})
	if got := sw1.Counters().Floods; got != 1 {
		t.Fatalf("Floods: got %d, want 1", got)
	}
}

// A unicast frame whose source equals its destination is learned first,
// then filtered because the destination is known on the ingress port.
func TestSourceEqualsDestination(t *testing.T) {
	sw := mustNew(t, 4, 100, 8)
	x := umac(0x01)
	checkOut(t, "src==dst filtered",
		mustFrame(t, sw, 1, x, x, 1, 0), []int{})
	c := sw.Counters()
	if c.Learned != 1 || c.Filtered != 1 {
		t.Fatalf("counters: %+v, want Learned=1 Filtered=1", c)
	}
	if port, ok := sw.Lookup(1, x, 0); !ok || port != 1 {
		t.Fatalf("Lookup: got (%d,%v), want (1,true)", port, ok)
	}
	checkInvariant(t, sw)
}

// FlushPort removes all dynamic entries on a port, leaves static entries
// and other ports alone, and subsequent frames to removed hosts flood.
func TestFlushPort(t *testing.T) {
	sw := mustNew(t, 4, 1000, 8)
	a, b, c, s := umac(0x0a), umac(0x0b), umac(0x0c), umac(0x0d)
	mc := mcastMAC(0x01)
	mustFrame(t, sw, 0, a, mc, 1, 0)
	mustFrame(t, sw, 1, b, mc, 1, 1)
	mustFrame(t, sw, 2, c, mc, 1, 2)
	if err := sw.AddStatic(1, s, 2, 3); err != nil {
		t.Fatalf("AddStatic: %v", err)
	}

	if err := sw.FlushPort(2, 4); err != nil {
		t.Fatalf("FlushPort: %v", err)
	}
	if got := sw.Counters().Flushed; got != 1 {
		t.Fatalf("Flushed: got %d, want 1 (only c)", got)
	}
	if _, ok := sw.Lookup(1, c, 4); ok {
		t.Fatalf("c should be flushed")
	}
	if port, ok := sw.Lookup(1, s, 4); !ok || port != 2 {
		t.Fatalf("static s must survive FlushPort: got (%d,%v)", port, ok)
	}
	// Traffic to the flushed host now floods again.
	checkOut(t, "dest flushed -> flood",
		mustFrame(t, sw, 0, a, c, 1, 5), []int{1, 2, 3})
	// Traffic to the static host on the flushed port is unaffected.
	checkOut(t, "static dest still unicast",
		mustFrame(t, sw, 0, a, s, 1, 6), []int{2})

	if err := sw.FlushPort(0, 7); err != nil {
		t.Fatalf("FlushPort: %v", err)
	}
	if got := sw.Counters().Flushed; got != 2 {
		t.Fatalf("Flushed: got %d, want 2", got)
	}
	checkInvariant(t, sw)
}

// Constructor and call validation: every rejection is errors.Is-able and
// leaves state, clock, and counters untouched.
func TestValidationAndClock(t *testing.T) {
	for _, cfg := range [][3]int{{0, 1, 1}, {65, 1, 1}, {1, 0, 1}, {1, -1, 1}, {1, 1, 0}, {1, 1, -1}} {
		if _, err := fdb.New(cfg[0], int64(cfg[1]), cfg[2]); !errors.Is(err, fdb.ErrInvalidConfig) {
			t.Fatalf("New%v: got %v, want ErrInvalidConfig", cfg, err)
		}
	}

	sw := mustNew(t, 4, 100, 8)
	a, b := umac(0x0a), umac(0x0b)
	mustFrame(t, sw, 0, a, b, 1, 10)
	before := sw.Counters()
	beforeLen := sw.Len()

	checkUnchanged := func(what string) {
		t.Helper()
		if got := sw.Counters(); got != before {
			t.Fatalf("%s changed counters: %+v -> %+v", what, before, got)
		}
		if got := sw.Len(); got != beforeLen {
			t.Fatalf("%s changed Len: %d -> %d", what, beforeLen, got)
		}
	}

	if _, err := sw.Frame(4, a, b, 1, 10); !errors.Is(err, fdb.ErrPortOutOfRange) {
		t.Fatalf("bad port: %v", err)
	}
	if _, err := sw.Frame(-1, a, b, 1, 10); !errors.Is(err, fdb.ErrPortOutOfRange) {
		t.Fatalf("negative port: %v", err)
	}
	if _, err := sw.Frame(0, a, b, 0, 10); !errors.Is(err, fdb.ErrVLANOutOfRange) {
		t.Fatalf("vlan 0: %v", err)
	}
	if _, err := sw.Frame(0, a, b, 4095, 10); !errors.Is(err, fdb.ErrVLANOutOfRange) {
		t.Fatalf("vlan 4095: %v", err)
	}
	if err := sw.AddStatic(1, mcastMAC(0x01), 0, 10); !errors.Is(err, fdb.ErrMulticastStaticMAC) {
		t.Fatalf("static multicast mac: %v", err)
	}
	if err := sw.AddStatic(1, broadcast, 0, 10); !errors.Is(err, fdb.ErrMulticastStaticMAC) {
		t.Fatalf("static broadcast mac: %v", err)
	}
	if err := sw.FlushPort(9, 10); !errors.Is(err, fdb.ErrPortOutOfRange) {
		t.Fatalf("flush bad port: %v", err)
	}
	checkUnchanged("parameter rejections")

	// Clock may not go backwards; rejected calls do not rewind it.
	if _, err := sw.Frame(0, a, b, 1, 9); !errors.Is(err, fdb.ErrClockBackward) {
		t.Fatalf("backward frame: %v", err)
	}
	if err := sw.AddStatic(1, b, 1, 5); !errors.Is(err, fdb.ErrClockBackward) {
		t.Fatalf("backward addstatic: %v", err)
	}
	if err := sw.FlushPort(0, 5); !errors.Is(err, fdb.ErrClockBackward) {
		t.Fatalf("backward flush: %v", err)
	}
	checkUnchanged("clock rejections")

	// Equal timestamps are allowed; the clock was not rewound by the
	// rejected calls above.
	if _, err := sw.Frame(0, a, b, 1, 10); err != nil {
		t.Fatalf("equal timestamp must be accepted: %v", err)
	}
	checkInvariant(t, sw)
}

// ---------------------------------------------------------------------------
// naive reference implementation
//
// A deliberately simple, per-rule linear-scan implementation used as a
// differential oracle. It shares no code with the real FDB.
// ---------------------------------------------------------------------------

type naiveEntry struct {
	key    fdb.Key
	port   int
	seen   int64
	static bool
}

type naiveFDB struct {
	n       int
	aging   int64
	cap     int
	ents    []naiveEntry
	last    int64
	hasLast bool
	ctr     fdb.Counters
	log     []string // decision log for the current call
}

func newNaive(n int, aging int64, capacity int) *naiveFDB {
	return &naiveFDB{n: n, aging: aging, cap: capacity}
}

func (nf *naiveFDB) logf(format string, args ...any) {
	nf.log = append(nf.log, fmt.Sprintf(format, args...))
}

func (nf *naiveFDB) find(k fdb.Key) int {
	for i := range nf.ents {
		if nf.ents[i].key == k {
			return i
		}
	}
	return -1
}

func (nf *naiveFDB) dynLen() int {
	n := 0
	for _, e := range nf.ents {
		if !e.static {
			n++
		}
	}
	return n
}

func (nf *naiveFDB) checkClock(t int64) error {
	if nf.hasLast && t < nf.last {
		return fdb.ErrClockBackward
	}
	return nil
}

func (nf *naiveFDB) advance(t int64) {
	nf.last = t
	nf.hasLast = true
	nf.purge(t)
}

func (nf *naiveFDB) purge(t int64) {
	kept := nf.ents[:0]
	for _, e := range nf.ents {
		if !e.static && t-e.seen >= nf.aging {
			nf.ctr.Expired++
			nf.logf("purge expired (%d,%v) seen=%d", e.key.VLAN, e.key.MAC, e.seen)
			continue
		}
		kept = append(kept, e)
	}
	nf.ents = kept
}

func (nf *naiveFDB) evictOldest() {
	victim := -1
	for i, e := range nf.ents {
		if e.static {
			continue
		}
		if victim < 0 || e.seen < nf.ents[victim].seen ||
			(e.seen == nf.ents[victim].seen && naiveKeyLess(e.key, nf.ents[victim].key)) {
			victim = i
		}
	}
	if victim >= 0 {
		nf.logf("evict (%d,%v) seen=%d", nf.ents[victim].key.VLAN, nf.ents[victim].key.MAC, nf.ents[victim].seen)
		nf.ents = append(nf.ents[:victim], nf.ents[victim+1:]...)
		nf.ctr.Evictions++
	}
}

func naiveKeyLess(a, b fdb.Key) bool {
	if a.VLAN != b.VLAN {
		return a.VLAN < b.VLAN
	}
	for i := 0; i < 6; i++ {
		if a.MAC[i] != b.MAC[i] {
			return a.MAC[i] < b.MAC[i]
		}
	}
	return false
}

// frame mirrors FDB.Frame with linear scans and records its decisions.
func (nf *naiveFDB) frame(p int, s, d fdb.MAC, v uint16, t int64) ([]int, error) {
	nf.log = nf.log[:0]
	if p < 0 || p >= nf.n {
		return nil, fdb.ErrPortOutOfRange
	}
	if v < 1 || v > 4094 {
		return nil, fdb.ErrVLANOutOfRange
	}
	if err := nf.checkClock(t); err != nil {
		return nil, err
	}
	nf.advance(t)

	if !s.IsMulticast() {
		k := fdb.Key{VLAN: v, MAC: s}
		idx := nf.find(k)
		switch {
		case idx < 0:
			if nf.dynLen() == nf.cap {
				nf.evictOldest()
			}
			nf.ents = append(nf.ents, naiveEntry{key: k, port: p, seen: t})
			nf.ctr.Learned++
			nf.logf("learn (%d,%v) port=%d", v, s, p)
		case nf.ents[idx].static:
			if nf.ents[idx].port != p {
				nf.ctr.SecurityDrops++
				nf.logf("static src (%d,%v) on foreign port %d: drop", v, s, p)
				return []int{}, nil
			}
			nf.logf("static src (%d,%v) on own port: no-op", v, s)
		default:
			if nf.ents[idx].port != p {
				nf.ctr.Moves++
				nf.logf("move (%d,%v) port %d->%d", v, s, nf.ents[idx].port, p)
			} else {
				nf.logf("refresh (%d,%v) port=%d", v, s, p)
			}
			nf.ents[idx].port = p
			nf.ents[idx].seen = t
		}
	} else {
		nf.logf("multicast src %v: not learned", s)
	}

	flood := func() []int {
		out := make([]int, 0, nf.n-1)
		for port := 0; port < nf.n; port++ {
			if port != p {
				out = append(out, port)
			}
		}
		return out
	}

	if d.IsMulticast() {
		nf.ctr.Floods++
		nf.logf("multicast dst %v: flood", d)
		return flood(), nil
	}
	idx := nf.find(fdb.Key{VLAN: v, MAC: d})
	if idx < 0 {
		nf.ctr.Floods++
		nf.logf("unknown dst (%d,%v): flood", v, d)
		return flood(), nil
	}
	if nf.ents[idx].port == p {
		nf.ctr.Filtered++
		nf.logf("dst (%d,%v) on ingress port %d: filter", v, d, p)
		return []int{}, nil
	}
	nf.logf("dst (%d,%v) known on port %d: unicast", v, d, nf.ents[idx].port)
	return []int{nf.ents[idx].port}, nil
}

func (nf *naiveFDB) addStatic(v uint16, mac fdb.MAC, port int, t int64) error {
	nf.log = nf.log[:0]
	if port < 0 || port >= nf.n {
		return fdb.ErrPortOutOfRange
	}
	if v < 1 || v > 4094 {
		return fdb.ErrVLANOutOfRange
	}
	if mac.IsMulticast() {
		return fdb.ErrMulticastStaticMAC
	}
	if err := nf.checkClock(t); err != nil {
		return err
	}
	nf.advance(t)

	k := fdb.Key{VLAN: v, MAC: mac}
	idx := nf.find(k)
	switch {
	case idx < 0:
		nf.ents = append(nf.ents, naiveEntry{key: k, port: port, seen: t, static: true})
		nf.logf("add static (%d,%v) port=%d", v, mac, port)
	case nf.ents[idx].static:
		nf.ents[idx].port = port
		nf.logf("update static (%d,%v) port=%d", v, mac, port)
	default:
		nf.ents[idx] = naiveEntry{key: k, port: port, seen: t, static: true}
		nf.ctr.Overridden++
		nf.logf("override dynamic (%d,%v) with static port=%d", v, mac, port)
	}
	return nil
}

func (nf *naiveFDB) flushPort(port int, t int64) error {
	nf.log = nf.log[:0]
	if port < 0 || port >= nf.n {
		return fdb.ErrPortOutOfRange
	}
	if err := nf.checkClock(t); err != nil {
		return err
	}
	nf.advance(t)

	kept := nf.ents[:0]
	for _, e := range nf.ents {
		if !e.static && e.port == port {
			nf.ctr.Flushed++
			nf.logf("flush (%d,%v) on port %d", e.key.VLAN, e.key.MAC, port)
			continue
		}
		kept = append(kept, e)
	}
	nf.ents = kept
	return nil
}

func (nf *naiveFDB) lookup(v uint16, mac fdb.MAC, t int64) (int, bool) {
	idx := nf.find(fdb.Key{VLAN: v, MAC: mac})
	if idx < 0 {
		return 0, false
	}
	e := nf.ents[idx]
	if !e.static && t-e.seen >= nf.aging {
		return 0, false
	}
	return e.port, true
}

// ---------------------------------------------------------------------------
// differential test against the naive oracle + conservation invariant
// ---------------------------------------------------------------------------

func TestDifferentialRandom(t *testing.T) {
	const (
		n        = 4
		aging    = 100
		capacity = 3
		steps    = 3000
	)
	rng := rand.New(rand.NewSource(20261002))
	sw := mustNew(t, n, aging, capacity)
	nf := newNaive(n, aging, capacity)

	unicastPool := make([]fdb.MAC, 0, 24)
	for b := byte(1); b <= 24; b++ {
		unicastPool = append(unicastPool, umac(b))
	}
	mcastPool := []fdb.MAC{mcastMAC(0x01), broadcast}
	anyMAC := func() fdb.MAC {
		if rng.Intn(4) == 0 {
			return mcastPool[rng.Intn(len(mcastPool))]
		}
		return unicastPool[rng.Intn(len(unicastPool))]
	}
	anyPort := func() int {
		if rng.Intn(20) == 0 {
			return []int{-1, n, n + 3}[rng.Intn(3)] // occasionally invalid
		}
		return rng.Intn(n)
	}
	anyVLAN := func() uint16 {
		if rng.Intn(20) == 0 {
			return []uint16{0, 4095, 5000}[rng.Intn(3)] // occasionally invalid
		}
		return uint16(1 + rng.Intn(3))
	}

	now := int64(0)
	nextTime := func() int64 {
		if rng.Intn(25) == 0 && now > 0 {
			return now - 1 - rng.Int63n(5) // occasionally backwards
		}
		now += rng.Int63n(aging / 2)
		return now
	}

	checkState := func(step int) {
		t.Helper()
		if got, want := sw.Len(), nf.dynLen(); got != want {
			t.Fatalf("step %d: Len: got %d, naive %d", step, got, want)
		}
		if got, want := sw.Counters(), nf.ctr; got != want {
			t.Fatalf("step %d: counters: got %+v, naive %+v", step, got, want)
		}
		checkInvariant(t, sw)
		c := sw.Counters()
		t.Logf("step %d state: Len=%d counters=%+v invariant ok", step, sw.Len(), c)
	}

	for i := 0; i < steps; i++ {
		switch rng.Intn(10) {
		case 0, 1, 2, 3, 4, 5, 6: // Frame
			p, s, d, v, ts := anyPort(), anyMAC(), anyMAC(), anyVLAN(), nextTime()
			gotOut, gotErr := sw.Frame(p, s, d, v, ts)
			wantOut, wantErr := nf.frame(p, s, d, v, ts)
			t.Logf("step %d Frame(p=%d s=%v d=%v v=%d t=%d) -> out=%v err=%v | naive out=%v err=%v | decisions=%v",
				i, p, s, d, v, ts, gotOut, gotErr, wantOut, wantErr, nf.log)
			if (gotErr == nil) != (wantErr == nil) || (gotErr != nil && !errors.Is(gotErr, wantErr)) {
				t.Fatalf("step %d: err mismatch: got %v, naive %v", i, gotErr, wantErr)
			}
			if !reflect.DeepEqual(gotOut, wantOut) {
				t.Fatalf("step %d: out mismatch: got %v, naive %v", i, gotOut, wantOut)
			}
		case 7: // AddStatic
			v, mac, p, ts := anyVLAN(), anyMAC(), anyPort(), nextTime()
			gotErr := sw.AddStatic(v, mac, p, ts)
			wantErr := nf.addStatic(v, mac, p, ts)
			t.Logf("step %d AddStatic(v=%d mac=%v port=%d t=%d) -> err=%v | naive err=%v | decisions=%v",
				i, v, mac, p, ts, gotErr, wantErr, nf.log)
			if (gotErr == nil) != (wantErr == nil) || (gotErr != nil && !errors.Is(gotErr, wantErr)) {
				t.Fatalf("step %d: err mismatch: got %v, naive %v", i, gotErr, wantErr)
			}
		case 8: // FlushPort
			p, ts := anyPort(), nextTime()
			gotErr := sw.FlushPort(p, ts)
			wantErr := nf.flushPort(p, ts)
			t.Logf("step %d FlushPort(port=%d t=%d) -> err=%v | naive err=%v | decisions=%v",
				i, p, ts, gotErr, wantErr, nf.log)
			if (gotErr == nil) != (wantErr == nil) || (gotErr != nil && !errors.Is(gotErr, wantErr)) {
				t.Fatalf("step %d: err mismatch: got %v, naive %v", i, gotErr, wantErr)
			}
		case 9: // Lookup (read-only; never changes state)
			v, mac := anyVLAN(), anyMAC()
			ts := now // never move the clock for lookups
			gotP, gotOK := sw.Lookup(v, mac, ts)
			wantP, wantOK := nf.lookup(v, mac, ts)
			t.Logf("step %d Lookup(v=%d mac=%v t=%d) -> (%d,%v) | naive (%d,%v)",
				i, v, mac, ts, gotP, gotOK, wantP, wantOK)
			if gotOK != wantOK || (gotOK && gotP != wantP) {
				t.Fatalf("step %d: lookup mismatch: got (%d,%v), naive (%d,%v)",
					i, gotP, gotOK, wantP, wantOK)
			}
		}
		checkState(i)
	}
	if got := sw.Counters().Evictions; got == 0 {
		t.Fatalf("differential run never exercised eviction path")
	}
}

// Concurrent calls must be race-free and keep the conservation invariant.
func TestConcurrent(t *testing.T) {
	sw := mustNew(t, 8, 1000, 16)
	var clock atomic.Int64
	var wg sync.WaitGroup
	for g := 0; g < 8; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			rng := rand.New(rand.NewSource(int64(g) + 1))
			for i := 0; i < 500; i++ {
				ts := clock.Add(1)
				s := umac(byte(rng.Intn(32)))
				d := umac(byte(rng.Intn(32)))
				v := uint16(1 + rng.Intn(4))
				switch rng.Intn(6) {
				case 0:
					_ = sw.AddStatic(v, s, rng.Intn(8), ts)
				case 1:
					_ = sw.FlushPort(rng.Intn(8), ts)
				case 2:
					_, _ = sw.Lookup(v, s, ts)
				default:
					_, _ = sw.Frame(rng.Intn(8), s, d, v, ts)
				}
			}
		}(g)
	}
	wg.Wait()
	checkInvariant(t, sw)
	t.Logf("final: Len=%d counters=%+v", sw.Len(), sw.Counters())
}
