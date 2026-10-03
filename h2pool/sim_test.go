package h2pool

// This file contains a naive reference implementation of the pool rules,
// written as literally as possible from the specification, plus a
// randomized differential test that replays 2000 random call sequences
// against both implementations and compares connection choices, retry
// lists, state transitions and error codes after every single call.

import (
	"fmt"
	"math/rand"
	"reflect"
	"sort"
	"testing"
)

type naiveConn struct {
	id         string
	state      State
	maxConc    int
	nextID     int64
	streams    map[int64]string
	goAwayLast int64
	idleSince  int64
	refuse     int
}

type naiveSim struct {
	m0          int
	idleTimeout int64
	maxID       int64
	k           int
	conns       []*naiveConn // insertion order; Closed conns stay in the list
	inFlight    map[string]string
	lastNow     int64
	hasNow      bool
}

func newNaiveSim(m0 int, idleTimeout, maxID int64, k int) *naiveSim {
	return &naiveSim{
		m0:          m0,
		idleTimeout: idleTimeout,
		maxID:       maxID,
		k:           k,
		inFlight:    make(map[string]string),
	}
}

func (n *naiveSim) find(id string) *naiveConn {
	for _, c := range n.conns {
		if c.id == id {
			return c
		}
	}
	return nil
}

func (n *naiveSim) checkNow(now int64) ErrCode {
	if now < 0 || now > 1_000_000_000_000 {
		return ErrBadNow
	}
	if n.hasNow && now < n.lastNow {
		return ErrClockBackwards
	}
	return 0
}

func (n *naiveSim) accept(now int64) {
	n.lastNow = now
	n.hasNow = true
}

func (n *naiveSim) addConn(now int64, id string) ErrCode {
	if n.find(id) != nil {
		return ErrDupConn
	}
	if code := n.checkNow(now); code != 0 {
		return code
	}
	n.accept(now)
	n.conns = append(n.conns, &naiveConn{
		id:         id,
		state:      Active,
		maxConc:    n.m0,
		nextID:     1,
		streams:    make(map[int64]string),
		goAwayLast: n.maxID,
		idleSince:  now,
	})
	return 0
}

func (n *naiveSim) open(now int64, req string) (string, int64, ErrCode) {
	if _, dup := n.inFlight[req]; dup {
		return "", 0, ErrDupReq
	}
	if code := n.checkNow(now); code != 0 {
		return "", 0, code
	}
	// Candidates: Active with active streams strictly below maxConc.
	var best *naiveConn
	for _, c := range n.conns {
		if c.state != Active || len(c.streams) >= c.maxConc {
			continue
		}
		if best == nil ||
			len(c.streams) < len(best.streams) ||
			(len(c.streams) == len(best.streams) && c.id < best.id) {
			best = c
		}
	}
	if best == nil {
		return "", 0, ErrNoCapacity
	}
	n.accept(now)
	stream := best.nextID
	best.streams[stream] = req
	n.inFlight[req] = best.id
	best.nextID += 2
	if best.nextID > n.maxID {
		best.state = Draining
	}
	return best.id, stream, 0
}

func (n *naiveSim) setMaxConcurrent(now int64, id string, m int) ErrCode {
	c := n.find(id)
	if c == nil {
		return ErrUnknownConn
	}
	if m < 1 || m > 1000 {
		return ErrBadMaxConc
	}
	if code := n.checkNow(now); code != 0 {
		return code
	}
	if c.state == Closed {
		return ErrConnClosed
	}
	n.accept(now)
	c.maxConc = m
	return 0
}

func (n *naiveSim) closeStream(now int64, id string, stream int64, kind CloseKind) (bool, ErrCode) {
	c := n.find(id)
	if c == nil {
		return false, ErrUnknownConn
	}
	req, ok := c.streams[stream]
	if !ok {
		return false, ErrUnknownStream
	}
	if kind != Done && kind != Refused && kind != Reset {
		return false, ErrBadKind
	}
	if code := n.checkNow(now); code != 0 {
		return false, code
	}
	n.accept(now)
	delete(c.streams, stream)
	delete(n.inFlight, req)
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
	case Reset:
	}
	if len(c.streams) == 0 {
		if c.state == Draining {
			c.state = Closed
		} else if c.state == Active {
			c.idleSince = now
		}
	}
	return retryable, 0
}

func (n *naiveSim) goAway(now int64, id string, lastID int64) ([]string, ErrCode) {
	c := n.find(id)
	if c == nil {
		return nil, ErrUnknownConn
	}
	if lastID != 0 && (lastID < 0 || lastID > n.maxID || lastID%2 == 0) {
		return nil, ErrBadLastID
	}
	if code := n.checkNow(now); code != 0 {
		return nil, code
	}
	if c.state == Closed {
		return nil, ErrConnClosed
	}
	if lastID > c.goAwayLast {
		return nil, ErrGoAwayUp
	}
	lastAlloc := int64(0)
	if c.nextID > 1 {
		lastAlloc = c.nextID - 2
	}
	if lastID > lastAlloc && lastID != n.maxID {
		return nil, ErrGoAwayBeyond
	}
	n.accept(now)
	c.goAwayLast = lastID
	if c.state == Active {
		c.state = Draining
	}
	unprocessed := []string{}
	streams := make([]int64, 0, len(c.streams))
	for s := range c.streams {
		streams = append(streams, s)
	}
	sort.Slice(streams, func(i, j int) bool { return streams[i] < streams[j] })
	for _, s := range streams {
		if s > lastID {
			unprocessed = append(unprocessed, c.streams[s])
			delete(n.inFlight, c.streams[s])
			delete(c.streams, s)
		}
	}
	if len(c.streams) == 0 {
		c.state = Closed
	}
	return unprocessed, 0
}

func (n *naiveSim) tick(now int64) ([]string, ErrCode) {
	if code := n.checkNow(now); code != 0 {
		return nil, code
	}
	n.accept(now)
	closed := []string{}
	for _, c := range n.conns {
		if c.state == Active && len(c.streams) == 0 && now-c.idleSince >= n.idleTimeout {
			c.state = Closed
			closed = append(closed, c.id)
		}
	}
	sort.Strings(closed)
	return closed, 0
}

// ---------------------------------------------------------------------------
// Randomized differential test
// ---------------------------------------------------------------------------

var connNames = []string{"A", "B", "C", "D", "E"}

// compareStates verifies that every connection of p matches the naive sim
// exactly, and that the in-flight request sets and clocks agree.
func compareStates(t *testing.T, seq, step int, p *Pool, n *naiveSim) {
	t.Helper()
	if p.lastNow != n.lastNow || p.hasNow != n.hasNow {
		t.Fatalf("seq=%d step=%d: clock diverged: pool(%d,%v) naive(%d,%v)",
			seq, step, p.lastNow, p.hasNow, n.lastNow, n.hasNow)
	}
	if !reflect.DeepEqual(p.inFlight, n.inFlight) {
		t.Fatalf("seq=%d step=%d: inFlight diverged: pool=%v naive=%v",
			seq, step, p.inFlight, n.inFlight)
	}
	if len(p.conns) != len(n.conns) {
		t.Fatalf("seq=%d step=%d: conn count %d != %d", seq, step, len(p.conns), len(n.conns))
	}
	for _, nc := range n.conns {
		snap, ok := p.Snapshot(nc.id)
		if !ok {
			t.Fatalf("seq=%d step=%d: conn %q missing from pool", seq, step, nc.id)
		}
		want := ConnSnapshot{
			ID:         nc.id,
			State:      nc.state,
			MaxConc:    nc.maxConc,
			NextID:     nc.nextID,
			Streams:    nc.streams,
			GoAwayLast: nc.goAwayLast,
			IdleSince:  nc.idleSince,
			Refuse:     nc.refuse,
		}
		if len(want.Streams) == 0 {
			want.Streams = map[int64]string{}
		}
		if len(snap.Streams) == 0 {
			snap.Streams = map[int64]string{}
		}
		if !reflect.DeepEqual(snap, want) {
			t.Fatalf("seq=%d step=%d: conn %q diverged:\n pool=%+v\nnaive=%+v",
				seq, step, nc.id, snap, want)
		}
	}
}

func errCode(err error) ErrCode { return CodeOf(err) }

// TestRandomAgainstNaive replays 2000 random call sequences against both
// the pool and the naive simulation, comparing every result and the full
// state after every call. Each call is logged with its input, output and
// the deciding rule (visible with `go test -v`).
func TestRandomAgainstNaive(t *testing.T) {
	const sequences = 2000
	const steps = 40

	for seq := 0; seq < sequences; seq++ {
		rng := rand.New(rand.NewSource(int64(seq)*7919 + 13))
		m0 := 1 + rng.Intn(4)
		idleTimeout := int64(1 + rng.Intn(300))
		maxID := int64(1 + 2*rng.Intn(15)) // small: exercises ID exhaustion
		k := 1 + rng.Intn(3)

		p, err := NewPool(m0, idleTimeout, maxID, k)
		if err != nil {
			t.Fatalf("seq=%d: NewPool: %v", seq, err)
		}
		n := newNaiveSim(m0, idleTimeout, maxID, k)
		t.Logf("seq=%d config m0=%d idleTimeout=%d maxID=%d K=%d", seq, m0, idleTimeout, maxID, k)

		now := int64(0)
		reqCounter := 0

		for step := 0; step < steps; step++ {
			// Clock: mostly forward, sometimes same, rarely backwards.
			switch r := rng.Intn(100); {
			case r < 5:
				now -= int64(rng.Intn(50)) // possible rollback
			case r < 25:
				// same now
			default:
				now += int64(rng.Intn(120))
			}
			if now < 0 {
				now = 0
			}

			op := rng.Intn(100)
			switch {
			case op < 15: // AddConn
				id := connNames[rng.Intn(len(connNames))]
				got := p.AddConn(now, id)
				want := n.addConn(now, id)
				t.Logf("seq=%d step=%d now=%d AddConn(%s) -> err=%v (rule: dup-id, then clock)", seq, step, now, id, errCode(got))
				if errCode(got) != want {
					t.Fatalf("seq=%d step=%d AddConn(%q): pool=%v naive=%v", seq, step, id, errCode(got), want)
				}

			case op < 45: // Open
				var req string
				if rng.Intn(10) == 0 && len(n.inFlight) > 0 {
					for r := range n.inFlight { // duplicate req
						req = r
						break
					}
				} else {
					req = fmt.Sprintf("req-%d-%d", seq, reqCounter)
					reqCounter++
				}
				gotID, gotStream, gotErr := p.Open(now, req)
				wantID, wantStream, wantCode := n.open(now, req)
				t.Logf("seq=%d step=%d now=%d Open(%s) -> (%s,%d,%v) (rule: min active streams, tie -> min id; nextID>maxID drains)",
					seq, step, now, req, gotID, gotStream, errCode(gotErr))
				if gotID != wantID || gotStream != wantStream || errCode(gotErr) != wantCode {
					t.Fatalf("seq=%d step=%d Open(%q): pool=(%q,%d,%v) naive=(%q,%d,%v)",
						seq, step, req, gotID, gotStream, errCode(gotErr), wantID, wantStream, wantCode)
				}

			case op < 60: // CloseStream
				var id string
				var stream int64
				if rng.Intn(10) > 0 && len(n.conns) > 0 {
					c := n.conns[rng.Intn(len(n.conns))]
					id = c.id
					if len(c.streams) > 0 && rng.Intn(10) > 1 {
						for s := range c.streams {
							stream = s
							break
						}
					} else {
						stream = int64(2*rng.Intn(20) + 1) // likely unknown
					}
				} else {
					id = connNames[rng.Intn(len(connNames))]
					stream = int64(2*rng.Intn(20) + 1)
				}
				kind := CloseKind(rng.Intn(4)) // 3 is invalid on purpose
				gotRetry, gotErr := p.CloseStream(now, id, stream, kind)
				wantRetry, wantCode := n.closeStream(now, id, stream, kind)
				t.Logf("seq=%d step=%d now=%d CloseStream(%s,%d,%v) -> (retry=%v,err=%v) (rule: Done clears refuse; Refused>=K drains; empty Draining closes, empty Active re-idles)",
					seq, step, now, id, stream, kind, gotRetry, errCode(gotErr))
				if gotRetry != wantRetry || errCode(gotErr) != wantCode {
					t.Fatalf("seq=%d step=%d CloseStream(%q,%d,%v): pool=(%v,%v) naive=(%v,%v)",
						seq, step, id, stream, kind, gotRetry, errCode(gotErr), wantRetry, wantCode)
				}

			case op < 72: // SetMaxConcurrent
				id := connNames[rng.Intn(len(connNames))]
				m := rng.Intn(1003) // sometimes 0 or >1000
				got := p.SetMaxConcurrent(now, id, m)
				want := n.setMaxConcurrent(now, id, m)
				t.Logf("seq=%d step=%d now=%d SetMaxConcurrent(%s,%d) -> err=%v (rule: shrink allowed, active streams kept)",
					seq, step, now, id, m, errCode(got))
				if errCode(got) != want {
					t.Fatalf("seq=%d step=%d SetMaxConcurrent(%q,%d): pool=%v naive=%v",
						seq, step, id, m, errCode(got), want)
				}

			case op < 87: // GoAway
				id := connNames[rng.Intn(len(connNames))]
				var lastID int64
				switch rng.Intn(8) {
				case 0:
					lastID = 0
				case 1:
					lastID = maxID
				case 2:
					lastID = int64(2 * rng.Intn(20)) // even -> invalid unless 0
				case 3:
					lastID = -1
				case 4:
					lastID = maxID + 2
				default:
					lastID = int64(2*rng.Intn(20) + 1)
				}
				gotList, gotErr := p.GoAway(now, id, lastID)
				wantList, wantCode := n.goAway(now, id, lastID)
				if len(gotList) == 0 {
					gotList = []string{}
				}
				if len(wantList) == 0 {
					wantList = []string{}
				}
				t.Logf("seq=%d step=%d now=%d GoAway(%s,%d) -> (unprocessed=%v,err=%v) (rule: lastID>goAwayLast up; >lastAlloc&&!=maxID beyond; streams>lastID retried ascending; empty closes)",
					seq, step, now, id, lastID, gotList, errCode(gotErr))
				if errCode(gotErr) != wantCode || !reflect.DeepEqual(gotList, wantList) {
					t.Fatalf("seq=%d step=%d GoAway(%q,%d): pool=(%v,%v) naive=(%v,%v)",
						seq, step, id, lastID, gotList, errCode(gotErr), wantList, wantCode)
				}

			default: // Tick
				gotList, gotErr := p.Tick(now)
				wantList, wantCode := n.tick(now)
				if len(gotList) == 0 {
					gotList = []string{}
				}
				if len(wantList) == 0 {
					wantList = []string{}
				}
				t.Logf("seq=%d step=%d now=%d Tick() -> (closed=%v,err=%v) (rule: Active && 0 streams && now-idleSince>=idleTimeout closes)",
					seq, step, now, gotList, errCode(gotErr))
				if errCode(gotErr) != wantCode || !reflect.DeepEqual(gotList, wantList) {
					t.Fatalf("seq=%d step=%d Tick: pool=(%v,%v) naive=(%v,%v)",
						seq, step, gotList, errCode(gotErr), wantList, wantCode)
				}
			}

			compareStates(t, seq, step, p, n)
		}
	}
}
