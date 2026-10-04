package h2pool

import (
	"errors"
	"fmt"
	"math/rand"
	"reflect"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
)

// naivePool is a deliberately plain, step-by-step transcription of the
// specification. The randomized test replays identical call sequences
// against Pool and naivePool and requires identical results, retry lists
// and state transitions.

type naiveConn struct {
	state      State
	maxConc    int
	nextID     int64
	streams    map[int64]string
	goAwayLast int64
	idleSince  int64
	refuse     int
}

type naivePool struct {
	m0       int
	idle     int64
	maxID    int64
	k        int
	conns    map[string]*naiveConn
	inflight map[string]bool
	last     int64
	set      bool
}

func newNaive(m0 int, idle, maxID int64, k int) *naivePool {
	return &naivePool{
		m0: m0, idle: idle, maxID: maxID, k: k,
		conns: make(map[string]*naiveConn), inflight: make(map[string]bool),
	}
}

func (n *naivePool) clockOK(now int64) error {
	if n.set && now < n.last {
		return ErrClockBackwards
	}
	return nil
}

func (n *naivePool) commit(now int64) { n.last, n.set = now, true }

func (n *naivePool) addConn(now int64, id string) error {
	if _, dup := n.conns[id]; dup {
		return ErrDuplicateConn
	}
	if err := n.clockOK(now); err != nil {
		return err
	}
	n.commit(now)
	n.conns[id] = &naiveConn{
		state: Active, maxConc: n.m0, nextID: 1,
		streams: make(map[int64]string), goAwayLast: n.maxID, idleSince: now,
	}
	return nil
}

func (n *naivePool) open(now int64, req string) (string, int64, error) {
	if n.inflight[req] {
		return "", 0, ErrDuplicateReq
	}
	if err := n.clockOK(now); err != nil {
		return "", 0, err
	}
	// Collect candidates, then scan for the minimum by (active, id).
	best := ""
	bestActive := -1
	ids := make([]string, 0, len(n.conns))
	for id := range n.conns {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	for _, id := range ids {
		c := n.conns[id]
		if c.state != Active || len(c.streams) >= c.maxConc {
			continue
		}
		if best == "" || len(c.streams) < bestActive {
			best, bestActive = id, len(c.streams)
		}
	}
	if best == "" {
		return "", 0, ErrNoCapacity
	}
	n.commit(now)
	c := n.conns[best]
	stream := c.nextID
	c.nextID += 2
	if c.nextID > n.maxID {
		c.state = Draining
	}
	c.streams[stream] = req
	n.inflight[req] = true
	return best, stream, nil
}

func (n *naivePool) setMaxConcurrent(now int64, id string, m int) error {
	c, ok := n.conns[id]
	if !ok {
		return ErrUnknownConn
	}
	if m < 1 || m > 1000 {
		return ErrInvalidMaxConc
	}
	if err := n.clockOK(now); err != nil {
		return err
	}
	if c.state == Closed {
		return ErrConnClosed
	}
	n.commit(now)
	c.maxConc = m
	return nil
}

func (n *naivePool) closeStream(now int64, id string, stream int64, kind StreamEndKind) (bool, error) {
	c, ok := n.conns[id]
	if !ok {
		return false, ErrUnknownConn
	}
	req, ok := c.streams[stream]
	if !ok {
		return false, ErrUnknownStream
	}
	if kind != Done && kind != Refused && kind != Reset {
		return false, ErrInvalidKind
	}
	if err := n.clockOK(now); err != nil {
		return false, err
	}
	n.commit(now)
	delete(c.streams, stream)
	delete(n.inflight, req)
	retryable := false
	switch kind {
	case Done:
		c.refuse = 0
	case Refused:
		c.refuse++
		retryable = true
		if c.refuse >= n.k && c.state == Active {
			c.state = Draining
		}
	}
	if len(c.streams) == 0 {
		if c.state == Draining {
			c.state = Closed
		} else if c.state == Active {
			c.idleSince = now
		}
	}
	return retryable, nil
}

func (n *naivePool) goAway(now int64, id string, lastID int64) ([]string, error) {
	c, ok := n.conns[id]
	if !ok {
		return nil, ErrUnknownConn
	}
	if lastID != 0 && (lastID < 0 || lastID > n.maxID || lastID%2 == 0) {
		return nil, ErrInvalidLastID
	}
	if err := n.clockOK(now); err != nil {
		return nil, err
	}
	if c.state == Closed {
		return nil, ErrConnClosed
	}
	if lastID > c.goAwayLast {
		return nil, ErrGoAwayUp
	}
	lastAssigned := c.nextID - 2
	if lastAssigned < 0 {
		lastAssigned = 0
	}
	if lastID > lastAssigned && lastID != n.maxID {
		return nil, ErrGoAwayBeyond
	}
	n.commit(now)
	c.goAwayLast = lastID
	if c.state == Active {
		c.state = Draining
	}
	retry := []string{}
	// Scan stream ids ascending; drop those above lastID.
	streams := make([]int64, 0, len(c.streams))
	for s := range c.streams {
		streams = append(streams, s)
	}
	sort.Slice(streams, func(i, j int) bool { return streams[i] < streams[j] })
	for _, s := range streams {
		if s > lastID {
			retry = append(retry, c.streams[s])
			delete(n.inflight, c.streams[s])
			delete(c.streams, s)
		}
	}
	if len(c.streams) == 0 {
		c.state = Closed
	}
	return retry, nil
}

func (n *naivePool) tick(now int64) ([]string, error) {
	if err := n.clockOK(now); err != nil {
		return nil, err
	}
	n.commit(now)
	var closed []string
	ids := make([]string, 0, len(n.conns))
	for id := range n.conns {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	for _, id := range ids {
		c := n.conns[id]
		if c.state == Active && len(c.streams) == 0 && now-c.idleSince >= n.idle {
			c.state = Closed
			closed = append(closed, id)
		}
	}
	return closed, nil
}

// ---- randomized differential test -------------------------------------

func errName(err error) string {
	if err == nil {
		return "nil"
	}
	for _, e := range []error{
		ErrUnknownConn, ErrUnknownStream, ErrInvalidLastID, ErrInvalidMaxConc,
		ErrInvalidKind, ErrDuplicateReq, ErrDuplicateConn, ErrInvalidParam,
		ErrClockBackwards, ErrConnClosed, ErrGoAwayUp, ErrGoAwayBeyond, ErrNoCapacity,
	} {
		if errors.Is(err, e) {
			return e.Error()
		}
	}
	return "?" + err.Error()
}

func sameErr(got, want error) bool {
	if want == nil {
		return got == nil
	}
	return errors.Is(got, want)
}

func stateString(p *Pool) string {
	var b strings.Builder
	for _, id := range p.sortedIDs() {
		c := p.conns[id]
		streams := make([]int64, 0, len(c.streams))
		for s := range c.streams {
			streams = append(streams, s)
		}
		sort.Slice(streams, func(i, j int) bool { return streams[i] < streams[j] })
		fmt.Fprintf(&b, " %s{%s cap=%d next=%d ga=%d idle@%d refuse=%d s=%v}",
			id, c.state, c.maxConc, c.nextID, c.goAwayLast, c.idleSince, c.refuse, streams)
	}
	return b.String()
}

func compareStates(t *testing.T, p *Pool, n *naivePool, ctx string) {
	t.Helper()
	if len(p.conns) != len(n.conns) {
		t.Fatalf("%s: conn count %d != naive %d", ctx, len(p.conns), len(n.conns))
	}
	for id, c := range p.conns {
		nc, ok := n.conns[id]
		if !ok {
			t.Fatalf("%s: conn %q missing in naive model", ctx, id)
		}
		if c.state != nc.state || c.maxConc != nc.maxConc || c.nextID != nc.nextID ||
			c.goAwayLast != nc.goAwayLast || c.idleSince != nc.idleSince || c.refuse != nc.refuse ||
			!reflect.DeepEqual(c.streams, nc.streams) {
			t.Fatalf("%s: conn %q diverged:\n pool=%+v %v\nnaive=%+v %v",
				ctx, id, c, c.streams, nc, nc.streams)
		}
	}
	if !reflect.DeepEqual(p.lastNow, n.last) || p.clockSet != n.set {
		t.Fatalf("%s: clock diverged: pool=(%d,%v) naive=(%d,%v)", ctx, p.lastNow, p.clockSet, n.last, n.set)
	}
	if len(p.inflight) != len(n.inflight) {
		t.Fatalf("%s: inflight count %d != naive %d", ctx, len(p.inflight), len(n.inflight))
	}
	for r := range p.inflight {
		if !n.inflight[r] {
			t.Fatalf("%s: inflight req %q missing in naive model", ctx, r)
		}
	}
}

// TestRandomAgainstNaiveModel replays 2000 random call sequences against
// both implementations and requires identical picks, retry lists and state
// transitions. Every call is logged with its input, output and the rule
// that decided it.
func TestRandomAgainstNaiveModel(t *testing.T) {
	const sequences = 2000
	rng := rand.New(rand.NewSource(20261227))
	maxIDs := []int64{1, 3, 5, 7, 9, 15}
	connIDs := []string{"c0", "c1", "c2", "c3"}
	reqIDs := []string{"r0", "r1", "r2", "r3", "r4", "r5", "r6", "r7"}

	for seq := 0; seq < sequences; seq++ {
		m0 := 1 + rng.Intn(4)
		idle := int64(1 + rng.Intn(30))
		maxID := maxIDs[rng.Intn(len(maxIDs))]
		k := 1 + rng.Intn(3)
		p, err := NewPool(m0, idle, maxID, k)
		if err != nil {
			t.Fatalf("seq %d: NewPool: %v", seq, err)
		}
		n := newNaive(m0, idle, maxID, k)
		t.Logf("seq=%d setup m0=%d idleTimeout=%d maxID=%d K=%d", seq, m0, idle, maxID, k)

		now := int64(0)
		for step := 0; step < 40; step++ {
			if rng.Intn(100) < 6 {
				now -= int64(rng.Intn(10)) // provoke clock-rollback rejections
				if now < 0 {
					now = 0
				}
			} else {
				now += int64(rng.Intn(6))
			}
			ctx := fmt.Sprintf("seq=%d step=%d now=%d", seq, step, now)

			switch op := rng.Intn(100); {
			case op < 14: // AddConn
				id := connIDs[rng.Intn(len(connIDs))]
				gerr := p.AddConn(now, id)
				werr := n.addConn(now, id)
				t.Logf("%s AddConn(%q) -> %v | %s", ctx, id, errName(gerr), addConnReason(n, id, gerr))
				if !sameErr(gerr, werr) {
					t.Fatalf("%s AddConn(%q): pool=%v naive=%v", ctx, id, gerr, werr)
				}

			case op < 44: // Open
				req := reqIDs[rng.Intn(len(reqIDs))]
				gid, gs, gerr := p.Open(now, req)
				wid, ws, werr := n.open(now, req)
				t.Logf("%s Open(%q) -> (%q,%d,%v) | %s", ctx, req, gid, gs, errName(gerr), openReason(n, req, gid, gerr))
				if !sameErr(gerr, werr) || gid != wid || gs != ws {
					t.Fatalf("%s Open(%q): pool=(%q,%d,%v) naive=(%q,%d,%v)", ctx, req, gid, gs, gerr, wid, ws, werr)
				}

			case op < 64: // CloseStream
				id := connIDs[rng.Intn(len(connIDs))]
				stream := pickStream(rng, n, id, maxID)
				kind := []StreamEndKind{Done, Done, Refused, Reset}[rng.Intn(4)]
				gretry, gerr := p.CloseStream(now, id, stream, kind)
				wretry, werr := n.closeStream(now, id, stream, kind)
				t.Logf("%s CloseStream(%q,%d,%v) -> (retry=%v,%v) | %s",
					ctx, id, stream, kind, gretry, errName(gerr), closeReason(n, id, stream, kind, gerr))
				if !sameErr(gerr, werr) || gretry != wretry {
					t.Fatalf("%s CloseStream(%q,%d,%v): pool=(%v,%v) naive=(%v,%v)",
						ctx, id, stream, kind, gretry, gerr, wretry, werr)
				}

			case op < 79: // GoAway
				id := connIDs[rng.Intn(len(connIDs))]
				lastID := pickLastID(rng, maxID)
				gretry, gerr := p.GoAway(now, id, lastID)
				wretry, werr := n.goAway(now, id, lastID)
				t.Logf("%s GoAway(%q,%d) -> (retry=%v,%v) | %s",
					ctx, id, lastID, gretry, errName(gerr), goAwayReason(n, id, lastID, gerr))
				if !sameErr(gerr, werr) || !reflect.DeepEqual(gretry, wretry) {
					t.Fatalf("%s GoAway(%q,%d): pool=(%v,%v) naive=(%v,%v)",
						ctx, id, lastID, gretry, gerr, wretry, werr)
				}

			case op < 90: // SetMaxConcurrent
				id := connIDs[rng.Intn(len(connIDs))]
				m := rng.Intn(6) // 0 is out of range on purpose
				gerr := p.SetMaxConcurrent(now, id, m)
				werr := n.setMaxConcurrent(now, id, m)
				t.Logf("%s SetMaxConcurrent(%q,%d) -> %v | %s", ctx, id, m, errName(gerr), setMaxReason(n, id, m, gerr))
				if !sameErr(gerr, werr) {
					t.Fatalf("%s SetMaxConcurrent(%q,%d): pool=%v naive=%v", ctx, id, m, gerr, werr)
				}

			default: // Tick
				gclosed, gerr := p.Tick(now)
				wclosed, werr := n.tick(now)
				t.Logf("%s Tick() -> (closed=%v,%v) | %s", ctx, gclosed, errName(gerr), tickReason(n, gclosed, gerr))
				if !sameErr(gerr, werr) || !reflect.DeepEqual(gclosed, wclosed) {
					t.Fatalf("%s Tick: pool=(%v,%v) naive=(%v,%v)", ctx, gclosed, gerr, wclosed, werr)
				}
			}
			compareStates(t, p, n, ctx)
			t.Logf("%s state:%s", ctx, stateString(p))
		}
	}
}

// pickStream chooses an existing stream of the conn 70% of the time, and a
// (likely unknown) random odd stream id otherwise.
func pickStream(rng *rand.Rand, n *naivePool, id string, maxID int64) int64 {
	if c, ok := n.conns[id]; ok && len(c.streams) > 0 && rng.Intn(100) < 70 {
		streams := make([]int64, 0, len(c.streams))
		for s := range c.streams {
			streams = append(streams, s)
		}
		sort.Slice(streams, func(i, j int) bool { return streams[i] < streams[j] })
		return streams[rng.Intn(len(streams))]
	}
	return 1 + 2*rng.Int63n((maxID+2)/2+2)
}

// pickLastID mixes valid, edge and invalid last-stream-ids.
func pickLastID(rng *rand.Rand, maxID int64) int64 {
	switch rng.Intn(10) {
	case 0, 1, 2:
		return 0
	case 3, 4:
		return maxID
	case 5, 6, 7:
		return 1 + 2*rng.Int63n((maxID+2)/2+3) // odd, possibly beyond assigned
	case 8:
		return 2 * rng.Int63n((maxID+2)/2+1) // even: invalid
	default:
		return []int64{-1, maxID + 2}[rng.Intn(2)] // out of range
	}
}

// ---- rationale strings (判定依据) --------------------------------------

func addConnReason(n *naivePool, id string, err error) string {
	switch {
	case errors.Is(err, ErrDuplicateConn):
		return "id already used (ids of Closed conns stay taken)"
	case errors.Is(err, ErrClockBackwards):
		return "now earlier than last accepted call; rejected without side effects"
	default:
		return "accepted: new Active conn, maxConc=m0, nextID=1, goAwayLast=maxID, idleSince=now"
	}
}

func openReason(n *naivePool, req, picked string, err error) string {
	switch {
	case errors.Is(err, ErrDuplicateReq):
		return "req already in flight on some conn/stream"
	case errors.Is(err, ErrClockBackwards):
		return "now earlier than last accepted call; rejected without side effects"
	case errors.Is(err, ErrNoCapacity):
		return "no Active conn with activeStreams < maxConc"
	default:
		c := n.conns[picked]
		drain := ""
		if c.state == Draining {
			drain = "; nextID exceeded maxID, conn now Draining"
		}
		return fmt.Sprintf("picked %s: fewest active streams among Active conns with headroom, smallest id on ties%s", picked, drain)
	}
}

func closeReason(n *naivePool, id string, stream int64, kind StreamEndKind, err error) string {
	switch {
	case errors.Is(err, ErrUnknownConn):
		return "unknown conn id"
	case errors.Is(err, ErrUnknownStream):
		return "stream not in conn's active table (already closed or removed by GOAWAY)"
	case errors.Is(err, ErrInvalidKind):
		return "unknown stream end kind"
	case errors.Is(err, ErrClockBackwards):
		return "now earlier than last accepted call; rejected without side effects"
	}
	c := n.conns[id]
	base := map[StreamEndKind]string{
		Done:    "Done: refuse reset to 0, not retryable",
		Refused: "Refused: refuse incremented, retryable",
		Reset:   "Reset: refuse untouched, not retryable",
	}[kind]
	switch c.state {
	case Closed:
		return base + "; last stream removed while Draining -> Closed"
	case Draining:
		if kind == Refused {
			return base + "; refuse>=K or already draining -> Draining"
		}
		return base + "; conn stays Draining"
	default:
		if len(c.streams) == 0 {
			return base + "; conn idle now, idleSince=now"
		}
		return base
	}
}

func goAwayReason(n *naivePool, id string, lastID int64, err error) string {
	switch {
	case errors.Is(err, ErrUnknownConn):
		return "unknown conn id"
	case errors.Is(err, ErrInvalidLastID):
		return "lastID must be 0 or an odd value <= maxID"
	case errors.Is(err, ErrClockBackwards):
		return "now earlier than last accepted call; rejected without side effects"
	case errors.Is(err, ErrConnClosed):
		return "GOAWAY on a Closed conn is a state error"
	case errors.Is(err, ErrGoAwayUp):
		return "lastID above previously accepted goAwayLast"
	case errors.Is(err, ErrGoAwayBeyond):
		return "lastID beyond last assigned stream and != maxID"
	}
	c := n.conns[id]
	if c.state == Closed {
		return "accepted: streams > lastID retried ascending; no streams left -> Closed"
	}
	return "accepted: streams > lastID retried ascending; conn Draining"
}

func setMaxReason(n *naivePool, id string, m int, err error) string {
	switch {
	case errors.Is(err, ErrUnknownConn):
		return "unknown conn id"
	case errors.Is(err, ErrInvalidMaxConc):
		return "m outside [1,1000]"
	case errors.Is(err, ErrClockBackwards):
		return "now earlier than last accepted call; rejected without side effects"
	case errors.Is(err, ErrConnClosed):
		return "SetMaxConcurrent on a Closed conn is a state error"
	default:
		return "accepted: only maxConc changes; active streams are never cancelled"
	}
}

func tickReason(n *naivePool, closed []string, err error) string {
	switch {
	case errors.Is(err, ErrClockBackwards):
		return "now earlier than last accepted call; rejected without side effects"
	case len(closed) == 0:
		return "no Active conn with 0 streams idle for >= idleTimeout"
	default:
		return "closed Active conns with 0 streams and now-idleSince >= idleTimeout (inclusive)"
	}
}

// TestConcurrentSmoke hammers the pool from many goroutines. With -race it
// proves data-race freedom; the final invariant check proves every in-flight
// request sits on exactly one stream of exactly one conn.
func TestConcurrentSmoke(t *testing.T) {
	p := mustPool(t, 4, 50, 1<<20-1, 3)
	for i := 0; i < 4; i++ {
		if err := p.AddConn(0, string(rune('a'+i))); err != nil {
			t.Fatalf("AddConn: %v", err)
		}
	}
	var wg sync.WaitGroup
	var clock int64
	for g := 0; g < 8; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			rng := rand.New(rand.NewSource(int64(g) + 1))
			for i := 0; i < 300; i++ {
				now := atomic.AddInt64(&clock, int64(rng.Intn(3)))
				req := fmt.Sprintf("g%d-r%d", g, i)
				id, stream, err := p.Open(now, req)
				if err != nil {
					p.Tick(now) //nolint:errcheck // smoke test: outcomes are timing-dependent
					continue
				}
				kind := []StreamEndKind{Done, Refused, Reset}[rng.Intn(3)]
				for attempt := 0; attempt < 100; attempt++ {
					_, err := p.CloseStream(now, id, stream, kind)
					if err == nil {
						break
					}
					if !errors.Is(err, ErrClockBackwards) {
						t.Errorf("CloseStream(%q,%d): %v", id, stream, err)
						break
					}
					now = atomic.AddInt64(&clock, 1)
					if attempt == 99 {
						t.Errorf("CloseStream(%q,%d): still rejected after 100 retries", id, stream)
					}
				}
			}
		}(g)
	}
	wg.Wait()

	seen := make(map[string]int)
	for _, id := range p.sortedIDs() {
		snap, err := p.Snapshot(id)
		if err != nil {
			t.Fatalf("Snapshot(%q): %v", id, err)
		}
		for s, req := range snap.Streams {
			if s%2 == 0 || s > snap.NextID-2 {
				t.Errorf("conn %q holds invalid stream id %d", id, s)
			}
			seen[req]++
		}
		if snap.State == Closed && len(snap.Streams) != 0 {
			t.Errorf("conn %q Closed with %d active streams", id, len(snap.Streams))
		}
	}
	for req, count := range seen {
		if count != 1 {
			t.Errorf("req %q appears on %d streams, want exactly 1", req, count)
		}
	}
}
