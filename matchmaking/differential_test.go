package matchmaking

import (
	"fmt"
	"math/rand"
	"reflect"
	"sort"
	"sync"
	"sync/atomic"
	"testing"
)

// naiveLobby is an independent reference implementation used only by tests.
// It keeps every player in a map and, on each tick, enumerates all player
// pairs brute-force to find matches.
type naiveLobby struct {
	w0, g, wmax, cap int64
	maxNow           int64
	players          map[int64]*naivePlayer
}

type naivePlayer struct {
	id, rating, joined int64
	state              playerState
}

func newNaiveLobby(w0, g, wmax, cap int64) *naiveLobby {
	return &naiveLobby{w0: w0, g: g, wmax: wmax, cap: cap, maxNow: -1, players: map[int64]*naivePlayer{}}
}

func (n *naiveLobby) tolerance(joined, now int64) int64 {
	w := n.w0 + n.g*(now-joined)
	if w > n.wmax {
		w = n.wmax
	}
	return w
}

func (n *naiveLobby) checkTime(now int64) error {
	if now < 0 || now > maxNow {
		return ErrInvalidTime
	}
	if now < n.maxNow {
		return ErrClockRollback
	}
	return nil
}

func (n *naiveLobby) accept(now int64) {
	if now > n.maxNow {
		n.maxNow = now
	}
}

func (n *naiveLobby) queuedCount() int64 {
	var c int64
	for _, p := range n.players {
		if p.state == stateQueued {
			c++
		}
	}
	return c
}

func (n *naiveLobby) join(id, rating, now int64) error {
	if err := n.checkTime(now); err != nil {
		return err
	}
	if id < 1 {
		return ErrInvalidID
	}
	if rating < 0 || rating > maxRating {
		return ErrInvalidRating
	}
	if _, ok := n.players[id]; ok {
		return ErrDuplicateID
	}
	if n.queuedCount() >= n.cap {
		return ErrQueueFull
	}
	n.players[id] = &naivePlayer{id: id, rating: rating, joined: now}
	n.accept(now)
	return nil
}

func (n *naiveLobby) leave(id, now int64) error {
	if err := n.checkTime(now); err != nil {
		return err
	}
	p, ok := n.players[id]
	if !ok {
		return ErrUnknownID
	}
	switch p.state {
	case stateMatched:
		return ErrAlreadyMatched
	case stateLeft:
		return ErrAlreadyLeft
	}
	p.state = stateLeft
	n.accept(now)
	return nil
}

// pairable reports whether a and b accept each other at now.
func (n *naiveLobby) pairable(a, b *naivePlayer, now int64) bool {
	diff := a.rating - b.rating
	if diff < 0 {
		diff = -diff
	}
	wa, wb := n.tolerance(a.joined, now), n.tolerance(b.joined, now)
	limit := wa
	if wb < limit {
		limit = wb
	}
	return diff <= limit
}

func (n *naiveLobby) tick(now int64) ([]Pair, error) {
	if err := n.checkTime(now); err != nil {
		return nil, err
	}
	// Processing order: (joined, id) ascending.
	var order []*naivePlayer
	for _, p := range n.players {
		if p.state == stateQueued {
			order = append(order, p)
		}
	}
	sort.Slice(order, func(i, j int) bool {
		if order[i].joined != order[j].joined {
			return order[i].joined < order[j].joined
		}
		return order[i].id < order[j].id
	})
	matched := map[int64]bool{}
	var formed []Pair
	for _, a := range order {
		if matched[a.id] {
			continue
		}
		// Enumerate every other still-unmatched queued player.
		var best *naivePlayer
		var bestDiff int64
		for _, b := range n.players {
			if b.id == a.id || b.state != stateQueued || matched[b.id] {
				continue
			}
			if !n.pairable(a, b, now) {
				continue
			}
			diff := a.rating - b.rating
			if diff < 0 {
				diff = -diff
			}
			if best == nil || diff < bestDiff ||
				(diff == bestDiff && (b.joined < best.joined ||
					(b.joined == best.joined && b.id < best.id))) {
				best, bestDiff = b, diff
			}
		}
		if best == nil {
			continue
		}
		matched[a.id] = true
		matched[best.id] = true
		formed = append(formed, Pair{
			A:    Player{ID: a.id, Rating: a.rating, Joined: a.joined},
			B:    Player{ID: best.id, Rating: best.rating, Joined: best.joined},
			Diff: bestDiff,
			At:   now,
		})
	}
	for _, pr := range formed {
		n.players[pr.A.ID].state = stateMatched
		n.players[pr.B.ID].state = stateMatched
	}
	n.accept(now)
	return formed, nil
}

func (n *naiveLobby) queue() []Player {
	out := []Player{}
	for _, p := range n.players {
		if p.state == stateQueued {
			out = append(out, Player{ID: p.id, Rating: p.rating, Joined: p.joined})
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Joined != out[j].Joined {
			return out[i].Joined < out[j].Joined
		}
		return out[i].ID < out[j].ID
	})
	return out
}

type randOp struct {
	kind            string // "join", "leave" or "tick"
	id, rating, now int64
}

func (o randOp) String() string {
	switch o.kind {
	case "join":
		return fmt.Sprintf("Join(id=%d, rating=%d, now=%d)", o.id, o.rating, o.now)
	case "leave":
		return fmt.Sprintf("Leave(id=%d, now=%d)", o.id, o.now)
	default:
		return fmt.Sprintf("Tick(now=%d)", o.now)
	}
}

// genOps builds one random operation sequence. The clock mostly advances;
// invalid times, rollbacks, duplicate ids and bad ratings are injected to
// exercise the rejection paths.
func genOps(rng *rand.Rand, n int) []randOp {
	ops := make([]randOp, 0, n)
	var cur, nextID int64
	cur = 0
	nextID = 1
	seenIDs := []int64{}
	now := func() int64 {
		switch rng.Intn(20) {
		case 0:
			return -1 - rng.Int63n(5) // illegal: negative
		case 1:
			return 1_000_000_001 + rng.Int63n(5) // illegal: too large
		case 2:
			if cur > 0 {
				return rng.Int63n(cur) // rollback
			}
			return cur
		default:
			cur += rng.Int63n(8)
			return cur
		}
	}
	for i := 0; i < n; i++ {
		switch rng.Intn(10) {
		case 0, 1, 2, 3, 4: // join
			id := nextID
			if len(seenIDs) > 0 && rng.Intn(4) == 0 {
				id = seenIDs[rng.Intn(len(seenIDs))] // duplicate
			} else if rng.Intn(20) == 0 {
				id = 0 // invalid
			} else {
				nextID++
			}
			rating := rng.Int63n(300) // clustered ratings -> frequent matches
			if rng.Intn(20) == 0 {
				rating = -1 + rng.Int63n(5003) // possibly out of range
			}
			ops = append(ops, randOp{kind: "join", id: id, rating: rating, now: now()})
			if id >= 1 {
				seenIDs = append(seenIDs, id)
			}
		case 5, 6: // leave
			var id int64
			if len(seenIDs) > 0 && rng.Intn(4) != 0 {
				id = seenIDs[rng.Intn(len(seenIDs))]
			} else {
				id = nextID + rng.Int63n(10) // likely unknown
			}
			ops = append(ops, randOp{kind: "leave", id: id, now: now()})
		default: // tick
			ops = append(ops, randOp{kind: "tick", now: now()})
		}
	}
	return ops
}

// opResult captures everything observable about one applied operation.
type opResult struct {
	err   error
	pairs []Pair
	queue []Player
}

func applyOp(l *Lobby, o randOp) opResult {
	var r opResult
	switch o.kind {
	case "join":
		r.err = l.Join(o.id, o.rating, o.now)
	case "leave":
		r.err = l.Leave(o.id, o.now)
	default:
		r.pairs, r.err = l.Tick(o.now)
	}
	r.queue = l.Queue()
	return r
}

func applyOpNaive(n *naiveLobby, o randOp) opResult {
	var r opResult
	switch o.kind {
	case "join":
		r.err = n.join(o.id, o.rating, o.now)
	case "leave":
		r.err = n.leave(o.id, o.now)
	default:
		r.pairs, r.err = n.tick(o.now)
	}
	r.queue = n.queue()
	return r
}

func sameErr(a, b error) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	// All operation errors are bare sentinels.
	return a == b
}

func TestRandomSequencesMatchNaive(t *testing.T) {
	const sequences = 2000
	for seq := 0; seq < sequences; seq++ {
		rng := rand.New(rand.NewSource(int64(seq)))
		w0 := rng.Int63n(30)
		g := rng.Int63n(6)
		wmax := w0 + rng.Int63n(120)
		cap := int64(2 + rng.Intn(7))
		ops := genOps(rng, 40)

		real := mustLobby(t, w0, g, wmax, cap)
		naive := newNaiveLobby(w0, g, wmax, cap)
		replay := mustLobby(t, w0, g, wmax, cap)

		t.Logf("seq=%d params W0=%d G=%d Wmax=%d Cap=%d", seq, w0, g, wmax, cap)
		for i, o := range ops {
			got := applyOp(real, o)
			want := applyOpNaive(naive, o)
			again := applyOp(replay, o)

			t.Logf("seq=%d op=%d %s -> err=%v pairs=%v queue=%v (basis: w=min(Wmax,%d+%d*(now-joined)), limit=min(wa,wb))",
				seq, i, o, got.err, pairIDs(got.pairs), queueIDs(real), w0, g)

			if !sameErr(got.err, want.err) {
				t.Fatalf("seq=%d op=%d %s: err=%v, naive=%v", seq, i, o, got.err, want.err)
			}
			if !reflect.DeepEqual(got.pairs, want.pairs) {
				t.Fatalf("seq=%d op=%d %s: pairs=%+v, naive=%+v", seq, i, o, got.pairs, want.pairs)
			}
			if !reflect.DeepEqual(got.queue, want.queue) {
				t.Fatalf("seq=%d op=%d %s: queue=%+v, naive=%+v", seq, i, o, got.queue, want.queue)
			}
			// Replaying the same sequence must reproduce results exactly.
			if !sameErr(got.err, again.err) || !reflect.DeepEqual(got.pairs, again.pairs) ||
				!reflect.DeepEqual(got.queue, again.queue) {
				t.Fatalf("seq=%d op=%d %s: replay diverged: %+v vs %+v", seq, i, o, got, again)
			}
			// Invariants after every operation.
			if int64(len(got.queue)) > cap {
				t.Fatalf("seq=%d op=%d %s: queue length %d exceeds Cap=%d", seq, i, o, len(got.queue), cap)
			}
			for _, p := range got.pairs {
				wa := real.tolerance(p.A.Joined, p.At)
				wb := real.tolerance(p.B.Joined, p.At)
				limit := wa
				if wb < limit {
					limit = wb
				}
				if p.Diff > limit {
					t.Fatalf("seq=%d op=%d: pair %+v violates mutual acceptance (limit=%d)", seq, i, p, limit)
				}
			}
			if o.kind == "tick" && got.err == nil {
				assertPostTickInvariant(t, real, o.now)
			}
		}
	}
}

// TestConcurrent hammers one lobby from many goroutines. With -race it
// proves data-race freedom; the final state checks prove the three-state
// invariant and mutual acceptance of every pair.
func TestConcurrent(t *testing.T) {
	l := mustLobby(t, 10, 1, 500, 64)
	var now atomic.Int64
	var wg sync.WaitGroup
	for g := 0; g < 8; g++ {
		wg.Add(1)
		go func(base int64) {
			defer wg.Done()
			for i := int64(0); i < 200; i++ {
				nowVal := now.Add(1) - 1
				id := base*1000 + i + 1
				switch i % 4 {
				case 0, 1:
					_ = l.Join(id, (base*7+i*13)%200, nowVal)
				case 2:
					_, _ = l.Tick(nowVal)
				default:
					_ = l.Leave(id-2, nowVal)
					_ = l.Queue()
				}
			}
		}(int64(g))
	}
	wg.Wait()

	final := now.Add(1) - 1
	pairs, err := l.Tick(final)
	if err != nil {
		t.Fatal(err)
	}
	if int64(len(l.Queue())) > 64 {
		t.Fatalf("queue length %d exceeds Cap", len(l.Queue()))
	}
	inPair := map[int64]bool{}
	l.mu.Lock()
	allPairs := append([]Pair{}, l.pairs...)
	l.mu.Unlock()
	allPairs = append(allPairs, pairs...)
	for _, p := range allPairs {
		for _, id := range []int64{p.A.ID, p.B.ID} {
			if inPair[id] {
				t.Fatalf("player %d appears in more than one pair", id)
			}
			inPair[id] = true
		}
		wa := l.tolerance(p.A.Joined, p.At)
		wb := l.tolerance(p.B.Joined, p.At)
		limit := wa
		if wb < limit {
			limit = wb
		}
		if p.Diff > limit {
			t.Fatalf("pair %+v violates mutual acceptance", p)
		}
	}
	assertPostTickInvariant(t, l, final)
}
