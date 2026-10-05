package trade_test

import (
	"fmt"
	"math/rand"
	"sync"
	"testing"

	"ontology/inventory"
	"ontology/trade"
)

// naive is a deliberately simple reference model: it recomputes locked
// amounts by scanning ALL sessions on every check, treats expiry purely
// as a view, and mirrors the documented rejection orders. The real
// system must agree with it on every operation.
type naive struct {
	r, cap, slots, ttl, q int64
	maxNow                int64
	nextSID               int64
	registered            map[string]bool
	gold                  map[string]int64
	items                 map[string]map[string]int64
	burned                int64
	sessions              map[int64]*nSession
}

type nSession struct {
	a, b      string
	items     [2]map[string]int64
	gold      [2]int64
	ver       int64
	confirmed [2]bool
	expiresAt int64
}

func newNaive(r, cap, slots, ttl, q int64) *naive {
	return &naive{
		r: r, cap: cap, slots: slots, ttl: ttl, q: q,
		maxNow: -1, registered: map[string]bool{},
		gold: map[string]int64{}, items: map[string]map[string]int64{},
		sessions: map[int64]*nSession{},
	}
}

func (n *naive) valid(s *nSession, now int64) bool { return s.expiresAt > now }

func (n *naive) side(s *nSession, who string) int {
	if who == s.a {
		return 0
	}
	if who == s.b {
		return 1
	}
	return -1
}

// lockedQty scans every session (except excludeSID) for the player's
// locks valid at now.
func (n *naive) lockedQty(p, item string, now, excludeSID int64) int64 {
	var total int64
	for sid, s := range n.sessions {
		if sid == excludeSID || !n.valid(s, now) {
			continue
		}
		if side := n.side(s, p); side >= 0 {
			total += s.items[side][item]
		}
	}
	return total
}

func (n *naive) lockedGold(p string, now, excludeSID int64) int64 {
	var total int64
	for sid, s := range n.sessions {
		if sid == excludeSID || !n.valid(s, now) {
			continue
		}
		if side := n.side(s, p); side >= 0 {
			total += s.gold[side]
		}
	}
	return total
}

func (n *naive) openCount(p string, now int64) int64 {
	var total int64
	for _, s := range n.sessions {
		if n.valid(s, now) && n.side(s, p) >= 0 {
			total++
		}
	}
	return total
}

func (n *naive) qty(p, item string) int64 { return n.items[p][item] }

func (n *naive) kinds(p string) int64 { return int64(len(n.items[p])) }

func (n *naive) checkClock(now int64) (error, string) {
	if now < 0 || now > 1_000_000_000_000 {
		return trade.ErrParam, "now out of [0,1e12]"
	}
	if now < n.maxNow {
		return trade.ErrClock, fmt.Sprintf("now %d < committed %d", now, n.maxNow)
	}
	return nil, ""
}

func (n *naive) grant(p, item string, qty int64) (error, string) {
	if p == "" || item == "" || qty < 1 || qty > 1_000_000_000_000 {
		return inventory.ErrParam, "grant param"
	}
	if n.items[p][item] == 0 && int64(len(n.items[p])) >= n.slots {
		return inventory.ErrSlotLimit, "slot limit"
	}
	if n.items[p] == nil {
		n.items[p] = map[string]int64{}
	}
	n.items[p][item] += qty
	n.registered[p] = true
	return nil, "granted"
}

func (n *naive) grantGold(p string, g int64) (error, string) {
	if p == "" || g < 0 || g > 1_000_000_000_000 {
		return inventory.ErrParam, "grantGold param"
	}
	if n.gold[p]+g > n.cap {
		return inventory.ErrGoldCap, "gold cap"
	}
	n.gold[p] += g
	n.registered[p] = true
	return nil, "granted"
}

func (n *naive) open(now int64, a, b string) (int64, error, string) {
	if a == "" || b == "" || a == b {
		return 0, trade.ErrParam, "open param"
	}
	if err, why := n.checkClock(now); err != nil {
		return 0, err, why
	}
	if !n.registered[a] || !n.registered[b] {
		return 0, trade.ErrNoPlayer, "unregistered player"
	}
	if n.openCount(a, now) >= n.q {
		return 0, trade.ErrSessionLimit, "a at session limit"
	}
	if n.openCount(b, now) >= n.q {
		return 0, trade.ErrSessionLimit, "b at session limit"
	}
	n.nextSID++
	sid := n.nextSID
	n.sessions[sid] = &nSession{a: a, b: b, expiresAt: now + n.ttl}
	n.maxNow = now
	return sid, nil, "opened"
}

func (n *naive) offer(now, sid int64, who string, items map[string]int64, gold int64) (error, string) {
	if sid < 1 || who == "" {
		return trade.ErrParam, "offer param: sid/who"
	}
	if len(items) > 16 {
		return trade.ErrParam, "offer param: >16 kinds"
	}
	for item, q := range items {
		if item == "" || q < 1 || q > 1_000_000_000 {
			return trade.ErrParam, "offer param: item/qty"
		}
	}
	if gold < 0 || gold > n.cap {
		return trade.ErrParam, "offer param: gold"
	}
	if err, why := n.checkClock(now); err != nil {
		return err, why
	}
	s := n.sessions[sid]
	if s == nil || !n.valid(s, now) {
		return trade.ErrNoSession, "no such (live) session"
	}
	side := n.side(s, who)
	if side < 0 {
		return trade.ErrNotMember, "not a member"
	}
	for item, q := range items {
		avail := n.qty(who, item) - n.lockedQty(who, item, now, sid)
		if q > avail {
			return trade.ErrInsufficient, fmt.Sprintf("item %s: need %d, available %d", item, q, avail)
		}
	}
	if avail := n.gold[who] - n.lockedGold(who, now, sid); gold > avail {
		return trade.ErrInsufficient, fmt.Sprintf("gold: need %d, available %d", gold, avail)
	}
	cp := make(map[string]int64, len(items))
	for item, q := range items {
		cp[item] = q
	}
	s.items[side] = cp
	s.gold[side] = gold
	s.ver++
	s.confirmed = [2]bool{}
	s.expiresAt = now + n.ttl
	n.maxNow = now
	return nil, "offer replaced"
}

func (n *naive) kindsAfter(p string, out, in map[string]int64) int64 {
	kinds := n.kinds(p)
	for item, q := range out {
		pre := n.qty(p, item)
		if pre > 0 && pre-q+in[item] == 0 {
			kinds--
		}
	}
	for item, q := range in {
		if _, paid := out[item]; paid {
			continue
		}
		if n.qty(p, item) == 0 && q > 0 {
			kinds++
		}
	}
	return kinds
}

func nTax(g, r int64) int64 { return (g*r + 999) / 1000 }

func (n *naive) confirm(now, sid int64, who string, ver int64) (error, string) {
	if sid < 1 || who == "" || ver < 0 {
		return trade.ErrParam, "confirm param"
	}
	if err, why := n.checkClock(now); err != nil {
		return err, why
	}
	s := n.sessions[sid]
	if s == nil || !n.valid(s, now) {
		return trade.ErrNoSession, "no such (live) session"
	}
	side := n.side(s, who)
	if side < 0 {
		return trade.ErrNotMember, "not a member"
	}
	if ver != s.ver {
		return trade.ErrStale, fmt.Sprintf("ver %d != current %d", ver, s.ver)
	}
	if s.confirmed[side] {
		return trade.ErrAlreadyConfirmed, "already confirmed"
	}
	empty := func(i int) bool { return s.gold[i] == 0 && len(s.items[i]) == 0 }
	if empty(0) && empty(1) {
		return trade.ErrEmptyTrade, "both quotes empty"
	}
	if !s.confirmed[1-side] {
		s.confirmed[side] = true
		n.maxNow = now
		return nil, "confirm recorded"
	}
	taxA := nTax(s.gold[0], n.r)
	taxB := nTax(s.gold[1], n.r)
	if n.gold[s.a]-s.gold[0]+s.gold[1]-taxB > n.cap {
		return trade.ErrGoldCap, "a post-trade gold over cap"
	}
	if n.gold[s.b]-s.gold[1]+s.gold[0]-taxA > n.cap {
		return trade.ErrGoldCap, "b post-trade gold over cap"
	}
	if n.kindsAfter(s.a, s.items[0], s.items[1]) > n.slots {
		return trade.ErrSlots, "a post-trade kinds over slots"
	}
	if n.kindsAfter(s.b, s.items[1], s.items[0]) > n.slots {
		return trade.ErrSlots, "b post-trade kinds over slots"
	}
	for item, q := range s.items[0] {
		n.items[s.a][item] -= q
		if n.items[s.a][item] == 0 {
			delete(n.items[s.a], item)
		}
		if n.items[s.b] == nil {
			n.items[s.b] = map[string]int64{}
		}
		n.items[s.b][item] += q
	}
	for item, q := range s.items[1] {
		n.items[s.b][item] -= q
		if n.items[s.b][item] == 0 {
			delete(n.items[s.b], item)
		}
		if n.items[s.a] == nil {
			n.items[s.a] = map[string]int64{}
		}
		n.items[s.a][item] += q
	}
	n.gold[s.a] += -s.gold[0] + s.gold[1] - taxB
	n.gold[s.b] += -s.gold[1] + s.gold[0] - taxA
	n.burned += taxA + taxB
	delete(n.sessions, sid)
	n.maxNow = now
	return nil, "trade settled"
}

func (n *naive) cancel(now, sid int64, who string) (error, string) {
	if sid < 1 || who == "" {
		return trade.ErrParam, "cancel param"
	}
	if err, why := n.checkClock(now); err != nil {
		return err, why
	}
	s := n.sessions[sid]
	if s == nil || !n.valid(s, now) {
		return trade.ErrNoSession, "no such (live) session"
	}
	if n.side(s, who) < 0 {
		return trade.ErrNotMember, "not a member"
	}
	delete(n.sessions, sid)
	n.maxNow = now
	return nil, "cancelled"
}

func pickStr(rng *rand.Rand, xs []string) string { return xs[rng.Intn(len(xs))] }

func pickInt(rng *rand.Rand, xs []int64) int64 { return xs[rng.Intn(len(xs))] }

// compareState asserts the real system and the naive model observe the
// same world at the committed clock.
func compareState(t *testing.T, s *trade.System, n *naive, players, items []string) {
	t.Helper()
	for _, p := range players {
		if got, want := s.Gold(p), n.gold[p]; got != want {
			t.Fatalf("%s gold: sys=%d naive=%d", p, got, want)
		}
		if got, want := s.Kinds(p), n.kinds(p); got != want {
			t.Fatalf("%s kinds: sys=%d naive=%d", p, got, want)
		}
		if got, want := s.LockedGold(p), n.lockedGold(p, n.maxNow, -1); got != want {
			t.Fatalf("%s lockedGold: sys=%d naive=%d", p, got, want)
		}
		if got, want := int64(s.OpenSessions(p)), n.openCount(p, n.maxNow); got != want {
			t.Fatalf("%s openSessions: sys=%d naive=%d", p, got, want)
		}
		for _, item := range items {
			if got, want := s.Qty(p, item), n.qty(p, item); got != want {
				t.Fatalf("%s %s: sys=%d naive=%d", p, item, got, want)
			}
			if got, want := s.LockedQty(p, item), n.lockedQty(p, item, n.maxNow, -1); got != want {
				t.Fatalf("%s locked %s: sys=%d naive=%d", p, item, got, want)
			}
		}
	}
	if got, want := s.Burned(), n.burned; got != want {
		t.Fatalf("burned: sys=%d naive=%d", got, want)
	}
}

// TestRandomAgainstNaive replays 1500 random operation sequences on the
// real system (twice, to prove replay determinism) and on the naive
// model, comparing every operation's outcome and the full observable
// state after every step. Each step is logged with input, output and
// the decision basis.
func TestRandomAgainstNaive(t *testing.T) {
	players := []string{"p0", "p1", "p2", "p3"}
	items := []string{"i0", "i1", "i2", "i3", "i4"}
	for seq := 0; seq < 1500; seq++ {
		seq := seq
		t.Run(fmt.Sprintf("seq%04d", seq), func(t *testing.T) {
			rng := rand.New(rand.NewSource(int64(seq)*2654435761 + 7))
			r := pickInt(rng, []int64{0, 1, 50, 500, 999, 1000})
			goldCap := pickInt(rng, []int64{50, 300, 100_000})
			slots := pickInt(rng, []int64{1, 2, 3, 6})
			ttl := pickInt(rng, []int64{30, 500, 100_000})
			maxOpen := pickInt(rng, []int64{1, 2, 4})
			sys1, err := trade.New(r, goldCap, slots, ttl, maxOpen)
			mustOK(t, err)
			sys2, err := trade.New(r, goldCap, slots, ttl, maxOpen)
			mustOK(t, err)
			nv := newNaive(r, goldCap, slots, ttl, maxOpen)
			now := int64(0)
			grantedGold := int64(0)
			grantedItems := map[string]int64{}
			for step := 0; step < 60; step++ {
				switch rng.Intn(20) {
				case 0:
					now -= rng.Int63n(120) // rollback attempt, maybe negative
				case 1, 2:
					// same now
				default:
					now += rng.Int63n(400)
				}
				var e1, e2, en error
				var desc string
				switch op := rng.Intn(100); {
				case op < 16: // grant
					p, item := pickStr(rng, players), pickStr(rng, items)
					qty := int64(rng.Intn(12)) // sometimes 0 -> param error
					e1 = sys1.Grant(p, item, qty)
					e2 = sys2.Grant(p, item, qty)
					en, desc = nv.grant(p, item, qty)
					if e1 == nil {
						grantedItems[item] += qty
					}
					desc = fmt.Sprintf("Grant(%s,%s,%d) -> %s", p, item, qty, desc)
				case op < 26: // grantGold
					p := pickStr(rng, players)
					g := int64(rng.Intn(400))
					e1 = sys1.GrantGold(p, g)
					e2 = sys2.GrantGold(p, g)
					en, desc = nv.grantGold(p, g)
					if e1 == nil {
						grantedGold += g
					}
					desc = fmt.Sprintf("GrantGold(%s,%d) -> %s", p, g, desc)
				case op < 44: // open
					a, b := pickStr(rng, players), pickStr(rng, players)
					if rng.Intn(10) == 0 {
						a = "ghost"
					}
					sid1, err1 := sys1.Open(now, a, b)
					sid2, err2 := sys2.Open(now, a, b)
					sidN, errN, why := nv.open(now, a, b)
					e1, e2, en = err1, err2, errN
					if sid1 != sid2 || sid1 != sidN {
						t.Fatalf("sid mismatch: %d %d %d", sid1, sid2, sidN)
					}
					desc = fmt.Sprintf("Open(%d,%s,%s) -> sid=%d %s", now, a, b, sid1, why)
				case op < 70: // offer
					sid := int64(rng.Intn(int(nv.nextSID) + 3))
					who := pickStr(rng, players)
					if rng.Intn(15) == 0 {
						who = "ghost"
					}
					offerItems := map[string]int64{}
					for _, item := range items {
						if rng.Intn(3) == 0 {
							qty := int64(1 + rng.Intn(10))
							if rng.Intn(20) == 0 {
								qty = 0 // invalid
							}
							offerItems[item] = qty
						}
					}
					gold := int64(rng.Intn(120))
					e1 = sys1.Offer(now, sid, who, offerItems, gold)
					e2 = sys2.Offer(now, sid, who, offerItems, gold)
					en, desc = nv.offer(now, sid, who, offerItems, gold)
					desc = fmt.Sprintf("Offer(%d,s%d,%s,%v,%d) -> %s", now, sid, who, offerItems, gold, desc)
				case op < 90: // confirm
					sid := int64(rng.Intn(int(nv.nextSID) + 3))
					who := pickStr(rng, players)
					ver := int64(rng.Intn(5))
					e1 = sys1.Confirm(now, sid, who, ver)
					e2 = sys2.Confirm(now, sid, who, ver)
					en, desc = nv.confirm(now, sid, who, ver)
					desc = fmt.Sprintf("Confirm(%d,s%d,%s,v%d) -> %s", now, sid, who, ver, desc)
				default: // cancel
					sid := int64(rng.Intn(int(nv.nextSID) + 3))
					who := pickStr(rng, players)
					e1 = sys1.Cancel(now, sid, who)
					e2 = sys2.Cancel(now, sid, who)
					en, desc = nv.cancel(now, sid, who)
					desc = fmt.Sprintf("Cancel(%d,s%d,%s) -> %s", now, sid, who, desc)
				}
				if e1 != en || e2 != en {
					t.Fatalf("step %d %s: sys1=%v sys2=%v naive=%v", step, desc, e1, e2, en)
				}
				t.Logf("step %02d now=%d %s err=%v", step, now, desc, en)
				compareState(t, sys1, nv, players, items)
			}
			// Replay determinism: the second instance must end in the
			// same observable state.
			compareState(t, sys2, nv, players, items)
			// Invariants: locks never exceed holdings; gold (incl.
			// burned) and item totals are conserved.
			var goldSum int64
			for _, p := range players {
				goldSum += nv.gold[p]
				if nv.lockedGold(p, nv.maxNow, -1) > nv.gold[p] {
					t.Fatalf("%s: locked gold exceeds holdings", p)
				}
				for _, item := range items {
					if nv.lockedQty(p, item, nv.maxNow, -1) > nv.qty(p, item) {
						t.Fatalf("%s %s: locked qty exceeds holdings", p, item)
					}
				}
			}
			if goldSum+nv.burned != grantedGold {
				t.Fatalf("gold not conserved: %d+%d != %d", goldSum, nv.burned, grantedGold)
			}
			for _, item := range items {
				var total int64
				for _, p := range players {
					total += nv.qty(p, item)
				}
				if total != grantedItems[item] {
					t.Fatalf("item %s not conserved: %d != %d", item, total, grantedItems[item])
				}
			}
		})
	}
}

// TestConcurrentOps hammers one system from many goroutines (run with
// -race) and then checks the global invariants: locks never exceed
// holdings, and gold plus burned equals the granted total.
func TestConcurrentOps(t *testing.T) {
	players := []string{"p0", "p1", "p2", "p3"}
	items := []string{"i0", "i1", "i2"}
	s, err := trade.New(37, 100_000, 4, 500, 4)
	mustOK(t, err)
	for _, p := range players {
		for _, item := range items {
			mustOK(t, s.Grant(p, item, 100))
		}
		mustOK(t, s.GrantGold(p, 10_000))
	}
	var wg sync.WaitGroup
	for g := 0; g < 8; g++ {
		wg.Add(1)
		go func(seed int64) {
			defer wg.Done()
			rng := rand.New(rand.NewSource(seed))
			now := int64(0)
			for i := 0; i < 300; i++ {
				now += rng.Int63n(50)
				a, b := pickStr(rng, players), pickStr(rng, players)
				sid, err := s.Open(now, a, b)
				if err != nil {
					continue
				}
				offer := map[string]int64{pickStr(rng, items): int64(1 + rng.Intn(5))}
				_ = s.Offer(now, sid, a, offer, int64(rng.Intn(50)))
				_ = s.Offer(now, sid, b, nil, int64(rng.Intn(50)))
				if v, ok := s.Version(sid); ok {
					_ = s.Confirm(now, sid, a, v)
					_ = s.Confirm(now, sid, b, v)
				}
				_ = s.Cancel(now, sid, a)
			}
		}(int64(g) + 1)
	}
	wg.Wait()
	var goldSum int64
	for _, p := range players {
		goldSum += s.Gold(p)
		if s.LockedGold(p) > s.Gold(p) {
			t.Fatalf("%s: locked gold exceeds holdings", p)
		}
		for _, item := range items {
			if s.LockedQty(p, item) > s.Qty(p, item) {
				t.Fatalf("%s %s: locked %d > held %d", p, item, s.LockedQty(p, item), s.Qty(p, item))
			}
		}
	}
	if goldSum+s.Burned() != 40_000 {
		t.Fatalf("gold not conserved: %d+%d != 40000", goldSum, s.Burned())
	}
	for _, item := range items {
		var total int64
		for _, p := range players {
			total += s.Qty(p, item)
		}
		if total != 400 {
			t.Fatalf("item %s not conserved: %d != 400", item, total)
		}
	}
}
