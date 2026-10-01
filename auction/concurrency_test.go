package auction

import (
	"strconv"
	"testing"
)

func TestConcurrentBids(t *testing.T) {
	book := mustBook(t, 100, 10, 0, 1000, 0, 10)

	t.Log("concurrency input=40 concurrent non-leader bids at now=0 output=one serializable final state under the mutex")
	results := make(chan BidResult, 40)
	start := make(chan struct{})
	done := make(chan struct{})

	for i := 0; i < 40; i++ {
		go func(i int) {
			<-start
			results <- book.Bid("bidder-"+strconv.Itoa(i), 1000, 0)
			done <- struct{}{}
		}(i)
	}

	close(start)
	for i := 0; i < 40; i++ {
		<-done
	}
	close(results)

	accepted := 0
	for result := range results {
		if result.Accepted {
			accepted++
			continue
		}
		if result.Reason != ErrBidTooLow {
			t.Fatalf("Bid() rejected with %+v, want ErrBidTooLow", result)
		}
	}
	if accepted != 2 {
		t.Fatalf("accepted bids = %d, want 2", accepted)
	}
	if book.Price() != 1000 || book.EndsAt() != 1000 || book.Leader() == "" {
		t.Fatalf("final P=%d leader=%q E=%d, want P=1000 nonempty leader E=1000", book.Price(), book.Leader(), book.EndsAt())
	}
}

func TestConcurrentSettlement(t *testing.T) {
	book := mustBook(t, 100, 10, 130, 100, 0, 10)
	requireBid(t, book, "alice", 140, 0, 100, "alice", false)

	early := book.Settle(50)
	if early.Reason != ErrNotEnded || book.Settled() {
		t.Fatalf("early Settle() = %+v, settled=%v", early, book.Settled())
	}

	const calls = 64
	results := make(chan Settlement, calls)
	start := make(chan struct{})
	done := make(chan struct{})

	t.Log("concurrency input=64 concurrent Settle(100) output=one immutable settlement")
	for i := 0; i < calls; i++ {
		go func() {
			<-start
			results <- book.Settle(100)
			done <- struct{}{}
		}()
	}

	close(start)
	for i := 0; i < calls; i++ {
		<-done
	}
	close(results)

	for result := range results {
		if !result.Sold || result.Winner != "alice" || result.Price != 130 {
			t.Fatalf("Settle() = %+v, want sold to alice at 130", result)
		}
	}
	again := book.Settle(1 << 62)
	if !again.Sold || again.Winner != "alice" || again.Price != 130 {
		t.Fatalf("later Settle() = %+v, want sold to alice at 130", again)
	}
}
