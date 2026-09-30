package conntrack

import (
	"errors"
	"fmt"
	"net/netip"
	"strings"
	"sync"
	"testing"
)

func mustAddr(t *testing.T, s string) netip.Addr {
	t.Helper()
	return netip.MustParseAddr(s)
}

func mkep(t *testing.T, ip string, port uint16) Endpoint {
	t.Helper()
	return Endpoint{Addr: mustAddr(t, ip), Port: port}
}

// logRecorder captures decision lines and prints them with the test so that
// input, output and the decision basis are visible in -v output.
type logRecorder struct {
	t *testing.T
	b strings.Builder
}

func (l *logRecorder) Printf(format string, args ...any) {
	line := fmt.Sprintf(format, args...)
	l.b.WriteString(line)
	l.b.WriteByte('\n')
	l.t.Log(line)
}

func newTestTable(t *testing.T, lo, hi uint16, n int, th, te, tc int64) *Table {
	t.Helper()
	tbl, err := New(lo, hi, n, th, te, tc, WithLogger(&logRecorder{t: t}))
	if err != nil {
		t.Fatalf("New: unexpected error: %v", err)
	}
	return tbl
}

func outPkt(now int64, internal, remote Endpoint, ev EventType) Packet {
	return Packet{Now: now, Internal: internal, Remote: remote, Event: ev}
}

func inPkt(now int64, remote Endpoint, ev EventType) Packet {
	return Packet{Now: now, Remote: remote, Event: ev}
}

// The same internal endpoint talking to different remotes shares one external
// port, and a different internal endpoint gets the next smallest free port.
func TestSharedExternalPortAndSmallestFree(t *testing.T) {
	tbl := newTestTable(t, 40000, 40002, 10, 10, 20, 5)
	int1 := mkep(t, "10.0.0.1", 1000)
	int2 := mkep(t, "10.0.0.2", 2000)
	rem1 := mkep(t, "203.0.11.1", 80)
	rem2 := mkep(t, "198.51.100.7", 443)

	r1, err := tbl.HandleOutbound(outPkt(1, int1, rem1, EventSYN))
	if err != nil || !r1.Allowed || r1.ExternalPort != 40000 || r1.State != StateHalfOpen || r1.Expiry != 11 {
		t.Fatalf("conn1: %+v %v", r1, err)
	}
	r2, err := tbl.HandleOutbound(outPkt(2, int1, rem2, EventSYN))
	if err != nil || r2.ExternalPort != 40000 {
		t.Fatalf("same endpoint must reuse port 40000, got %+v %v", r2, err)
	}
	r3, err := tbl.HandleOutbound(outPkt(3, int2, rem1, EventSYN))
	if err != nil || r3.ExternalPort != 40001 {
		t.Fatalf("new endpoint must get smallest free 40001, got %+v %v", r3, err)
	}
	if live := tbl.Snapshot(3); len(live) != 3 {
		t.Fatalf("want 3 live connections, got %d", len(live))
	}

	// One connection of int1 dies on RST: the port stays bound because a
	// second connection of the same endpoint still exists.
	if _, err := tbl.HandleOutbound(outPkt(4, int1, rem1, EventRST)); err != nil {
		t.Fatalf("RST: %v", err)
	}
	if live := tbl.Snapshot(4); len(live) != 2 {
		t.Fatalf("want 2 live connections after RST, got %d", len(live))
	}

	// The last connection of int1 dies: 40000 is released and a new endpoint
	// gets 40000 back (reuse after release), not 40002.
	if _, err := tbl.HandleOutbound(outPkt(5, int1, rem2, EventRST)); err != nil {
		t.Fatalf("RST: %v", err)
	}
	int3 := mkep(t, "10.0.0.3", 3000)
	r4, err := tbl.HandleOutbound(outPkt(6, int3, rem1, EventSYN))
	if err != nil || r4.ExternalPort != 40000 {
		t.Fatalf("released smallest port 40000 should be reused, got %+v %v", r4, err)
	}
}

// A connection expiring exactly at the current instant is already gone: an
// inbound packet on that port has no connection for the remote. One tick
// earlier it is still admitted and establishes the connection.
func TestInboundExactlyAtExpiry(t *testing.T) {
	tbl := newTestTable(t, 50000, 50000, 10, 10, 20, 5)
	internal := mkep(t, "10.0.0.1", 1000)
	remote := mkep(t, "203.0.11.1", 80)
	sibling := mkep(t, "198.51.100.7", 443)

	r, err := tbl.HandleOutbound(outPkt(0, internal, remote, EventSYN))
	if err != nil || r.Expiry != 10 {
		t.Fatalf("setup: %+v %v", r, err)
	}
	// Sibling keeps the endpoint (and its port mapping) alive while the target
	// connection expires, so the inbound rejection is "no connection for
	// remote" rather than "no port mapping".
	if _, err := tbl.HandleOutbound(outPkt(1, internal, sibling, EventSYN)); err != nil {
		t.Fatalf("sibling: %v", err)
	}
	// now == expiry: already invalid, so the inbound lookup fails.
	if _, err := tbl.HandleInbound(inPkt(10, remote, EventSYN), 50000); !errors.Is(err, ErrNoConnectionForRemote) {
		t.Fatalf("inbound at exact expiry: want ErrNoConnectionForRemote, got %v", err)
	}
	// An unowned port is rejected as "no mapping".
	if _, err := tbl.HandleInbound(inPkt(10, sibling, EventDATA), 50001); !errors.Is(err, ErrNoMapping) {
		t.Fatalf("unowned port: want ErrNoMapping, got %v", err)
	}
	// Recreate the connection at the expiry instant; one tick before its new
	// expiry the inbound SYN is admitted and establishes it.
	if _, err := tbl.HandleOutbound(outPkt(10, internal, remote, EventSYN)); err != nil {
		t.Fatalf("recreate: %v", err)
	}
	// Recreate the sibling at t=10 as well.
	if _, err := tbl.HandleOutbound(outPkt(10, internal, sibling, EventSYN)); err != nil {
		t.Fatalf("recreate sibling: %v", err)
	}
	r3, err := tbl.HandleInbound(inPkt(19, remote, EventSYN), 50000)
	if err != nil || !r3.Allowed || r3.State != StateEstablished || r3.Expiry != 39 {
		t.Fatalf("inbound at expiry-1: %+v %v", r3, err)
	}
	// Keep the sibling half-open past the target's established expiry so the
	// endpoint mapping survives while the target connection dies.
	if r, err := tbl.HandleOutbound(outPkt(30, internal, sibling, EventSYN)); err != nil || r.Expiry != 40 {
		t.Fatalf("recreate sibling at t=30: %+v %v", r, err)
	}
	// At exactly the established expiry it is dead again while the sibling
	// connection keeps the port mapping present.
	if _, err := tbl.HandleInbound(inPkt(39, remote, EventDATA), 50000); !errors.Is(err, ErrNoConnectionForRemote) {
		t.Fatalf("inbound at established expiry: want ErrNoConnectionForRemote, got %v", err)
	}
}

// After FIN the connection is closed; subsequent admitted packets never extend
// the closed expiry, and at expiry the connection is gone.
func TestClosedPacketsDoNotRefresh(t *testing.T) {
	tbl := newTestTable(t, 60000, 60000, 10, 100, 200, 5)
	internal := mkep(t, "10.0.0.1", 1000)
	remote := mkep(t, "203.0.11.1", 80)

	if r, err := tbl.HandleOutbound(outPkt(0, internal, remote, EventSYN)); err != nil || !r.Allowed {
		t.Fatalf("setup: %+v %v", r, err)
	}
	if r, err := tbl.HandleInbound(inPkt(1, remote, EventDATA), 60000); err != nil ||
		r.State != StateEstablished || r.Expiry != 201 {
		t.Fatalf("establish: %+v %v", r, err)
	}
	r, err := tbl.HandleOutbound(outPkt(10, internal, remote, EventFIN))
	if err != nil || r.State != StateClosed || r.Expiry != 15 {
		t.Fatalf("fin: %+v %v", r, err)
	}

	r2, err := tbl.HandleInbound(inPkt(11, remote, EventDATA), 60000)
	if err != nil || !r2.Allowed || r2.State != StateClosed || r2.Expiry != 15 {
		t.Fatalf("closed DATA must not refresh expiry, got %+v %v", r2, err)
	}
	r3, err := tbl.HandleOutbound(outPkt(12, internal, remote, EventFIN))
	if err != nil || r3.Expiry != 15 {
		t.Fatalf("second FIN must not change expiry, got %+v %v", r3, err)
	}
	r4, err := tbl.HandleOutbound(outPkt(13, internal, remote, EventDATA))
	if err != nil || r4.Expiry != 15 {
		t.Fatalf("closed outbound DATA must not refresh expiry, got %+v %v", r4, err)
	}

	if _, err := tbl.HandleInbound(inPkt(15, remote, EventDATA), 60000); !errors.Is(err, ErrNoMapping) {
		t.Fatalf("at closed expiry the mapping must be gone, got %v", err)
	}
}

// Table-full and port-pool-exhausted rejections happen in the specified order
// and leave the table unchanged.
func TestTableFullAndPortPoolExhausted(t *testing.T) {
	tbl := newTestTable(t, 40000, 40001, 3, 100, 200, 50)
	rem := mkep(t, "203.0.11.1", 80)
	int1 := mkep(t, "10.0.0.1", 1000)
	int2 := mkep(t, "10.0.0.2", 2000)

	if _, err := tbl.HandleOutbound(outPkt(1, int1, rem, EventSYN)); err != nil {
		t.Fatal(err)
	}
	// Same endpoint, different remote: shares int1's port, second connection.
	rem2 := mkep(t, "198.51.100.7", 443)
	if _, err := tbl.HandleOutbound(outPkt(2, int1, rem2, EventSYN)); err != nil {
		t.Fatal(err)
	}
	// N=3 with two live connections: temporarily block one conn slot via a
	// third endpoint, so a new connection hits the table-full limit even
	// though a free port still remains.
	int3 := mkep(t, "10.0.0.3", 3000)
	if _, err := tbl.HandleOutbound(outPkt(3, int3, rem, EventSYN)); err != nil {
		t.Fatalf("fill third slot: %v", err)
	}
	int4 := mkep(t, "10.0.0.4", 4000)
	if _, err := tbl.HandleOutbound(outPkt(4, int4, rem, EventSYN)); !errors.Is(err, ErrTableFull) {
		t.Fatalf("want ErrTableFull, got %v", err)
	}

	// Remove the third-endpoint connection to free a slot again, then empty the
	// table and exhaust the two-port pool with two endpoints.
	if _, err := tbl.HandleOutbound(outPkt(5, int3, rem, EventRST)); err != nil {
		t.Fatal(err)
	}
	if _, err := tbl.HandleOutbound(outPkt(6, int1, rem, EventRST)); err != nil {
		t.Fatal(err)
	}
	if _, err := tbl.HandleOutbound(outPkt(7, int1, rem2, EventRST)); err != nil {
		t.Fatal(err)
	}
	if _, err := tbl.HandleOutbound(outPkt(8, int1, rem, EventSYN)); err != nil {
		t.Fatal(err)
	}
	if _, err := tbl.HandleOutbound(outPkt(9, int2, rem, EventSYN)); err != nil {
		t.Fatal(err)
	}
	if _, err := tbl.HandleOutbound(outPkt(10, int3, rem, EventSYN)); !errors.Is(err, ErrPortPoolExhausted) {
		t.Fatalf("want ErrPortPoolExhausted, got %v", err)
	}
	if live := tbl.Snapshot(10); len(live) != 2 {
		t.Fatalf("rejected SYN must not create a connection, live=%d", len(live))
	}
}

// Outbound rejection reasons follow the mandated first-error order, and
// rejected operations mutate nothing.
func TestOutboundRejectionOrderAndNoMutation(t *testing.T) {
	tbl := newTestTable(t, 40000, 40000, 1, 5, 10, 3)
	internal := mkep(t, "10.0.0.1", 1000)
	remote := mkep(t, "203.0.11.1", 80)

	if _, err := tbl.HandleOutbound(outPkt(10, internal, remote, EventSYN)); err != nil {
		t.Fatal(err)
	}

	// 1. clock skew beats every other reason.
	other9 := mkep(t, "10.0.0.9", 9999)
	if _, err := tbl.HandleOutbound(outPkt(9, other9, remote, EventDATA)); !errors.Is(err, ErrClockSkew) {
		t.Fatalf("want ErrClockSkew, got %v", err)
	}
	// 2. no connection and not SYN.
	other := mkep(t, "10.0.0.2", 2000)
	if _, err := tbl.HandleOutbound(outPkt(10, other, remote, EventDATA)); !errors.Is(err, ErrNotSynNoConn) {
		t.Fatalf("want ErrNotSynNoConn, got %v", err)
	}
	// 3. table full when a new connection is needed.
	if _, err := tbl.HandleOutbound(outPkt(10, other, remote, EventSYN)); !errors.Is(err, ErrTableFull) {
		t.Fatalf("want ErrTableFull, got %v", err)
	}

	// Port-pool-empty: N large enough, pool of one port already taken.
	tbl2 := newTestTable(t, 40000, 40000, 100, 5, 10, 3)
	if _, err := tbl2.HandleOutbound(outPkt(1, internal, remote, EventSYN)); err != nil {
		t.Fatal(err)
	}
	if _, err := tbl2.HandleOutbound(outPkt(2, other, remote, EventSYN)); !errors.Is(err, ErrPortPoolExhausted) {
		t.Fatalf("want ErrPortPoolExhausted, got %v", err)
	}

	// Nothing changed by the rejected calls.
	if live := tbl.Snapshot(10); len(live) != 1 || live[0].Expiry != 15 {
		t.Fatalf("rejected operations mutated state: %+v", live)
	}
}

// Inbound rejections: clock skew, then no port mapping, then no matching
// connection for the remote.
func TestInboundRejections(t *testing.T) {
	tbl := newTestTable(t, 40000, 40000, 10, 5, 10, 3)
	internal := mkep(t, "10.0.0.1", 1000)
	remote := mkep(t, "203.0.11.1", 80)
	if _, err := tbl.HandleOutbound(outPkt(5, internal, remote, EventSYN)); err != nil {
		t.Fatal(err)
	}

	if _, err := tbl.HandleInbound(inPkt(4, remote, EventDATA), 40000); !errors.Is(err, ErrClockSkew) {
		t.Fatalf("want ErrClockSkew, got %v", err)
	}
	if _, err := tbl.HandleInbound(inPkt(6, remote, EventDATA), 40001); !errors.Is(err, ErrNoMapping) {
		t.Fatalf("want ErrNoMapping, got %v", err)
	}
	wrongRemote := mkep(t, "198.51.100.9", 22)
	if _, err := tbl.HandleInbound(inPkt(6, wrongRemote, EventDATA), 40000); !errors.Is(err, ErrNoConnectionForRemote) {
		t.Fatalf("want ErrNoConnectionForRemote, got %v", err)
	}
	// The valid connection is untouched and still admits its own remote.
	r, err := tbl.HandleInbound(inPkt(6, remote, EventSYN), 40000)
	if err != nil || !r.Allowed || r.State != StateEstablished || r.Expiry != 16 {
		t.Fatalf("valid inbound: %+v %v", r, err)
	}
}

// Constructor rejects an empty/inverted range, non-positive N and timeouts.
func TestInvalidConfig(t *testing.T) {
	cases := []struct {
		name string
		lo   uint16
		hi   uint16
		n    int
		th   int64
		te   int64
		tc   int64
	}{
		{"lo>hi", 40002, 40000, 10, 1, 1, 1},
		{"n zero", 40000, 40000, 0, 1, 1, 1},
		{"n negative", 40000, 40000, -1, 1, 1, 1},
		{"th zero", 40000, 40000, 10, 0, 1, 1},
		{"te negative", 40000, 40000, 10, 1, -2, 1},
		{"tc zero", 40000, 40000, 10, 1, 1, 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := New(tc.lo, tc.hi, tc.n, tc.th, tc.te, tc.tc); !errors.Is(err, ErrInvalidConfig) {
				t.Fatalf("want ErrInvalidConfig, got %v", err)
			}
		})
	}
}

// Half-open outbound packets extend the half-open timeout; inbound packets
// establish. RST removes the connection and releases the port.
func TestStateTransitionsAndRST(t *testing.T) {
	tbl := newTestTable(t, 40000, 40000, 10, 10, 20, 5)
	internal := mkep(t, "10.0.0.1", 1000)
	remote := mkep(t, "203.0.11.1", 80)

	r, _ := tbl.HandleOutbound(outPkt(1, internal, remote, EventSYN))
	if r.State != StateHalfOpen || r.Expiry != 11 {
		t.Fatalf("new conn: %+v", r)
	}
	r, _ = tbl.HandleOutbound(outPkt(5, internal, remote, EventDATA))
	if r.State != StateHalfOpen || r.Expiry != 15 {
		t.Fatalf("half-open refresh: %+v", r)
	}
	r, _ = tbl.HandleInbound(inPkt(6, remote, EventDATA), 40000)
	if r.State != StateEstablished || r.Expiry != 26 {
		t.Fatalf("inbound establishes: %+v", r)
	}
	r, _ = tbl.HandleOutbound(outPkt(7, internal, remote, EventDATA))
	if r.State != StateEstablished || r.Expiry != 27 {
		t.Fatalf("established refresh: %+v", r)
	}
	r, err := tbl.HandleInbound(inPkt(8, remote, EventRST), 40000)
	if err != nil || !r.Allowed {
		t.Fatalf("inbound RST: %+v %v", r, err)
	}
	if live := tbl.Snapshot(8); len(live) != 0 {
		t.Fatalf("RST must delete the connection, live=%d", len(live))
	}
	if _, err := tbl.HandleInbound(inPkt(9, remote, EventDATA), 40000); !errors.Is(err, ErrNoMapping) {
		t.Fatalf("port must be released after RST, got %v", err)
	}
}

// Concurrent use must keep the invariants: <= N connections and each external
// port bound to at most one internal endpoint. Run under -race.
func TestConcurrentInvariants(t *testing.T) {
	const goroutines = 16
	const connsPer = 12
	tbl := newTestTable(t, 40000, 40100, goroutines*connsPer, 1000, 2000, 500)

	var wg sync.WaitGroup
	var synsDone sync.WaitGroup
	synsDone.Add(goroutines)
	var startPhase2 sync.WaitGroup
	startPhase2.Add(1)
	for g := 0; g < goroutines; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			// Each goroutine owns one internal endpoint. All SYNs are emitted at
			// time 1; after a barrier all follow-ups run at time 2, so the table
			// clock is monotonic under every interleaving.
			internal := mkep(t, "10.0.0.1", uint16(1000+g))
			ports := make([]uint16, connsPer)
			for i := 0; i < connsPer; i++ {
				remote := mkep(t, "203.0.113.1", uint16(1+i))
				r, err := tbl.HandleOutbound(outPkt(1, internal, remote, EventSYN))
				if err != nil || !r.Allowed {
					t.Errorf("out g=%d i=%d: %v %v", g, i, r, err)
					ports[i] = 0
					continue
				}
				ports[i] = r.ExternalPort
			}
			synsDone.Done()
			startPhase2.Wait()
			for i := 0; i < connsPer; i++ {
				if ports[i] == 0 {
					continue
				}
				remote := mkep(t, "203.0.113.1", uint16(1+i))
				if _, err := tbl.HandleInbound(inPkt(2, remote, EventDATA), ports[i]); err != nil {
					t.Errorf("in g=%d i=%d: %v", g, i, err)
					continue
				}
				// Every connection of this endpoint must share the port.
				if got, err := tbl.HandleOutbound(outPkt(2, internal, remote, EventDATA)); err != nil || got.ExternalPort != ports[i] {
					t.Errorf("shared port g=%d i=%d: got %d want %d (%v)", g, i, got.ExternalPort, ports[i], err)
				}
			}
		}(g)
	}
	synsDone.Wait()
	startPhase2.Done()
	wg.Wait()

	live := tbl.Snapshot(2)
	if len(live) > goroutines*connsPer {
		t.Fatalf("connection cap exceeded: %d", len(live))
	}
	owners := map[uint16]Endpoint{}
	for _, c := range live {
		if owner, dup := owners[c.ExternalPort]; dup && owner != c.Internal {
			t.Fatalf("external port %d owned by both %s and %s", c.ExternalPort, owner, c.Internal)
		}
		owners[c.ExternalPort] = c.Internal
	}
	if len(live) != goroutines*connsPer {
		t.Fatalf("want %d live connections, got %d", goroutines*connsPer, len(live))
	}
}

// Replaying the same timestamped event sequence yields identical results and
// snapshots.
func TestDeterministicReplay(t *testing.T) {
	play := func() []ConnInfo {
		tbl := newTestTable(t, 40000, 40003, 10, 8, 16, 4)
		type step struct {
			p    Packet
			port uint16
			in   bool
		}
		int1 := mkep(t, "10.0.0.1", 1000)
		int2 := mkep(t, "10.0.0.2", 2000)
		rem1 := mkep(t, "203.0.11.1", 80)
		rem2 := mkep(t, "198.51.100.7", 443)
		steps := []step{
			{outPkt(1, int1, rem1, EventSYN), 0, false},
			{outPkt(2, int1, rem2, EventSYN), 0, false},
			{outPkt(3, int2, rem1, EventSYN), 0, false},
			{inPkt(4, rem1, EventDATA), 40000, true},
			{outPkt(5, int1, rem2, EventFIN), 0, false},
			{inPkt(6, rem1, EventDATA), 40001, true},
			{outPkt(7, int2, rem1, EventRST), 0, false},
		}
		for _, s := range steps {
			if s.in {
				if _, err := tbl.HandleInbound(s.p, s.port); err != nil {
					t.Fatalf("replay inbound: %v", err)
				}
			} else {
				if _, err := tbl.HandleOutbound(s.p); err != nil {
					t.Fatalf("replay outbound: %v", err)
				}
			}
		}
		return tbl.Snapshot(7)
	}

	first := play()
	second := play()
	if len(first) != len(second) {
		t.Fatalf("replay length differs: %d vs %d", len(first), len(second))
	}
	for i := range first {
		if first[i] != second[i] {
			t.Fatalf("replay differs at %d:\n%+v\n%+v", i, first[i], second[i])
		}
	}
}

// The logger records input, output and the decision basis for each event.
func TestLogging(t *testing.T) {
	rec := &logRecorder{t: t}
	tbl, err := New(40000, 40000, 1, 5, 10, 3, WithLogger(rec))
	if err != nil {
		t.Fatal(err)
	}
	internal := mkep(t, "10.0.0.1", 1000)
	remote := mkep(t, "203.0.11.1", 80)
	if _, err := tbl.HandleOutbound(outPkt(1, internal, remote, EventSYN)); err != nil {
		t.Fatal(err)
	}
	if _, err := tbl.HandleInbound(inPkt(2, remote, EventDATA), 40000); err != nil {
		t.Fatal(err)
	}
	if _, err := tbl.HandleOutbound(outPkt(3, internal, remote, EventFIN)); err != nil {
		t.Fatal(err)
	}
	logs := rec.b.String()
	for _, want := range []string{"INPUT dir=outbound", "INPUT dir=inbound", "CREATE", "ALLOCATE",
		"half-open->established", "->closed", "OUTPUT allowed=true"} {
		if !strings.Contains(logs, want) {
			t.Errorf("log missing %q\n%s", want, logs)
		}
	}
	t.Logf("captured decision log:\n%s", logs)
}
