package auction

import (
	"errors"
	"sync"
)

const maxMoney int64 = 1_000_000_000_000_000
const maxInt64 = int64(^uint64(0) >> 1)

var (
	ErrInvalidConfig       = errors.New("invalid auction configuration")
	ErrInvalidBid          = errors.New("invalid bid")
	ErrClockRewound        = errors.New("clock rewound")
	ErrAuctionEnded        = errors.New("auction ended")
	ErrBidTooLow           = errors.New("bid too low")
	ErrProxyLimitNotRaised = errors.New("proxy limit not raised")
	ErrNotEnded            = errors.New("auction has not ended")
)

type BidResult struct {
	Accepted bool
	Price    int64
	Leader   string
	EndsAt   int64
	Extended bool
	Reason   error
}

type Settlement struct {
	Sold   bool
	Winner string
	Price  int64
	Reason error
}

type Book struct {
	mu                sync.RWMutex
	increment         int64
	reserve           int64
	endsAt            int64
	extensionWindow   int64
	extensionDuration int64

	price      int64
	leader     string
	leaderMax  int64
	hasBidTime bool
	maxBidTime int64
	settled    bool
	settlement Settlement
}

func NewBook(startPrice, increment, reserve, endsAt, extensionWindow, extensionDuration int64) (*Book, error) {
	if startPrice < 1 || increment < 1 || reserve < 0 ||
		endsAt < 1 || extensionWindow < 0 || extensionDuration < 1 ||
		startPrice > maxMoney || increment > maxMoney || reserve > maxMoney {
		return nil, ErrInvalidConfig
	}

	return &Book{
		increment:         increment,
		reserve:           reserve,
		endsAt:            endsAt,
		extensionWindow:   extensionWindow,
		extensionDuration: extensionDuration,
		price:             startPrice,
	}, nil
}

func (b *Book) Bid(bidder string, maxAmount, now int64) BidResult {
	b.mu.Lock()
	defer b.mu.Unlock()

	rejected := func(reason error) BidResult {
		return BidResult{
			Price:  b.price,
			Leader: b.leader,
			EndsAt: b.endsAt,
			Reason: reason,
		}
	}

	if bidder == "" || maxAmount < 1 || maxAmount > maxMoney {
		return rejected(ErrInvalidBid)
	}
	if now < 0 || (b.hasBidTime && now < b.maxBidTime) {
		return rejected(ErrClockRewound)
	}
	if b.settled || now >= b.endsAt {
		return rejected(ErrAuctionEnded)
	}

	if b.leader == "" {
		if maxAmount < b.price {
			return rejected(ErrBidTooLow)
		}

		extended := b.extendIfNeeded(now)

		b.leader = bidder
		b.leaderMax = maxAmount
		b.maxBidTime = now
		b.hasBidTime = true
		return BidResult{
			Accepted: true,
			Price:    b.price,
			Leader:   b.leader,
			EndsAt:   b.endsAt,
			Extended: extended,
		}
	}

	if bidder == b.leader {
		if maxAmount <= b.leaderMax {
			return rejected(ErrProxyLimitNotRaised)
		}

		b.leaderMax = maxAmount
		b.maxBidTime = now
		b.hasBidTime = true
		return BidResult{
			Accepted: true,
			Price:    b.price,
			Leader:   b.leader,
			EndsAt:   b.endsAt,
		}
	}

	minimumNextBid := b.price + b.increment
	if maxAmount < minimumNextBid {
		return rejected(ErrBidTooLow)
	}

	extended := b.extendIfNeeded(now)

	if maxAmount > b.leaderMax {
		b.price = min(maxAmount, b.leaderMax+b.increment)
		b.leader = bidder
		b.leaderMax = maxAmount
	} else {
		b.price = min(b.leaderMax, maxAmount+b.increment)
	}

	b.maxBidTime = now
	b.hasBidTime = true
	return BidResult{
		Accepted: true,
		Price:    b.price,
		Leader:   b.leader,
		EndsAt:   b.endsAt,
		Extended: extended,
	}
}

func (b *Book) Settle(now int64) Settlement {
	b.mu.Lock()
	defer b.mu.Unlock()

	if b.settled {
		return b.settlement
	}
	if now < b.endsAt {
		return Settlement{Reason: ErrNotEnded}
	}

	result := Settlement{}
	if b.leader != "" && b.leaderMax >= b.reserve {
		result = Settlement{
			Sold:   true,
			Winner: b.leader,
			Price:  max(b.price, b.reserve),
		}
	}

	b.settled = true
	b.settlement = result
	return result
}

func (b *Book) Price() int64 {
	b.mu.RLock()
	defer b.mu.RUnlock()
	return b.price
}

func (b *Book) Leader() string {
	b.mu.RLock()
	defer b.mu.RUnlock()
	return b.leader
}

func (b *Book) EndsAt() int64 {
	b.mu.RLock()
	defer b.mu.RUnlock()
	return b.endsAt
}

func (b *Book) Settled() bool {
	b.mu.RLock()
	defer b.mu.RUnlock()
	return b.settled
}

func saturatingAdd(left, right int64) int64 {
	if left > maxInt64-right {
		return maxInt64
	}
	return left + right
}

func (b *Book) extendIfNeeded(now int64) bool {
	if b.endsAt-now > b.extensionWindow {
		return false
	}
	newEndsAt := max(b.endsAt, saturatingAdd(now, b.extensionDuration))
	changed := newEndsAt != b.endsAt
	b.endsAt = newEndsAt
	return changed
}
