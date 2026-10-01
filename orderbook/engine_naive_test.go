package orderbook

import (
	"errors"
	"sort"
	"testing"
)

type naiveOrder struct {
	id        int64
	side      Side
	price     int64
	remaining int64
	seq       int64
}

type naiveState int

const (
	naiveLive naiveState = iota
	naiveFilled
	naiveCancelled
)

// naiveEngine is a deliberately literal transcription of the spec: every
// operation rescans all orders. The production Engine must be identical.
type naiveEngine struct {
	tick       int64
	seq        int64
	orders     map[int64]*naiveOrder
	states     map[int64]naiveState
	live       map[int64]bool
	submitted  map[int64]int64
	amendDelta map[int64]int64
	traded     map[int64]int64
	cancelled  map[int64]int64
}

func newNaive(tick int64) *naiveEngine {
	return &naiveEngine{
		tick:       tick,
		orders:     map[int64]*naiveOrder{},
		states:     map[int64]naiveState{},
		live:       map[int64]bool{},
		submitted:  map[int64]int64{},
		amendDelta: map[int64]int64{},
		traded:     map[int64]int64{},
		cancelled:  map[int64]int64{},
	}
}

func (n *naiveEngine) submit(o Order) ([]Trade, error) {
	if _, used := n.orders[o.ID]; used {
		return nil, ErrIDReused
	}
	if !o.Side.valid() {
		return nil, ErrInvalidSide
	}
	if o.Price <= 0 || o.Price > MaxValue || o.Price%n.tick != 0 {
		return nil, ErrInvalidPrice
	}
	if o.Qty <= 0 || o.Qty > MaxValue {
		return nil, ErrInvalidQty
	}

	aggr := &naiveOrder{id: o.ID, side: o.Side, price: o.Price, remaining: o.Qty}
	trades := []Trade{}
	for aggr.remaining > 0 {
		var bestPrice int64
		found := false
		for _, p := range n.orders {
			if !n.live[p.id] || p.side == aggr.side || p.remaining == 0 {
				continue
			}
			if aggr.side == Buy && p.price > aggr.price {
				continue
			}
			if aggr.side == Sell && p.price < aggr.price {
				continue
			}
			if !found || (aggr.side == Buy && p.price < bestPrice) ||
				(aggr.side == Sell && p.price > bestPrice) {
				bestPrice = p.price
				found = true
			}
		}
		if !found {
			break
		}
		var passive *naiveOrder
		for _, p := range n.orders {
			if !n.live[p.id] || p.side == aggr.side || p.remaining == 0 || p.price != bestPrice {
				continue
			}
			if passive == nil || p.seq < passive.seq {
				passive = p
			}
		}
		fill := aggr.remaining
		if passive.remaining < fill {
			fill = passive.remaining
		}
		trades = append(trades, Trade{
			PassiveID:   passive.id,
			AggressorID: aggr.id,
			Price:       passive.price,
			Qty:         fill,
		})
		aggr.remaining -= fill
		passive.remaining -= fill
		n.traded[passive.id] += fill
		if passive.remaining == 0 {
			n.live[passive.id] = false
			n.states[passive.id] = naiveFilled
		}
	}

	if aggr.remaining > 0 {
		n.seq++
		aggr.seq = n.seq
		n.states[aggr.id] = naiveLive
		n.live[aggr.id] = true
	} else {
		n.states[aggr.id] = naiveFilled
		n.live[aggr.id] = false
	}
	n.orders[aggr.id] = aggr
	n.submitted[aggr.id] = o.Qty
	n.traded[aggr.id] += o.Qty - aggr.remaining
	return trades, nil
}

func (n *naiveEngine) cancel(id int64) (int64, error) {
	if _, seen := n.orders[id]; !seen {
		return 0, ErrUnknownID
	}
	if !n.live[id] {
		if n.states[id] == naiveFilled {
			return 0, ErrAlreadyFilled
		}
		return 0, ErrAlreadyCancelled
	}
	ord := n.orders[id]
	removed := ord.remaining
	ord.remaining = 0
	n.live[id] = false
	n.states[id] = naiveCancelled
	n.cancelled[id] += removed
	return removed, nil
}

func (n *naiveEngine) amend(id int64, newQty int64) error {
	if _, seen := n.orders[id]; !seen {
		return ErrUnknownID
	}
	if !n.live[id] {
		if n.states[id] == naiveFilled {
			return ErrAlreadyFilled
		}
		return ErrAlreadyCancelled
	}
	if newQty <= 0 || newQty > MaxValue {
		return ErrInvalidQty
	}
	ord := n.orders[id]
	old := ord.remaining
	n.amendDelta[id] += newQty - old
	ord.remaining = newQty
	if newQty > old {
		n.seq++
		ord.seq = n.seq
	}
	return nil
}

func (n *naiveEngine) depth(side Side, k int) []Level {
	type pl struct {
		price int64
		qty   int64
		count int64
	}
	byPrice := map[int64]*pl{}
	for _, ord := range n.orders {
		if !n.live[ord.id] || ord.side != side || ord.remaining == 0 {
			continue
		}
		l := byPrice[ord.price]
		if l == nil {
			l = &pl{price: ord.price}
			byPrice[ord.price] = l
		}
		l.qty += ord.remaining
		l.count++
	}
	levels := make([]pl, 0, len(byPrice))
	for _, l := range byPrice {
		if l.qty > 0 {
			levels = append(levels, *l)
		}
	}
	sort.Slice(levels, func(i, j int) bool {
		if side == Buy {
			return levels[i].price > levels[j].price
		}
		return levels[i].price < levels[j].price
	})
	if k < len(levels) {
		levels = levels[:k]
	}
	out := make([]Level, len(levels))
	for i, l := range levels {
		out[i] = Level{Price: l.price, Qty: l.qty, Count: l.count}
	}
	return out
}

func (n *naiveEngine) checkInvariant(t *testing.T, where string) {
	t.Helper()
	for id, ord := range n.orders {
		lhs := n.submitted[id] + n.amendDelta[id]
		var remaining int64
		if n.live[id] {
			remaining = ord.remaining
		}
		rhs := n.traded[id] + n.cancelled[id] + remaining
		if lhs != rhs {
			t.Fatalf("[%s] quantity invariant broken for %d: %d != traded %d + cancelled %d + remaining %d",
				where, id, lhs, n.traded[id], n.cancelled[id], remaining)
		}
	}
	var bestBid, bestAsk int64
	haveBid, haveAsk := false, false
	for _, ord := range n.orders {
		if !n.live[ord.id] || ord.remaining == 0 {
			continue
		}
		if ord.side == Buy {
			if !haveBid || ord.price > bestBid {
				bestBid, haveBid = ord.price, true
			}
		} else if !haveAsk || ord.price < bestAsk {
			bestAsk, haveAsk = ord.price, true
		}
	}
	if haveBid && haveAsk && bestBid >= bestAsk {
		t.Fatalf("[%s] crossed book: best bid %d >= best ask %d", where, bestBid, bestAsk)
	}
}

func sameErr(a, b error) bool { return errors.Is(a, b) }
