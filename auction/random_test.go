package auction

import (
	"errors"
	"math/rand/v2"
	"testing"
)

type randomConfig struct {
	startPrice int64
	increment  int64
	reserve    int64
	endsAt     int64
	window     int64
	extension  int64
}

type randomBid struct {
	bidder string
	max    int64
	now    int64
}

type naiveBook struct {
	cfg         randomConfig
	price       int64
	leader      string
	leaderMax   int64
	maxBidTime  int64
	seenBidTime bool
	settled     bool
	settlement  Settlement
}

func (n *naiveBook) bid(input randomBid) BidResult {
	rejected := func(reason error) BidResult {
		return BidResult{Price: n.price, Leader: n.leader, EndsAt: n.cfg.endsAt, Reason: reason}
	}

	if input.bidder == "" || input.max < 1 || input.max > maxMoney {
		return rejected(ErrInvalidBid)
	}
	if input.now < 0 || (n.seenBidTime && input.now < n.maxBidTime) {
		return rejected(ErrClockRewound)
	}
	if input.now >= n.cfg.endsAt {
		return rejected(ErrAuctionEnded)
	}

	if n.leader == "" {
		if input.max < n.price {
			return rejected(ErrBidTooLow)
		}

		extended := false
		if n.cfg.endsAt-input.now <= n.cfg.window {
			nextEndsAt := max(n.cfg.endsAt, input.now+n.cfg.extension)
			extended = nextEndsAt != n.cfg.endsAt
			n.cfg.endsAt = nextEndsAt
		}

		n.leader = input.bidder
		n.leaderMax = input.max
		n.maxBidTime = input.now
		n.seenBidTime = true
		return BidResult{Accepted: true, Price: n.price, Leader: n.leader, EndsAt: n.cfg.endsAt, Extended: extended}
	}

	if input.bidder == n.leader {
		if input.max <= n.leaderMax {
			return rejected(ErrProxyLimitNotRaised)
		}
		n.leaderMax = input.max
		n.maxBidTime = input.now
		n.seenBidTime = true
		return BidResult{Accepted: true, Price: n.price, Leader: n.leader, EndsAt: n.cfg.endsAt}
	}

	minimumNextBid := n.price + n.cfg.increment
	if input.max < minimumNextBid {
		return rejected(ErrBidTooLow)
	}

	extended := false
	if n.cfg.endsAt-input.now <= n.cfg.window {
		nextEndsAt := max(n.cfg.endsAt, input.now+n.cfg.extension)
		extended = nextEndsAt != n.cfg.endsAt
		n.cfg.endsAt = nextEndsAt
	}

	if input.max > n.leaderMax {
		n.price = min(input.max, n.leaderMax+n.cfg.increment)
		n.leader = input.bidder
		n.leaderMax = input.max
	} else {
		n.price = min(n.leaderMax, input.max+n.cfg.increment)
	}

	n.maxBidTime = input.now
	n.seenBidTime = true
	return BidResult{Accepted: true, Price: n.price, Leader: n.leader, EndsAt: n.cfg.endsAt, Extended: extended}
}

func (n *naiveBook) settle(now int64) Settlement {
	if n.settled {
		return n.settlement
	}
	if now < n.cfg.endsAt {
		return Settlement{Reason: ErrNotEnded}
	}

	result := Settlement{}
	if n.leader != "" && n.leaderMax >= n.cfg.reserve {
		result = Settlement{Sold: true, Winner: n.leader, Price: max(n.price, n.cfg.reserve)}
	}
	n.settled = true
	n.settlement = result
	return result
}

func TestRandomSequencesAgainstNaiveSimulation(t *testing.T) {
	rng := rand.New(rand.NewPCG(1060, 20261001))
	for i := 0; i < 2000; i++ {
		cfg := randomConfig{
			startPrice: int64(rng.IntN(40) + 1),
			increment:  int64(rng.IntN(8) + 1),
			reserve:    int64(rng.IntN(60)),
			endsAt:     int64(rng.IntN(120) + 20),
			window:     int64(rng.IntN(30)),
			extension:  int64(rng.IntN(30) + 1),
		}

		book, err := NewBook(cfg.startPrice, cfg.increment, cfg.reserve, cfg.endsAt, cfg.window, cfg.extension)
		if err != nil {
			t.Fatalf("case=%d NewBook(%+v) error=%v", i, cfg, err)
		}
		naive := &naiveBook{cfg: cfg, price: cfg.startPrice}
		replay, err := NewBook(cfg.startPrice, cfg.increment, cfg.reserve, cfg.endsAt, cfg.window, cfg.extension)
		if err != nil {
			t.Fatalf("case=%d replay NewBook(%+v) error=%v", i, cfg, err)
		}

		operationCount := rng.IntN(24)
		now := int64(0)
		t.Logf("random case=%d config=%+v basis=all values are valid int64 and generated from fixed seed", i, cfg)

		for op := 0; op < operationCount; op++ {
			if now > 0 && rng.IntN(6) == 0 {
				now--
			} else {
				now += int64(rng.IntN(8))
			}

			bidder := "bidder-" + string(rune('a'+rng.IntN(5)))
			if rng.IntN(10) == 0 {
				bidder = ""
			}

			maxAmount := int64(rng.IntN(int(cfg.startPrice + 80)))
			if rng.IntN(10) == 0 {
				maxAmount = 0
			}
			if rng.IntN(20) == 0 {
				maxAmount = maxMoney + 1
			}

			input := randomBid{bidder: bidder, max: maxAmount, now: now}
			actual := book.Bid(input.bidder, input.max, input.now)
			expected := naive.bid(input)
			replayed := replay.Bid(input.bidder, input.max, input.now)
			basis := comparisonBasis(actual, expected)
			t.Logf("random case=%d Bid input=%+v actual=%+v replay=%+v expected=%+v basis=%s", i, input, actual, replayed, expected, basis)
			assertBidResult(t, actual, expected)
			assertBidResult(t, replayed, expected)

			if actual.Accepted && input.now > now {
				t.Fatal("internal test error: accepted bid timestamp was changed")
			}
			if actual.Accepted {
				now = input.now
			}
		}

		settleAt := naive.cfg.endsAt
		actualSettlement := book.Settle(settleAt)
		expectedSettlement := naive.settle(settleAt)
		replayedSettlement := replay.Settle(settleAt)
		t.Logf("random case=%d Settle input={now:%d} actual=%+v replay=%+v expected=%+v basis=final end reached and reserve checked", i, settleAt, actualSettlement, replayedSettlement, expectedSettlement)
		assertSettlement(t, actualSettlement, expectedSettlement)
		assertSettlement(t, replayedSettlement, expectedSettlement)
	}
}

func comparisonBasis(actual, expected BidResult) string {
	if equalBidResult(actual, expected) {
		return "matches naive step-by-step simulation"
	}
	return "mismatch against naive step-by-step simulation"
}

func equalBidResult(actual, expected BidResult) bool {
	return actual.Accepted == expected.Accepted &&
		actual.Price == expected.Price &&
		actual.Leader == expected.Leader &&
		actual.EndsAt == expected.EndsAt &&
		actual.Extended == expected.Extended &&
		errors.Is(actual.Reason, expected.Reason)
}

func assertBidResult(t *testing.T, actual, expected BidResult) {
	t.Helper()
	if !equalBidResult(actual, expected) {
		t.Fatalf("BidResult = %+v, want %+v", actual, expected)
	}
}

func assertSettlement(t *testing.T, actual, expected Settlement) {
	t.Helper()
	if actual.Sold != expected.Sold || actual.Winner != expected.Winner || actual.Price != expected.Price ||
		!errors.Is(actual.Reason, expected.Reason) {
		t.Fatalf("Settlement = %+v, want %+v", actual, expected)
	}
}
