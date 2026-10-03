package spotmarket

import (
	"errors"
	"fmt"
	"math/big"
	"sort"
	"strings"
	"sync"
	"testing"
)

func eventsString(evs []Event) string {
	parts := make([]string, len(evs))
	for i, e := range evs {
		parts[i] = e.String()
	}
	return "[" + strings.Join(parts, ", ") + "]"
}

func mustNew(t *testing.T, k, pmin int64) *Market {
	t.Helper()
	m, err := New(k, pmin)
	if err != nil {
		t.Fatalf("New(%d, %d) failed: %v", k, pmin, err)
	}
	return m
}

func checkEvents(t *testing.T, what string, got []Event, err error, want string) {
	t.Helper()
	if err != nil {
		t.Fatalf("%s: unexpected error: %v", what, err)
	}
	if s := eventsString(got); s != want {
		t.Fatalf("%s: events = %s, want %s", what, s, want)
	}
}

func checkErr(t *testing.T, what string, err error, want error) {
	t.Helper()
	if !errors.Is(err, want) {
		t.Fatalf("%s: err = %v, want %v", what, err, want)
	}
}

func checkBill(t *testing.T, m *Market, id int64, want int64) {
	t.Helper()
	if got := m.Bill(id); got.Cmp(big.NewInt(want)) != 0 {
		t.Fatalf("Bill(%d) = %s, want %d", id, got, want)
	}
}

func checkRunning(t *testing.T, m *Market, want ...int64) {
	t.Helper()
	got := m.RunningIDs()
	if len(got) != len(want) {
		t.Fatalf("RunningIDs = %v, want %v", got, want)
	}
	for i := range got {
		if got[i] != want[i] {
			t.Fatalf("RunningIDs = %v, want %v", got, want)
		}
	}
}

func checkPrice(t *testing.T, m *Market, x, want int64) {
	t.Helper()
	if got := m.PriceAt(x); got != want {
		t.Fatalf("PriceAt(%d) = %d, want %d", x, got, want)
	}
}

// TestWorkedExample replays the scenario from the specification.
func TestWorkedExample(t *testing.T) {
	m := mustNew(t, 2, 10)

	evs, err := m.Request(1, 50, 0) // a
	checkEvents(t, "Request(a,50,0)", evs, err, "[Start#1@0]")
	checkPrice(t, m, 0, 10)

	evs, err = m.Request(2, 40, 0) // b
	checkEvents(t, "Request(b,40,0)", evs, err, "[Start#2@0]")
	checkPrice(t, m, 0, 10)

	// Ranking a(50), c(45), b(40): b is evicted with a free partial hour.
	evs, err = m.Request(3, 45, 1800) // c
	checkEvents(t, "Request(c,45,1800)", evs, err,
		"[End#2 market-interrupted fee=0 [0,1800), Start#3@1800]")
	checkPrice(t, m, 1799, 10)
	checkPrice(t, m, 1800, 40)

	// a ends by user: ceil(3700/3600)=2 hours, price(0)=10, price(3600)=40.
	evs, err = m.Terminate(1, 3700)
	checkEvents(t, "Terminate(a,3700)", evs, err,
		"[End#1 user-terminated fee=50 [0,3700), Start#2@3700]")
	checkPrice(t, m, 3700, 10)

	// c ends by user exactly on an hour boundary: 1 hour at price(1800)=40.
	evs, err = m.Terminate(3, 5400)
	checkEvents(t, "Terminate(c,5400)", evs, err,
		"[End#3 user-terminated fee=40 [1800,5400)]")

	evs, err = m.Request(4, 30, 6000) // d
	checkEvents(t, "Request(d,30,6000)", evs, err, "[Start#4@6000]")
	checkPrice(t, m, 6000, 10)

	// K=1: d is interrupted after a free partial hour; price rises to 30.
	evs, err = m.SetCapacity(1, 7200)
	checkEvents(t, "SetCapacity(1,7200)", evs, err,
		"[End#4 market-interrupted fee=0 [6000,7200)]")
	checkPrice(t, m, 7200, 30)

	checkBill(t, m, 1, 50)
	checkBill(t, m, 2, 0)
	checkBill(t, m, 3, 40)
	checkBill(t, m, 4, 0)
	checkRunning(t, m, 2)
}

// Exactly K requests => floor price; K+1 requests => (K+1)-th bid.
func TestClearingPriceAtKAndKPlus1(t *testing.T) {
	m := mustNew(t, 2, 7)
	if _, err := m.Request(1, 90, 0); err != nil {
		t.Fatal(err)
	}
	if _, err := m.Request(2, 80, 0); err != nil {
		t.Fatal(err)
	}
	checkPrice(t, m, 0, 7) // only K requests: floor price
	if _, err := m.Request(3, 55, 10); err != nil {
		t.Fatal(err)
	}
	checkPrice(t, m, 10, 55) // K+1 requests: (K+1)-th bid
	if _, err := m.Request(4, 60, 20); err != nil {
		t.Fatal(err)
	}
	checkPrice(t, m, 20, 60) // ranking 90,80,60,55: (K+1)-th bid is 60
}

// Equal bids: the earlier submitter wins the tie.
func TestTieBreakBySubmissionOrder(t *testing.T) {
	m := mustNew(t, 1, 10)
	if _, err := m.Request(1, 50, 0); err != nil {
		t.Fatal(err)
	}
	// Same bid: request 1 was submitted first and keeps running.
	evs, err := m.Request(2, 50, 100)
	checkEvents(t, "Request(2,50,100)", evs, err, "[]")
	checkRunning(t, m, 1)
	// Terminating 1 lets 2 start.
	evs, err = m.Terminate(1, 200)
	checkEvents(t, "Terminate(1,200)", evs, err,
		"[End#1 user-terminated fee=10 [0,200), Start#2@200]")
	checkRunning(t, m, 2)
}

// A running request whose bid equals the clearing price keeps running.
func TestRunningBidEqualsClearingPrice(t *testing.T) {
	m := mustNew(t, 2, 10)
	for _, r := range [][2]int64{{1, 50}, {2, 40}, {3, 40}} {
		if _, err := m.Request(r[0], r[1], 0); err != nil {
			t.Fatal(err)
		}
	}
	// Ranking: 1(50), 2(40), 3(40). Clearing price = 3rd bid = 40.
	checkPrice(t, m, 0, 40)
	// Request 2 runs although its bid equals the clearing price.
	checkRunning(t, m, 1, 2)
}

// A new higher bid evicts the lowest ranked running request, which then
// waits.
func TestNewRequestEvictsLowestRanked(t *testing.T) {
	m := mustNew(t, 2, 10)
	if _, err := m.Request(1, 50, 0); err != nil {
		t.Fatal(err)
	}
	if _, err := m.Request(2, 40, 0); err != nil {
		t.Fatal(err)
	}
	checkRunning(t, m, 1, 2)
	// 45 evicts 2 (the lowest ranked), 2 keeps waiting in the request set.
	evs, err := m.Request(3, 45, 500)
	checkEvents(t, "Request(3,45,500)", evs, err,
		"[End#2 market-interrupted fee=0 [0,500), Start#3@500]")
	checkRunning(t, m, 1, 3)
	checkPrice(t, m, 500, 40) // (K+1)-th is now request 2 with bid 40
}

// Interrupted segments: partial hour free, full hours charged as integer
// hours.
func TestInterruptedSegmentFreeHours(t *testing.T) {
	m := mustNew(t, 1, 10)
	if _, err := m.Request(1, 100, 0); err != nil {
		t.Fatal(err)
	}
	// 1800s < 1 hour: free.
	evs, err := m.Request(2, 200, 1800)
	checkEvents(t, "Request(2,200,1800)", evs, err,
		"[End#1 market-interrupted fee=0 [0,1800), Start#2@1800]")
	checkBill(t, m, 1, 0)

	// Request 1 reruns when 2 leaves (price back to floor 10); interrupt it
	// after 1.5 hours: floor(5400/3600) = 1 hour charged at price(3600)=10.
	if _, err = m.Terminate(2, 3600); err != nil {
		t.Fatal(err)
	}
	checkRunning(t, m, 1)
	evs, err = m.Request(3, 300, 3600+5400)
	checkEvents(t, "Request(3,300,9000)", evs, err,
		"[End#1 market-interrupted fee=10 [3600,9000), Start#3@9000]")
	checkBill(t, m, 1, 10)
}

// An interruption exactly on an hour boundary does not charge a new hour.
func TestInterruptOnHourBoundary(t *testing.T) {
	m := mustNew(t, 1, 10)
	if _, err := m.Request(1, 100, 0); err != nil {
		t.Fatal(err)
	}
	evs, err := m.Request(2, 200, 7200)
	// floor(7200/3600) = 2 hours, not 3.
	checkEvents(t, "Request(2,200,7200)", evs, err,
		"[End#1 market-interrupted fee=20 [0,7200), Start#2@7200]")
	checkBill(t, m, 1, 20)
}

// User termination rounds up; exactly on the boundary no extra hour is
// charged.
func TestUserTerminateCeilAndBoundary(t *testing.T) {
	m := mustNew(t, 1, 10)
	if _, err := m.Request(1, 100, 0); err != nil {
		t.Fatal(err)
	}
	// 1 second into the hour still rounds up to a full hour.
	evs, err := m.Terminate(1, 3601)
	checkEvents(t, "Terminate(1,3601)", evs, err,
		"[End#1 user-terminated fee=20 [0,3601)]")
	checkBill(t, m, 1, 20)

	if _, err = m.Request(2, 100, 7200); err != nil {
		t.Fatal(err)
	}
	// Exactly 2 hours: ceil(7200/3600) = 2, no extra hour.
	evs, err = m.Terminate(2, 14400)
	checkEvents(t, "Terminate(2,14400)", evs, err,
		"[End#2 user-terminated fee=20 [7200,14400)]")
	checkBill(t, m, 2, 20)
}

// The k-th hour is priced at the hour start; multiple price records at the
// same t keep only the last one.
func TestHourPriceAtHourStartAndSameTLastWins(t *testing.T) {
	m := mustNew(t, 1, 10)
	if _, err := m.Request(1, 100, 0); err != nil {
		t.Fatal(err)
	}
	// t=1800: price becomes 90, then 95 at the same t; last one wins.
	if _, err := m.Request(2, 90, 1800); err != nil {
		t.Fatal(err)
	}
	checkPrice(t, m, 1800, 90)
	if _, err := m.Request(3, 95, 1800); err != nil {
		t.Fatal(err)
	}
	checkPrice(t, m, 1799, 10)
	checkPrice(t, m, 1800, 95) // 95, not 90

	// Segment [0, 9000): ceil = 3 hours priced at 0, 3600, 7200.
	evs, err := m.Terminate(1, 9000)
	checkEvents(t, "Terminate(1,9000)", evs, err,
		"[End#1 user-terminated fee=200 [0,9000), Start#3@9000]") // 10 + 95 + 95
	checkBill(t, m, 1, 200)
}

// An interrupted request that re-enters the top K starts a brand new
// segment billed from scratch.
func TestInterruptedRequestRerunsAsNewSegment(t *testing.T) {
	m := mustNew(t, 1, 10)
	if _, err := m.Request(1, 100, 0); err != nil {
		t.Fatal(err)
	}
	// Interrupt 1 after 1.5 hours: 1 hour charged.
	if _, err := m.Request(2, 200, 5400); err != nil {
		t.Fatal(err)
	}
	checkBill(t, m, 1, 10)
	// 2 leaves, 1 reruns as a NEW segment starting at 7200.
	evs, err := m.Terminate(2, 7200)
	checkEvents(t, "Terminate(2,7200)", evs, err,
		"[End#2 user-terminated fee=100 [5400,7200), Start#1@7200]")
	// Interrupt 1 again after 30 minutes: free partial hour again.
	evs, err = m.Request(3, 300, 7200+1800)
	checkEvents(t, "Request(3,300,9000)", evs, err,
		"[End#1 market-interrupted fee=0 [7200,9000), Start#3@9000]")
	checkBill(t, m, 1, 10) // only the first segment was charged
}

// SetCapacity down interrupts the lowest ranked and raises the clearing
// price; SetCapacity up lets waiters run.
func TestSetCapacityShrinkAndGrow(t *testing.T) {
	m := mustNew(t, 2, 10)
	for _, r := range [][2]int64{{1, 50}, {2, 40}, {3, 30}} {
		if _, err := m.Request(r[0], r[1], 0); err != nil {
			t.Fatal(err)
		}
	}
	checkRunning(t, m, 1, 2)
	checkPrice(t, m, 0, 30)

	// Shrink to 1: request 2 (lowest ranked running) is interrupted and
	// the clearing price rises to its bid 40.
	evs, err := m.SetCapacity(1, 1000)
	checkEvents(t, "SetCapacity(1,1000)", evs, err,
		"[End#2 market-interrupted fee=0 [0,1000)]")
	checkRunning(t, m, 1)
	checkPrice(t, m, 1000, 40)

	// Grow to 3: both waiters start, price falls back to the floor.
	evs, err = m.SetCapacity(3, 2000)
	checkEvents(t, "SetCapacity(3,2000)", evs, err,
		"[Start#2@2000, Start#3@2000]")
	checkRunning(t, m, 1, 2, 3)
	checkPrice(t, m, 2000, 10)
}

// Terminating a waiting request produces no events and no fee.
func TestTerminateWaitingRequestNoFee(t *testing.T) {
	m := mustNew(t, 1, 10)
	if _, err := m.Request(1, 50, 0); err != nil {
		t.Fatal(err)
	}
	if _, err := m.Request(2, 40, 0); err != nil {
		t.Fatal(err)
	}
	checkRunning(t, m, 1)
	evs, err := m.Terminate(2, 5000)
	checkEvents(t, "Terminate(2,5000)", evs, err, "[]")
	checkBill(t, m, 2, 0)
	checkRunning(t, m, 1)
}

// Rejected operations must not change requests, ranking, price history or
// the max accepted time.
func TestRejectionsDoNotChangeState(t *testing.T) {
	if _, err := New(0, 10); !errors.Is(err, ErrInvalidConfig) {
		t.Fatalf("New(0,10): %v", err)
	}
	if _, err := New(1001, 10); !errors.Is(err, ErrInvalidConfig) {
		t.Fatalf("New(1001,10): %v", err)
	}
	if _, err := New(2, 0); !errors.Is(err, ErrInvalidConfig) {
		t.Fatalf("New(2,0): %v", err)
	}
	if _, err := New(2, 1_000_000_001); !errors.Is(err, ErrInvalidConfig) {
		t.Fatalf("New(2,1e9+1): %v", err)
	}

	m := mustNew(t, 1, 10)
	if _, err := m.Request(1, 50, 100); err != nil {
		t.Fatal(err)
	}
	if _, err := m.Terminate(1, 200); err != nil { // accepted; maxT = 200
		t.Fatal(err)
	}
	before := stateString(m)

	// Request rejections, in order: invalid param, clock regression, dup id.
	_, err := m.Request(2, 9, 300) // bid below floor
	checkErr(t, "Request bid<floor", err, ErrInvalidParam)
	_, err = m.Request(2, 1_000_000_001, 300)
	checkErr(t, "Request bid>1e9", err, ErrInvalidParam)
	_, err = m.Request(-1, 50, 300)
	checkErr(t, "Request id<0", err, ErrInvalidParam)
	_, err = m.Request(2, 50, 1_000_000_000_000_001)
	checkErr(t, "Request t>1e15", err, ErrInvalidParam)
	_, err = m.Request(2, 50, 199) // t < maxT=200
	checkErr(t, "Request clock", err, ErrClockRegression)
	_, err = m.Request(1, 60, 300) // duplicate id
	checkErr(t, "Request dup", err, ErrDuplicateID)

	// Terminate rejections: invalid param, clock, not found, terminated.
	_, err = m.Terminate(-1, 300)
	checkErr(t, "Terminate id<0", err, ErrInvalidParam)
	_, err = m.Terminate(1, 199)
	checkErr(t, "Terminate clock", err, ErrClockRegression)
	_, err = m.Terminate(99, 300)
	checkErr(t, "Terminate unknown", err, ErrNotFound)
	_, err = m.Terminate(1, 300)
	checkErr(t, "Terminate twice", err, ErrAlreadyTerminated)
	// Terminated id cannot be reused.
	_, err = m.Request(1, 70, 300)
	checkErr(t, "Request terminated id", err, ErrDuplicateID)

	// SetCapacity rejections: invalid param, clock.
	_, err = m.SetCapacity(0, 400)
	checkErr(t, "SetCapacity 0", err, ErrInvalidParam)
	_, err = m.SetCapacity(1001, 400)
	checkErr(t, "SetCapacity 1001", err, ErrInvalidParam)
	_, err = m.SetCapacity(1, 150) // t < maxT=200
	checkErr(t, "SetCapacity clock", err, ErrClockRegression)

	if after := stateString(m); after != before {
		t.Fatalf("state changed by rejected ops:\nbefore: %s\nafter:  %s", before, after)
	}
}

func stateString(m *Market) string {
	m.mu.Lock()
	defer m.mu.Unlock()
	var b strings.Builder
	var run []int64
	for id, r := range m.reqs {
		if r.running {
			run = append(run, id)
		}
	}
	sort.Slice(run, func(i, j int) bool { return run[i] < run[j] })
	fmt.Fprintf(&b, "cap=%d maxT=%d seq=%d hist=%v run=%v", m.capacity, m.maxT,
		m.nextSeq, m.history, run)
	ids := make([]int64, 0, len(m.reqs))
	for id := range m.reqs {
		ids = append(ids, id)
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	for _, id := range ids {
		r := m.reqs[id]
		fmt.Fprintf(&b, " req{id:%d bid:%d seq:%d term:%v run:%v seg:%d bill:%s}",
			r.id, r.bid, r.seq, r.terminated, r.running, r.segStart, r.bill)
	}
	return b.String()
}

// Replaying the same operation sequence yields identical events and fees.
func TestReplayDeterminism(t *testing.T) {
	type op struct {
		kind       string
		id, bid, t int64
	}
	ops := []op{
		{"req", 1, 50, 0}, {"req", 2, 40, 0}, {"req", 3, 45, 1800},
		{"term", 1, 0, 3700}, {"cap", 0, 1, 4000}, {"req", 4, 60, 4000},
		{"term", 3, 0, 5400}, {"cap", 0, 3, 6000}, {"term", 2, 0, 9000},
		{"term", 4, 0, 9100},
	}
	run := func() []string {
		m := mustNew(t, 2, 10)
		var out []string
		for _, o := range ops {
			var evs []Event
			var err error
			switch o.kind {
			case "req":
				evs, err = m.Request(o.id, o.bid, o.t)
			case "term":
				evs, err = m.Terminate(o.id, o.t)
			case "cap":
				evs, err = m.SetCapacity(o.bid, o.t)
			}
			if err != nil {
				t.Fatalf("op %+v: %v", o, err)
			}
			out = append(out, eventsString(evs))
		}
		for _, id := range []int64{1, 2, 3, 4} {
			out = append(out, m.Bill(id).String())
		}
		return out
	}
	first := run()
	for i := 0; i < 5; i++ {
		got := run()
		if strings.Join(got, "\n") != strings.Join(first, "\n") {
			t.Fatalf("replay %d differs:\nfirst: %v\ngot:   %v", i, first, got)
		}
	}
}

// Concurrent calls are serialized by the market; run with -race.
func TestConcurrentAccess(t *testing.T) {
	m := mustNew(t, 4, 10)
	var wg sync.WaitGroup
	for g := 0; g < 8; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			base := int64(g * 1000)
			for i := int64(0); i < 50; i++ {
				id := base + i
				if _, err := m.Request(id, 10+i, i*10); err != nil {
					return // clock regression against another goroutine
				}
				_, _ = m.Terminate(id, i*10+5)
				_ = m.Bill(id)
				_ = m.RunningIDs()
				_ = m.PriceAt(i * 10)
			}
		}(g)
	}
	wg.Wait()
	m.mu.Lock()
	defer m.mu.Unlock()
	running := 0
	for _, r := range m.reqs {
		if r.running {
			running++
		}
		if r.bill.Sign() < 0 {
			t.Fatalf("negative bill for %d", r.id)
		}
	}
	if int64(running) > m.capacity {
		t.Fatalf("running %d exceeds capacity %d", running, m.capacity)
	}
}
