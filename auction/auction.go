// Package auction implements an English auction book with proxy bidding
// (automatic bidding up to a declared maximum) and a soft-close
// deadline extension.
package auction

import (
	"errors"
	"sync"
)

// MaxAmount is the largest legal value for prices, increments and bids.
const MaxAmount = int64(1_000_000_000_000_000)

var (
	// ErrInvalidConfig is returned by NewAuction when the configuration
	// is out of range.
	ErrInvalidConfig = errors.New("auction: invalid config")
	// ErrInvalidBidParam: empty bidder, or m out of [1, MaxAmount].
	ErrInvalidBidParam = errors.New("auction: invalid bid parameter")
	// ErrClockBackward: now is negative or below the max now of accepted bids.
	ErrClockBackward = errors.New("auction: clock moved backward")
	// ErrAuctionEnded: now >= end time, or the auction is already settled.
	ErrAuctionEnded = errors.New("auction: auction already ended")
	// ErrBelowThreshold: m below start price (no leader) or below P+D.
	ErrBelowThreshold = errors.New("auction: bid below required threshold")
	// ErrNotRaisingMax: the leader's bid does not strictly raise the proxy max.
	ErrNotRaisingMax = errors.New("auction: leading bid does not raise proxy max")
	// ErrNotEnded is returned by Settle when now is before the end time.
	ErrNotEnded = errors.New("auction: not yet ended")
)

// Config holds the auction parameters. All amounts and times are int64.
type Config struct {
	StartPrice   int64 // S: minimum first price
	MinIncrement int64 // D: minimum increment
	ReservePrice int64 // R: reserve price, 0 means no reserve
	EndTime      int64 // E: initial end time
	ExtendWindow int64 // W: soft-close window
	ExtendBy     int64 // X: soft-close extension amount
}

func (c Config) valid() bool {
	if c.StartPrice < 1 || c.MinIncrement < 1 || c.ReservePrice < 0 ||
		c.EndTime < 1 || c.ExtendWindow < 0 || c.ExtendBy < 1 {
		return false
	}
	if c.StartPrice > MaxAmount || c.MinIncrement > MaxAmount || c.ReservePrice > MaxAmount {
		return false
	}
	return true
}

// Result is the outcome of a settled auction.
type Result struct {
	Sold   bool
	Winner string
	Price  int64
}

// Snapshot is a read-only view of the public auction state.
// The proxy maximum M is intentionally not exposed.
type Snapshot struct {
	Price   int64
	Leader  string
	EndTime int64
	Settled bool
	Result  Result
}

// Auction is a concurrency-safe English auction book. All methods may be
// called concurrently; the result is equivalent to some serial order.
type Auction struct {
	mu         sync.Mutex
	cfg        Config
	price      int64  // P: current displayed price
	leader     string // current leading bidder
	hasLeader  bool
	proxyMax   int64 // M: leader's proxy maximum (never exposed)
	endTime    int64 // E: current end time
	maxSeenNow int64 // max now over accepted bids
	settled    bool
	result     Result
}

// NewAuction validates cfg and returns a new auction book.
func NewAuction(cfg Config) (*Auction, error) {
	if !cfg.valid() {
		return nil, ErrInvalidConfig
	}
	return &Auction{cfg: cfg, endTime: cfg.EndTime}, nil
}

// Bid places a proxy bid: m is the bidder's maximum willingness to pay.
// Rejections are checked in a fixed order and only the first reason is
// reported: ErrInvalidBidParam, ErrClockBackward, ErrAuctionEnded,
// ErrBelowThreshold, ErrNotRaisingMax. A rejected bid changes nothing.
func (a *Auction) Bid(bidder string, m, now int64) error {
	a.mu.Lock()
	defer a.mu.Unlock()

	if bidder == "" || m < 1 || m > MaxAmount {
		return ErrInvalidBidParam
	}
	if now < 0 || now < a.maxSeenNow {
		return ErrClockBackward
	}
	if now >= a.endTime || a.settled {
		return ErrAuctionEnded
	}

	if !a.hasLeader {
		if m < a.cfg.StartPrice {
			return ErrBelowThreshold
		}
		a.price = a.cfg.StartPrice
		a.leader = bidder
		a.hasLeader = true
		a.proxyMax = m
		a.extend(now)
		a.maxSeenNow = now
		return nil
	}

	if bidder == a.leader {
		if m <= a.proxyMax {
			return ErrNotRaisingMax
		}
		a.proxyMax = m
		a.maxSeenNow = now
		return nil
	}

	if m < a.price+a.cfg.MinIncrement {
		return ErrBelowThreshold
	}
	if m > a.proxyMax {
		a.price = min(m, a.proxyMax+a.cfg.MinIncrement)
		a.leader = bidder
		a.proxyMax = m
	} else {
		a.price = min(a.proxyMax, m+a.cfg.MinIncrement)
	}
	a.extend(now)
	a.maxSeenNow = now
	return nil
}

// extend applies the soft-close rule. It is only invoked for accepted
// bids from non-leaders (including the very first bid).
func (a *Auction) extend(now int64) {
	if a.endTime-now <= a.cfg.ExtendWindow {
		if t := now + a.cfg.ExtendBy; t > a.endTime {
			a.endTime = t
		}
	}
}

// Settle closes the auction. Before the end time it returns ErrNotEnded.
// Once a result is produced it never changes: concurrent and repeated
// calls all observe the same single result.
func (a *Auction) Settle(now int64) (Result, error) {
	a.mu.Lock()
	defer a.mu.Unlock()

	if a.settled {
		return a.result, nil
	}
	if now < a.endTime {
		return Result{}, ErrNotEnded
	}
	a.settled = true
	if !a.hasLeader || a.proxyMax < a.cfg.ReservePrice {
		a.result = Result{Sold: false}
	} else {
		a.result = Result{
			Sold:   true,
			Winner: a.leader,
			Price:  max(a.price, a.cfg.ReservePrice),
		}
	}
	return a.result, nil
}

// Snapshot returns the current public state.
func (a *Auction) Snapshot() Snapshot {
	a.mu.Lock()
	defer a.mu.Unlock()
	return Snapshot{
		Price:   a.price,
		Leader:  a.leader,
		EndTime: a.endTime,
		Settled: a.settled,
		Result:  a.result,
	}
}
