package gsp

import (
	"errors"
	"sync"
)

var (
	ErrInvalidArgument = errors.New("invalid argument")
	ErrBidderExists    = errors.New("bidder already exists")
	ErrAuctionNotFound = errors.New("auction not found")
	ErrAuctionSettled  = errors.New("auction already settled")
	ErrNotWinner       = errors.New("click is not a winner")
	ErrNoBidders       = errors.New("no eligible bidders")
)

type Winner struct {
	ID    string
	Price int64
}

type AuctionResult struct {
	ID      int64
	Winners []Winner
}

type BidderState struct {
	ID           string
	Bid          int64
	Quality      int64
	Budget       int64
	Held         int64
	ChargedTotal int64
	Serial       int64
	MissedClicks int64
}

type AuctionState struct {
	ID      int64
	Settled bool
	Winners []Winner
}

type Engine struct {
	mu       sync.Mutex
	bidders  map[string]*bidder
	auctions map[int64]*auction

	nextBidderSerial int64
	nextAuctionID    int64

	lastSortComparisons int
}

type bidder struct {
	id           string
	bid          int64
	quality      int64
	budget       int64
	held         int64
	chargedTotal int64
	serial       int64
	missedClicks int64
}

type auction struct {
	id      int64
	settled bool
	winners []winnerRecord
}

type participant struct {
	bidder     *bidder
	score      int64
	effectiveQ int64
}

type winnerRecord struct {
	bidder  *bidder
	id      string
	price   int64
	frozenQ int64
}

func NewEngine() *Engine {
	return &Engine{
		bidders:  make(map[string]*bidder),
		auctions: make(map[int64]*auction),
	}
}

func (e *Engine) Register(id string, bid, quality, budget int64) error {
	if id == "" || bid < 1 || bid > 1_000_000 || quality < 1 || quality > 1000 || budget < 1 || budget > 1_000_000_000_000 {
		return ErrInvalidArgument
	}

	e.mu.Lock()
	defer e.mu.Unlock()

	if _, exists := e.bidders[id]; exists {
		return ErrBidderExists
	}

	e.nextBidderSerial++
	e.bidders[id] = &bidder{
		id:      id,
		bid:     bid,
		quality: quality,
		budget:  budget,
		serial:  e.nextBidderSerial,
	}
	return nil
}

func (e *Engine) Auction(slots int64, reserve int64) (AuctionResult, error) {
	if slots < 1 || slots > 100 || reserve < 1 || reserve > 1_000_000 {
		return AuctionResult{}, ErrInvalidArgument
	}

	e.mu.Lock()
	defer e.mu.Unlock()

	eligible := make([]participant, 0)
	for _, entrant := range e.bidders {
		available := entrant.budget - entrant.held
		if entrant.bid >= reserve && available >= entrant.bid {
			effectiveQ := effectiveQuality(entrant.quality, entrant.missedClicks)
			eligible = append(eligible, participant{
				bidder:     entrant,
				score:      entrant.bid * effectiveQ,
				effectiveQ: effectiveQ,
			})
		}
	}

	if len(eligible) == 0 {
		e.lastSortComparisons = 0
		return AuctionResult{}, ErrNoBidders
	}

	e.lastSortComparisons = 0
	stableSortParticipants(eligible, &e.lastSortComparisons)

	winnerCount := int(slots)
	if winnerCount > len(eligible) {
		winnerCount = len(eligible)
	}

	records := make([]winnerRecord, 0, winnerCount)
	resultWinners := make([]Winner, 0, winnerCount)
	for position := 0; position < winnerCount; position++ {
		current := eligible[position]
		price := reserve
		if position+1 < len(eligible) {
			candidate := eligible[position+1].score/current.effectiveQ + 1
			if candidate < reserve {
				candidate = reserve
			}
			if candidate > current.bidder.bid {
				candidate = current.bidder.bid
			}
			price = candidate
		}

		record := winnerRecord{
			bidder:  current.bidder,
			id:      current.bidder.id,
			price:   price,
			frozenQ: current.effectiveQ,
		}
		records = append(records, record)
		resultWinners = append(resultWinners, Winner{ID: record.id, Price: record.price})
		current.bidder.held += price
	}

	e.nextAuctionID++
	recordedAuction := &auction{id: e.nextAuctionID, winners: records}
	e.auctions[recordedAuction.id] = recordedAuction

	return AuctionResult{ID: recordedAuction.id, Winners: resultWinners}, nil
}

func (e *Engine) Resolve(auctionID int64, clicks []string) error {
	e.mu.Lock()
	defer e.mu.Unlock()

	seenClicks := make(map[string]struct{}, len(clicks))
	for _, clickID := range clicks {
		if _, duplicated := seenClicks[clickID]; duplicated {
			return ErrInvalidArgument
		}
		seenClicks[clickID] = struct{}{}
	}

	recordedAuction, exists := e.auctions[auctionID]
	if !exists {
		return ErrAuctionNotFound
	}
	if recordedAuction.settled {
		return ErrAuctionSettled
	}

	winnerIDs := make(map[string]struct{}, len(recordedAuction.winners))
	for _, winner := range recordedAuction.winners {
		winnerIDs[winner.id] = struct{}{}
	}
	for clickID := range seenClicks {
		if _, isWinner := winnerIDs[clickID]; !isWinner {
			return ErrNotWinner
		}
	}

	for _, winner := range recordedAuction.winners {
		_, clicked := seenClicks[winner.id]
		winner.bidder.held -= winner.price
		if clicked {
			winner.bidder.budget -= winner.price
			winner.bidder.chargedTotal += winner.price
			winner.bidder.missedClicks = 0
			winner.bidder.quality += (1000 - winner.bidder.quality) / 8
		} else {
			winner.bidder.missedClicks++
			winner.bidder.quality = max(1, winner.bidder.quality-ceilDiv(winner.bidder.quality, 16))
		}
	}
	recordedAuction.settled = true

	return nil
}

func ceilDiv(value, divisor int64) int64 {
	return (value + divisor - 1) / divisor
}

func effectiveQuality(quality, missedClicks int64) int64 {
	discountedWins := missedClicks - 2
	if discountedWins < 0 {
		discountedWins = 0
	}
	if discountedWins > 5 {
		discountedWins = 5
	}
	effective := quality * (100 - 10*discountedWins) / 100
	if effective < 1 {
		return 1
	}
	return effective
}

func (e *Engine) BidderStates() []BidderState {
	e.mu.Lock()
	defer e.mu.Unlock()

	states := make([]BidderState, 0, len(e.bidders))
	for _, entrant := range e.bidders {
		states = append(states, BidderState{
			ID:           entrant.id,
			Bid:          entrant.bid,
			Quality:      entrant.quality,
			Budget:       entrant.budget,
			Held:         entrant.held,
			ChargedTotal: entrant.chargedTotal,
			Serial:       entrant.serial,
			MissedClicks: entrant.missedClicks,
		})
	}
	sortBidderStates(states)
	return states
}

func (e *Engine) AuctionStates() []AuctionState {
	e.mu.Lock()
	defer e.mu.Unlock()

	ids := make([]int64, 0, len(e.auctions))
	for id := range e.auctions {
		ids = append(ids, id)
	}
	sortInt64s(ids)

	states := make([]AuctionState, 0, len(ids))
	for _, id := range ids {
		recordedAuction := e.auctions[id]
		winners := make([]Winner, 0, len(recordedAuction.winners))
		for _, winner := range recordedAuction.winners {
			winners = append(winners, Winner{ID: winner.id, Price: winner.price})
		}
		states = append(states, AuctionState{ID: id, Settled: recordedAuction.settled, Winners: winners})
	}
	return states
}

func (e *Engine) SortComparisons() int {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.lastSortComparisons
}

func stableSortParticipants(values []participant, comparisons *int) {
	if len(values) < 2 {
		return
	}
	source := values
	target := make([]participant, len(values))
	for width := 1; width < len(values); width *= 2 {
		for start := 0; start < len(values); start += 2 * width {
			middle := start + width
			end := start + 2*width
			if middle > len(values) {
				middle = len(values)
			}
			if end > len(values) {
				end = len(values)
			}

			left := start
			right := middle
			output := start
			for left < middle || right < end {
				if right == end || (left < middle && participantLess(source[left], source[right], comparisons)) {
					target[output] = source[left]
					left++
				} else {
					target[output] = source[right]
					right++
				}
				output++
			}
			for remaining := left; remaining < middle; remaining++ {
				target[output] = source[remaining]
				output++
			}
			for remaining := right; remaining < end; remaining++ {
				target[output] = source[remaining]
				output++
			}
		}
		source, target = target, source
	}
	copy(values, source)
}

func participantLess(left, right participant, comparisons *int) bool {
	*comparisons++
	if left.score != right.score {
		return left.score > right.score
	}
	return left.bidder.serial < right.bidder.serial
}

func sortBidderStates(values []BidderState) {
	for i := 1; i < len(values); i++ {
		for j := i; j > 0 && values[j-1].Serial > values[j].Serial; j-- {
			values[j-1], values[j] = values[j], values[j-1]
		}
	}
}

func sortInt64s(values []int64) {
	for i := 1; i < len(values); i++ {
		for j := i; j > 0 && values[j-1] > values[j]; j-- {
			values[j-1], values[j] = values[j], values[j-1]
		}
	}
}
