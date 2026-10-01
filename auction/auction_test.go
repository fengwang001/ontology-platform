package auction

import (
	"errors"
	"testing"
)

func mustBook(t *testing.T, startPrice, increment, reserve, endsAt, window, extension int64) *Book {
	t.Helper()
	book, err := NewBook(startPrice, increment, reserve, endsAt, window, extension)
	if err != nil {
		t.Fatalf("NewBook(S=%d,D=%d,R=%d,E=%d,W=%d,X=%d) error = %v", startPrice, increment, reserve, endsAt, window, extension, err)
	}
	return book
}

func requireBid(t *testing.T, book *Book, bidder string, maxAmount, now int64, wantPrice int64, wantLeader string, wantExtended bool) BidResult {
	t.Helper()
	result := book.Bid(bidder, maxAmount, now)
	t.Logf("Bid input={bidder:%q m:%d now:%d} output=%+v basis=%s", bidder, maxAmount, now, result, bidBasis(result))
	if !result.Accepted || result.Price != wantPrice || result.Leader != wantLeader || result.Extended != wantExtended {
		t.Fatalf("Bid() = %+v, want accepted price=%d leader=%q extended=%v", result, wantPrice, wantLeader, wantExtended)
	}
	return result
}

func requireRejectedBid(t *testing.T, book *Book, bidder string, maxAmount, now int64, wantReason error) {
	t.Helper()
	beforePrice, beforeLeader, beforeEndsAt := book.Price(), book.Leader(), book.EndsAt()
	result := book.Bid(bidder, maxAmount, now)
	t.Logf("Bid input={bidder:%q m:%d now:%d} output=%+v basis=%s", bidder, maxAmount, now, result, bidBasis(result))
	if result.Accepted || !errors.Is(result.Reason, wantReason) ||
		result.Price != beforePrice || result.Leader != beforeLeader || result.EndsAt != beforeEndsAt {
		t.Fatalf("Bid() = %+v, want reason=%v and unchanged state P=%d leader=%q E=%d", result, wantReason, beforePrice, beforeLeader, beforeEndsAt)
	}
}

func bidBasis(result BidResult) string {
	if result.Accepted {
		return "accepted by proxy-bidding rules"
	}
	return "rejected: " + result.Reason.Error()
}

func TestFirstBidBoundary(t *testing.T) {
	book := mustBook(t, 100, 10, 0, 100, 100, 110)
	requireBid(t, book, "alice", 100, 0, 100, "alice", true)

	other := mustBook(t, 100, 10, 0, 100, 0, 10)
	requireRejectedBid(t, other, "alice", 99, 0, ErrBidTooLow)
}

func TestChallengerBoundaryAndProxyPrices(t *testing.T) {
	book := mustBook(t, 100, 10, 0, 1000, 0, 10)
	requireBid(t, book, "alice", 150, 0, 100, "alice", false)

	requireRejectedBid(t, book, "bob", 109, 1, ErrBidTooLow)
	requireBid(t, book, "bob", 110, 2, 120, "alice", false)

	book2 := mustBook(t, 100, 10, 0, 1000, 0, 10)
	requireBid(t, book2, "alice", 150, 2, 100, "alice", false)
	requireBid(t, book2, "bob", 155, 3, 155, "bob", false)

	book3 := mustBook(t, 100, 10, 0, 1000, 0, 10)
	requireBid(t, book3, "alice", 150, 4, 100, "alice", false)
	requireBid(t, book3, "bob", 130, 5, 140, "alice", false)

	book4 := mustBook(t, 100, 10, 0, 1000, 0, 10)
	requireBid(t, book4, "alice", 150, 6, 100, "alice", false)
	requireBid(t, book4, "bob", 130, 7, 140, "alice", false)
}

func TestTieKeepsEarlierLeader(t *testing.T) {
	book := mustBook(t, 100, 10, 0, 1000, 0, 10)
	requireBid(t, book, "alice", 150, 0, 100, "alice", false)
	requireBid(t, book, "bob", 150, 1, 150, "alice", false)
	if got := book.Price(); got != 150 {
		t.Fatalf("Price() = %d, want 150", got)
	}
}

func TestLeaderRaisesProxyLimitWithoutExtension(t *testing.T) {
	book := mustBook(t, 100, 10, 0, 100, 5, 20)
	requireBid(t, book, "alice", 100, 95, 100, "alice", true)
	if got := book.EndsAt(); got != 115 {
		t.Fatalf("EndsAt() = %d, want 115", got)
	}
	requireBid(t, book, "alice", 200, 110, 100, "alice", false)
	if got := book.EndsAt(); got != 115 {
		t.Fatalf("EndsAt() = %d, want unchanged 115", got)
	}
	requireRejectedBid(t, book, "alice", 200, 111, ErrProxyLimitNotRaised)
}

func TestSoftCloseBoundaryAndNoShorterEndTime(t *testing.T) {
	atBoundary := mustBook(t, 100, 10, 0, 100, 20, 30)
	requireBid(t, atBoundary, "alice", 100, 80, 100, "alice", true)
	if got := atBoundary.EndsAt(); got != 110 {
		t.Fatalf("EndsAt() = %d, want 110", got)
	}

	outsideWindow := mustBook(t, 100, 10, 0, 100, 20, 10)
	requireBid(t, outsideWindow, "alice", 100, 79, 100, "alice", false)
	if got := outsideWindow.EndsAt(); got != 100 {
		t.Fatalf("EndsAt() = %d, want 100", got)
	}

	noShorter := mustBook(t, 100, 10, 0, 100, 100, 100)
	requireBid(t, noShorter, "alice", 100, 20, 100, "alice", true)
	if got := noShorter.EndsAt(); got != 120 {
		t.Fatalf("EndsAt() = %d, want 120", got)
	}
	requireBid(t, noShorter, "bob", 110, 25, 110, "bob", true)
	if got := noShorter.EndsAt(); got != 125 {
		t.Fatalf("EndsAt() = %d, want max(120, 125)=125", got)
	}

	notShortened := mustBook(t, 100, 10, 0, 100, 100, 10)
	requireBid(t, notShortened, "alice", 100, 85, 100, "alice", false)
	if got := notShortened.EndsAt(); got != 100 {
		t.Fatalf("EndsAt() = %d, want unchanged max(100, 95)=100", got)
	}
}

func TestBidAtEndRejected(t *testing.T) {
	book := mustBook(t, 100, 10, 0, 100, 0, 10)
	requireRejectedBid(t, book, "alice", 100, 100, ErrAuctionEnded)
}

func TestRejectionReasonsAndUnchangedState(t *testing.T) {
	book := mustBook(t, 100, 10, 0, 1000, 0, 10)
	requireBid(t, book, "alice", 100, 50, 100, "alice", false)

	requireRejectedBid(t, book, "", 100, 51, ErrInvalidBid)
	requireRejectedBid(t, book, "bob", 0, 51, ErrInvalidBid)
	requireRejectedBid(t, book, "bob", maxMoney+1, 51, ErrInvalidBid)
	requireRejectedBid(t, book, "bob", 200, 49, ErrClockRewound)
	requireRejectedBid(t, book, "bob", 100, 51, ErrBidTooLow)
	requireBid(t, book, "bob", 110, 51, 110, "bob", false)
}

func TestSettlementReserveRules(t *testing.T) {
	tests := []struct {
		name      string
		maxAmount int64
		reserve   int64
		wantSold  bool
		wantPrice int64
	}{
		{name: "leader max equals reserve", maxAmount: 120, reserve: 120, wantSold: true, wantPrice: 120},
		{name: "leader max below reserve", maxAmount: 119, reserve: 120},
		{name: "reserve raises final price", maxAmount: 120, reserve: 115, wantSold: true, wantPrice: 115},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			book := mustBook(t, 100, 10, tt.reserve, 100, 0, 10)
			requireBid(t, book, "alice", tt.maxAmount, 0, 100, "alice", false)
			result := settleAndLog(t, book, 100)
			if result.Sold != tt.wantSold || result.Price != tt.wantPrice {
				t.Fatalf("Settle() = %+v, want sold=%v price=%d", result, tt.wantSold, tt.wantPrice)
			}
			if tt.wantSold && result.Winner != "alice" {
				t.Fatalf("Settle() winner = %q, want alice", result.Winner)
			}
		})
	}

	noBid := mustBook(t, 100, 10, 0, 100, 0, 10)
	if result := settleAndLog(t, noBid, 100); result.Sold {
		t.Fatalf("Settle() = %+v, want no sale", result)
	}

	sold := mustBook(t, 100, 10, 0, 100, 0, 10)
	requireBid(t, sold, "alice", 100, 0, 100, "alice", false)
	first := settleAndLog(t, sold, 100)
	later := settleAndLog(t, sold, maxInt64)
	if first != later {
		t.Fatalf("later Settle() = %+v, want frozen %+v", later, first)
	}
	requireRejectedBid(t, sold, "bob", 200, 101, ErrAuctionEnded)
}

func settleAndLog(t *testing.T, book *Book, now int64) Settlement {
	t.Helper()
	result := book.Settle(now)
	t.Logf("Settle input={now:%d} output=%+v basis=end reached and reserve checked", now, result)
	return result
}

func TestInvalidConfigs(t *testing.T) {
	cases := [][]int64{
		{0, 10, 0, 100, 0, 1},
		{100, 0, 0, 100, 0, 1},
		{100, 10, -1, 100, 0, 1},
		{100, 10, 0, 0, 0, 1},
		{100, 10, 0, 100, -1, 1},
		{100, 10, 0, 100, 0, 0},
		{maxMoney + 1, 10, 0, 100, 0, 1},
		{100, maxMoney + 1, 0, 100, 0, 1},
		{100, 10, maxMoney + 1, 100, 0, 1},
	}
	for index, args := range cases {
		if _, err := NewBook(args[0], args[1], args[2], args[3], args[4], args[5]); !errors.Is(err, ErrInvalidConfig) {
			t.Fatalf("case %d NewBook(%v) error = %v, want %v", index, args, err, ErrInvalidConfig)
		}
	}
}

func TestSoftCloseAtInt64Boundary(t *testing.T) {
	book := mustBook(t, 1, 1, 0, maxInt64-1, 100, maxMoney)
	result := book.Bid("alice", 1, maxInt64-2)
	if !result.Accepted || result.EndsAt != maxInt64 {
		t.Fatalf("Bid() = %+v, want EndsAt=%d", result, maxInt64)
	}
}
