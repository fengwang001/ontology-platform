package gsp

import (
	"fmt"
	"math/rand/v2"
	"slices"
	"strings"
	"testing"
)

type oracleBidder struct {
	id      string
	bid     int64
	quality int64
	budget  int64
	held    int64
	charged int64
	serial  int64
	missed  int64
}

type oracleWinner struct {
	id    string
	price int64
}

type oracleAuction struct {
	winners []oracleWinner
	settled bool
}

type oracleEngine struct {
	bidders  map[string]*oracleBidder
	auctions map[int64]*oracleAuction
	serial   int64
	id       int64
}

func newOracleEngine() *oracleEngine {
	return &oracleEngine{
		bidders:  make(map[string]*oracleBidder),
		auctions: make(map[int64]*oracleAuction),
	}
}

func (o *oracleEngine) register(id string, bid, quality, budget int64) error {
	if id == "" || bid < 1 || bid > 1_000_000 || quality < 1 || quality > 1000 || budget < 1 || budget > 1_000_000_000_000 {
		return ErrInvalidArgument
	}
	if _, exists := o.bidders[id]; exists {
		return ErrBidderExists
	}
	o.serial++
	o.bidders[id] = &oracleBidder{id: id, bid: bid, quality: quality, budget: budget, serial: o.serial}
	return nil
}

func (o *oracleEngine) auction(slots, reserve int64) ([]oracleWinner, error) {
	if slots < 1 || slots > 100 || reserve < 1 || reserve > 1_000_000 {
		return nil, ErrInvalidArgument
	}

	type entrant struct {
		bidder *oracleBidder
		score  int64
		q      int64
	}
	eligible := make([]entrant, 0)
	for _, candidate := range o.bidders {
		if candidate.bid >= reserve && candidate.budget-candidate.held >= candidate.bid {
			q := oracleEffectiveQuality(candidate.quality, candidate.missed)
			eligible = append(eligible, entrant{bidder: candidate, score: candidate.bid * q, q: q})
		}
	}
	if len(eligible) == 0 {
		return nil, ErrNoBidders
	}

	slices.SortFunc(eligible, func(left, right entrant) int {
		if left.score != right.score {
			return int(right.score - left.score)
		}
		return int(left.bidder.serial - right.bidder.serial)
	})

	count := int(slots)
	if count > len(eligible) {
		count = len(eligible)
	}
	winners := make([]oracleWinner, 0, count)
	for position := 0; position < count; position++ {
		price := reserve
		if position+1 < len(eligible) {
			price = min(eligible[position].bidder.bid, max(reserve, eligible[position+1].score/eligible[position].q+1))
		}
		winners = append(winners, oracleWinner{id: eligible[position].bidder.id, price: price})
		eligible[position].bidder.held += price
	}

	o.id++
	o.auctions[o.id] = &oracleAuction{winners: winners}
	return winners, nil
}

func (o *oracleEngine) resolve(id int64, clicks []string) error {
	seen := make(map[string]struct{})
	for _, click := range clicks {
		if _, duplicated := seen[click]; duplicated {
			return ErrInvalidArgument
		}
		seen[click] = struct{}{}
	}
	current, exists := o.auctions[id]
	if !exists {
		return ErrAuctionNotFound
	}
	if current.settled {
		return ErrAuctionSettled
	}
	for click := range seen {
		found := false
		for _, winner := range current.winners {
			if winner.id == click {
				found = true
			}
		}
		if !found {
			return ErrNotWinner
		}
	}

	for _, winner := range current.winners {
		candidate := o.bidders[winner.id]
		_, clicked := seen[winner.id]
		candidate.held -= winner.price
		if clicked {
			candidate.budget -= winner.price
			candidate.charged += winner.price
			candidate.missed = 0
			candidate.quality = min(1000, candidate.quality+(1000-candidate.quality)/8)
		} else {
			candidate.missed++
			candidate.quality = max(1, candidate.quality-(candidate.quality+15)/16)
		}
	}
	current.settled = true
	return nil
}

func oracleEffectiveQuality(quality, missed int64) int64 {
	discounted := min(5, max(0, missed-2))
	return max(1, quality*(100-10*discounted)/100)
}

type operationLog struct {
	name   string
	args   string
	result string
	reason string
}

func TestRandomOperationsAgainstOracle(t *testing.T) {
	if !testRandomOperationsAgainstOracle(t) {
		t.Fatal("random oracle comparison failed; inspect logged operation sequence")
	}
}

func testRandomOperationsAgainstOracle(t *testing.T) bool {
	t.Helper()
	const sequences = 2000

	for seed := int64(1); seed <= sequences; seed++ {
		random := rand.New(rand.NewPCG(uint64(seed), 99))
		engine := NewEngine()
		oracle := newOracleEngine()
		logs := make([]operationLog, 0, 48)
		t.Logf("seed=%d input=begin random operation sequence judgment=comparing engine with naive oracle", seed)

		bidderCount := 2 + random.IntN(10)
		for i := 0; i < bidderCount; i++ {
			id := fmt.Sprintf("b%d", i)
			bid := int64(1 + random.IntN(30))
			quality := int64(1 + random.IntN(1000))
			budget := int64(1 + random.IntN(500))
			errEngine := engine.Register(id, bid, quality, budget)
			errOracle := oracle.register(id, bid, quality, budget)
			logs = append(logs, operationLog{"Register", fmt.Sprintf("%s,%d,%d,%d", id, bid, quality, budget), fmt.Sprintf("err=%v", errEngine), fmt.Sprintf("err=%v", errOracle)})
			if !sameError(errEngine, errOracle) {
				logFailure(t, seed, logs, "register errors differ")
				return false
			}
		}

		auctionCount := 0
		for step := 0; step < 35; step++ {
			switch random.IntN(3) {
			case 0:
				id := fmt.Sprintf("b%d", random.IntN(bidderCount))
				bid := int64(1 + random.IntN(32))
				quality := int64(1 + random.IntN(1000))
				budget := int64(1 + random.IntN(600))
				errEngine := engine.Register(id, bid, quality, budget)
				errOracle := oracle.register(id, bid, quality, budget)
				logs = append(logs, operationLog{"Register", fmt.Sprintf("id=%s bid=%d q=%d budget=%d", id, bid, quality, budget), fmt.Sprint(errEngine), fmt.Sprint(errOracle)})
				if !sameError(errEngine, errOracle) {
					logFailure(t, seed, logs, "register errors differ")
					return false
				}
			case 1:
				slots := int64(1 + random.IntN(5))
				reserve := int64(1 + random.IntN(25))
				result, errEngine := engine.Auction(slots, reserve)
				winners, errOracle := oracle.auction(slots, reserve)
				logs = append(logs, operationLog{"Auction", fmt.Sprintf("K=%d P=%d", slots, reserve), fmt.Sprintf("id=%d winners=%v err=%v", result.ID, result.Winners, errEngine), fmt.Sprintf("winners=%v err=%v", winners, errOracle)})
				if !sameError(errEngine, errOracle) || (errEngine == nil && !sameWinners(result.Winners, winners)) {
					logFailure(t, seed, logs, "auction result differs")
					return false
				}
				if errEngine == nil {
					auctionCount++
				}
			case 2:
				if auctionCount == 0 {
					clicks := []string{"missing"}
					errEngine := engine.Resolve(int64(1+random.IntN(3)), clicks)
					errOracle := oracle.resolve(int64(1+random.IntN(3)), clicks)
					logs = append(logs, operationLog{"Resolve", fmt.Sprintf("clicks=%v", clicks), fmt.Sprint(errEngine), fmt.Sprint(errOracle)})
					if !sameError(errEngine, errOracle) {
						logFailure(t, seed, logs, "missing resolve errors differ")
						return false
					}
					continue
				}
				id := int64(1 + random.IntN(auctionCount))
				auction := oracle.auctions[id]
				clicks := []string{}
				if !auction.settled {
					for _, winner := range auction.winners {
						if random.IntN(2) == 0 {
							clicks = append(clicks, winner.id)
						}
					}
					if random.IntN(10) == 0 {
						clicks = append(clicks, "outsider")
					}
					if random.IntN(10) == 0 && len(clicks) > 0 {
						clicks = append(clicks, clicks[0])
					}
				}
				errEngine := engine.Resolve(id, clicks)
				errOracle := oracle.resolve(id, clicks)
				logs = append(logs, operationLog{"Resolve", fmt.Sprintf("id=%d clicks=%v", id, clicks), fmt.Sprint(errEngine), fmt.Sprint(errOracle)})
				if !sameError(errEngine, errOracle) {
					logFailure(t, seed, logs, "resolve errors differ")
					return false
				}
			}

			if !statesAgree(engine, oracle) {
				logFailure(t, seed, logs, "state differs")
				return false
			}
		}
		trace := make([]string, 0, len(logs))
		for _, entry := range logs {
			trace = append(trace, fmt.Sprintf("%s(%s)->%s|oracle:%s", entry.name, entry.args, entry.result, entry.reason))
		}
		t.Logf("seed=%d input_and_output=%s judgment=identical to naive oracle", seed, strings.Join(trace, " ; "))
	}
	return true
}

func sameError(left, right error) bool {
	return left == right
}

func sameWinners(left []Winner, right []oracleWinner) bool {
	if len(left) != len(right) {
		return false
	}
	for i := range left {
		if left[i].ID != right[i].id || left[i].Price != right[i].price {
			return false
		}
	}
	return true
}

func statesAgree(engine *Engine, oracle *oracleEngine) bool {
	if len(engine.bidders) != len(oracle.bidders) || len(engine.auctions) != len(oracle.auctions) {
		return false
	}
	for id, expected := range oracle.bidders {
		actual := engine.bidders[id]
		if actual == nil || actual.bid != expected.bid || actual.quality != expected.quality || actual.budget != expected.budget || actual.held != expected.held || actual.chargedTotal != expected.charged || actual.serial != expected.serial || actual.missedClicks != expected.missed {
			return false
		}
	}
	for id, expected := range oracle.auctions {
		actual := engine.auctions[id]
		if actual == nil || actual.settled != expected.settled || len(actual.winners) != len(expected.winners) {
			return false
		}
		for i := range expected.winners {
			if actual.winners[i].id != expected.winners[i].id || actual.winners[i].price != expected.winners[i].price {
				return false
			}
		}
	}
	return true
}

func logFailure(t *testing.T, seed int64, logs []operationLog, reason string) {
	t.Helper()
	var builder strings.Builder
	fmt.Fprintf(&builder, "seed=%d judgment=%s\n", seed, reason)
	for _, log := range logs {
		fmt.Fprintf(&builder, "input: %s(%s)\nactual output: %s\noracle basis: %s\n", log.name, log.args, log.result, log.reason)
	}
	t.Log(builder.String())
}
