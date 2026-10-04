package route_test

import (
	"errors"
	"fmt"
	"math/rand"
	"testing"

	"ontology/route"
)

// Independent naive model: it keeps every accepted event forever and answers
// each question by a full linear re-scan, encoding the specification text
// literally. The optimized implementation must agree on every answer.

type simBlock struct {
	prefix string
	length int
	op     int64
	effAt  int64
}

type simPort struct {
	id        int64
	number    string
	recipient int64
	at        int64
	dead      bool // cancelled, or wiped by a later disconnect
}

type simDisc struct {
	number string
	at     int64
}

type naiveSim struct {
	lmin, q int64
	blocks  []simBlock
	ports   []simPort
	discs   []simDisc
	maxNow  int64
	nextID  int64
}

func newNaive(lmin, q int64) *naiveSim {
	return &naiveSim{lmin: lmin, q: q}
}

func validDec(s string) bool {
	for i := range s {
		if s[i] < '0' || s[i] > '9' {
			return false
		}
	}
	return len(s) > 0
}

func (s *naiveSim) home(number string, t int64) (int64, bool) {
	var bestLen int
	var op int64
	for _, b := range s.blocks {
		if b.length != len(number) || b.effAt > t {
			continue
		}
		if len(number) < len(b.prefix) || number[:len(b.prefix)] != b.prefix {
			continue
		}
		if len(b.prefix) > bestLen {
			bestLen = len(b.prefix)
			op = b.op
		}
	}
	return op, bestLen > 0
}

// state is the literal spec: latest disconnect vs latest effective port.
func (s *naiveSim) state(number string, t int64) (server, homeOp int64, allocated, frozen bool) {
	h, ok := s.home(number, t)
	if !ok {
		return 0, 0, false, false
	}
	var latestDisc int64 = -1
	for _, d := range s.discs {
		if d.number == number && d.at <= t && d.at > latestDisc {
			latestDisc = d.at
		}
	}
	if latestDisc >= 0 && t < latestDisc+s.q {
		return 0, h, true, true
	}
	server = h
	var latestPort int64 = -1
	var recipient int64
	for _, p := range s.ports {
		if p.dead || p.number != number || p.at > t {
			continue
		}
		if latestDisc >= 0 && p.at <= latestDisc {
			continue // disconnect clears earlier port records
		}
		if p.at > latestPort {
			latestPort = p.at
			recipient = p.recipient
		}
	}
	if latestPort >= 0 {
		server = recipient
	}
	return server, h, true, false
}

func (s *naiveSim) pathOf(number string, orig int64, m route.Method, t int64) (route.Result, error) {
	server, h, allocated, frozen := s.state(number, t)
	if !allocated {
		return route.Result{}, route.ErrNumberUnallocated
	}
	res := route.Result{Home: h, Server: server, Frozen: frozen, Path: []int64{}}
	if frozen {
		return res, route.ErrFrozen
	}
	res.Ported = server != h
	switch {
	case server == orig:
	case m == route.ACQ:
		res.Path = []int64{server}
	case h == server || h == orig:
		res.Path = []int64{server}
	default:
		res.Path = []int64{h, server}
	}
	return res, nil
}

func (s *naiveSim) block(prefix string, length int, op, now int64) error {
	if length < 5 || length > 15 || len(prefix) < 1 || len(prefix) > length ||
		!validDec(prefix) || op < 1 || op > 10000 || now < 0 {
		return route.ErrInvalidArgument
	}
	if now < s.maxNow {
		return route.ErrClockRewound
	}
	for _, b := range s.blocks {
		if b.prefix == prefix && b.length == length {
			return route.ErrBlockExists
		}
	}
	s.blocks = append(s.blocks, simBlock{prefix, length, op, now})
	s.maxNow = now
	return nil
}

func (s *naiveSim) pendingFor(number string, now int64) bool {
	for _, p := range s.ports {
		if !p.dead && p.number == number && p.at > now {
			return true
		}
	}
	return false
}

func (s *naiveSim) request(number string, donor, recipient, at, now int64) (int64, error) {
	if !validDec(number) || donor < 1 || donor > 10000 || recipient < 1 || recipient > 10000 ||
		at < 0 || now < 0 {
		return 0, route.ErrInvalidArgument
	}
	if now < s.maxNow {
		return 0, route.ErrClockRewound
	}
	server, _, allocated, frozen := s.state(number, now)
	if !allocated {
		return 0, route.ErrNumberUnallocated
	}
	if frozen {
		return 0, route.ErrFrozen
	}
	if s.pendingFor(number, now) {
		return 0, route.ErrPendingExists
	}
	if donor != server {
		return 0, route.ErrDonorMismatch
	}
	if recipient == donor {
		return 0, route.ErrSameOperator
	}
	if at-now < s.lmin {
		return 0, route.ErrLeadTime
	}
	s.nextID++
	s.ports = append(s.ports, simPort{id: s.nextID, number: number, recipient: recipient, at: at})
	s.maxNow = now
	return s.nextID, nil
}

// findOrder returns the port index for issued id, or -1 if it never existed
// or was cancelled.
func (s *naiveSim) findOrder(id int64) int {
	for i := range s.ports {
		if s.ports[i].id == id && !s.ports[i].dead {
			return i
		}
	}
	return -1
}

func (s *naiveSim) cancel(id, now int64) error {
	if id < 1 || now < 0 {
		return route.ErrInvalidArgument
	}
	if now < s.maxNow {
		return route.ErrClockRewound
	}
	var byID *simPort
	for i := range s.ports {
		if s.ports[i].id == id {
			byID = &s.ports[i]
		}
	}
	if byID == nil {
		// Never issued, or wiped by a disconnect (all records cleared): gone.
		return route.ErrOrderNotFound
	}
	idx := s.findOrder(id)
	if idx < 0 {
		// Issued but no longer live: either cancelled/auto-cancelled (never
		// effective -> not found) or effective and retained (handled below).
		return route.ErrOrderNotFound
	}
	if now >= s.ports[idx].at {
		return route.ErrAlreadyEffective
	}
	s.ports[idx].dead = true
	s.maxNow = now
	return nil
}

func (s *naiveSim) disconnect(number string, now int64) error {
	if !validDec(number) || now < 0 {
		return route.ErrInvalidArgument
	}
	if now < s.maxNow {
		return route.ErrClockRewound
	}
	_, _, allocated, frozen := s.state(number, now)
	if !allocated {
		return route.ErrNumberUnallocated
	}
	if frozen {
		return route.ErrFrozen
	}
	for i := range s.ports {
		// Only the not-yet-effective order is auto-cancelled; effective ports
		// stay on record for history but are shadowed by the disconnect.
		if s.ports[i].number == number && s.ports[i].at > now {
			s.ports[i].dead = true
		}
	}
	s.discs = append(s.discs, simDisc{number: number, at: now})
	s.maxNow = now
	return nil
}

func sameErr(a, b error) bool {
	return errors.Is(a, b) && errors.Is(b, a)
}

func TestNaiveSimulation1500(t *testing.T) {
	const sequences = 1500
	for seed := int64(1); seed <= sequences; seed++ {
		rng := rand.New(rand.NewSource(seed))
		lmin := int64(rng.Intn(6))
		q := int64(rng.Intn(8))
		r := route.New(lmin, q)
		sim := newNaive(lmin, q)

		for _, b := range []struct {
			prefix string
			op     int64
			at     int64
		}{
			{"1380", 1, 0},
			{"13805", 2, 5},
		} {
			if e1, e2 := r.AssignBlock(b.prefix, 11, b.op, b.at),
				sim.block(b.prefix, 11, b.op, b.at); !sameErr(e1, e2) {
				t.Fatalf("seed block mismatch: %v vs %v", e1, e2)
			}
		}

		numbers := make([]string, 0, 8)
		for i := 0; i < 8; i++ {
			numbers = append(numbers, fmt.Sprintf("13805%06d", i*1000+rng.Intn(900)))
		}

		var clock int64 = 5
		for step := 0; step < 80; step++ {
			clock += int64(rng.Intn(3))
			num := numbers[rng.Intn(len(numbers))]
			// Advance both clocks to the new step time with an accepted Query
			// on a stable, never-frozen number, so later historical QueryAt
			// values are within the accepted horizon on both sides.
			if clock > r.MaxNow() {
				const clockNum = "13805000000"
				if _, qe := r.Query(clockNum, 9, route.ACQ, clock); qe != nil {
					t.Fatalf("seed=%d step=%d clock tick query: %v", seed, step, qe)
				}
				if _, qe := sim.pathOf(clockNum, 9, route.ACQ, clock); qe != nil {
					t.Fatalf("seed=%d step=%d naive clock tick query: %v", seed, step, qe)
				}
				sim.maxNow = clock
			}
			srv, _, _, _ := sim.state(num, clock)

			switch rng.Intn(10) {
			case 0: // nested block (may duplicate or re-home)
				opd := int64(1 + rng.Intn(4))
				prefix := num[:5+rng.Intn(3)]
				e1 := r.AssignBlock(prefix, 11, opd, clock)
				e2 := sim.block(prefix, 11, opd, clock)
				if !sameErr(e1, e2) {
					t.Fatalf("seed=%d step=%d BLOCK input=(%s,%d,%d) impl=%v naive=%v",
						seed, step, prefix, opd, clock, e1, e2)
				}
				t.Logf("seed=%d step=%d input=BLOCK(%s,L=11,op=%d,now=%d) output_impl=%v output_naive=%v basis=longest-prefix",
					seed, step, prefix, opd, clock, e1, e2)

			case 1: // well-formed port request
				recipient := int64(1 + rng.Intn(4))
				at := clock + lmin + int64(rng.Intn(12))
				id1, e1 := r.RequestPort(num, srv, recipient, at, clock)
				id2, e2 := sim.request(num, srv, recipient, at, clock)
				if !sameErr(e1, e2) || e1 == nil && id1 != id2 {
					t.Fatalf("seed=%d step=%d REQUEST input=(%s,donor=%d,rcp=%d,at=%d,now=%d) impl=(%d,%v) naive=(%d,%v)",
						seed, step, num, srv, recipient, at, clock, id1, e1, id2, e2)
				}
				t.Logf("seed=%d step=%d input=REQUEST(%s,%d->%d,at=%d,now=%d) output_impl=(%d,%v) output_naive=(%d,%v) basis=eight-level-check",
					seed, step, num, srv, recipient, at, clock, id1, e1, id2, e2)

			case 2: // wrong-donor request
				bad := int64(1 + rng.Intn(4))
				id1, e1 := r.RequestPort(num, bad, bad%4+1, clock+lmin, clock)
				id2, e2 := sim.request(num, bad, bad%4+1, clock+lmin, clock)
				if !sameErr(e1, e2) || e1 == nil && id1 != id2 {
					t.Fatalf("seed=%d step=%d BAD-DONOR impl=(%d,%v) naive=(%d,%v)", seed, step, id1, e1, id2, e2)
				}

			case 3: // short lead time request
				rcp := int64(1 + rng.Intn(4))
				id1, e1 := r.RequestPort(num, srv, rcp, clock, clock)
				id2, e2 := sim.request(num, srv, rcp, clock, clock)
				if !sameErr(e1, e2) || e1 == nil && id1 != id2 {
					t.Fatalf("seed=%d step=%d LEAD impl=(%d,%v) naive=(%d,%v)", seed, step, id1, e1, id2, e2)
				}
				if !sameErr(e1, e2) {
					t.Fatalf("seed=%d step=%d LEAD impl=%v naive=%v", seed, step, e1, e2)
				}

			case 4: // cancel random id
				id := int64(1 + rng.Intn(5))
				e1 := r.Cancel(id, clock)
				e2 := sim.cancel(id, clock)
				if !sameErr(e1, e2) {
					t.Fatalf("seed=%d step=%d CANCEL input=(id=%d,now=%d) impl=%v naive=%v",
						seed, step, id, clock, e1, e2)
				}

			case 5: // disconnect
				e1 := r.Disconnect(num, clock)
				e2 := sim.disconnect(num, clock)
				if !sameErr(e1, e2) {
					t.Fatalf("seed=%d step=%d DISCONNECT input=(%s,%d) impl=%v naive=%v",
						seed, step, num, clock, e1, e2)
				}
				t.Logf("seed=%d step=%d input=DISCONNECT(%s,now=%d) output_impl=%v output_naive=%v basis=clear+freeze",
					seed, step, num, clock, e1, e2)

			default: // query: current or historical, ACQ or OR, random caller
				orig := int64(1 + rng.Intn(4))
				method := route.OR
				if rng.Intn(2) == 0 {
					method = route.ACQ
				}
				var r1, r2 route.Result
				var e1, e2 error
				ht := clock
				historical := rng.Intn(3) == 0 && clock > 5
				if historical {
					ht = 5 + rng.Int63n(clock-4)
					r1, e1 = r.QueryAt(num, orig, method, ht)
					r2, e2 = sim.pathOf(num, orig, method, ht)
				} else {
					r1, e1 = r.Query(num, orig, method, clock)
					r2, e2 = sim.pathOf(num, orig, method, clock)
					if e1 == nil {
						sim.maxNow = clock // accepted Query advances the clock
					}
				}
				if !sameErr(e1, e2) {
					t.Fatalf("seed=%d step=%d QUERY input=(%s,orig=%d,%s,t=%d,hist=%v) impl=%v naive=%v",
						seed, step, num, orig, method, ht, historical, e1, e2)
				}
				if e1 == nil {
					if r1.Home != r2.Home || r1.Server != r2.Server ||
						r1.Ported != r2.Ported || r1.Frozen != r2.Frozen ||
						!eqPath(r1.Path, r2.Path) {
						t.Fatalf("seed=%d step=%d QUERY input=(%s,orig=%d,%s,t=%d,hist=%v) impl=%+v naive=%+v basis=h/s-routing",
							seed, step, num, orig, method, ht, historical, r1, r2)
					}
				}
				t.Logf("seed=%d step=%d input=QUERY(%s,orig=%d,%s,t=%d,hist=%v) output_impl={h=%d s=%d ported=%v path=%v err=%v} match=%v basis=naive-linear-rescan",
					seed, step, num, orig, method, ht, historical,
					r1.Home, r1.Server, r1.Ported, r1.Path, e1, sameErr(e1, e2) && eqPath(r1.Path, r2.Path))
			}
		}
	}
}
