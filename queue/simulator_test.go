package queue

import (
	"errors"
	"math/rand"
	"sort"
	"sync"
	"testing"
)

func mustNewSimulator(t *testing.T, servers int) *Simulator {
	t.Helper()
	simulator, err := NewSimulator(servers)
	if err != nil {
		t.Fatalf("NewSimulator(%d) returned error: %v", servers, err)
	}
	return simulator
}

func mustAdd(t *testing.T, simulator *Simulator, customer Customer) {
	t.Helper()
	if err := simulator.Add(customer.ID, customer.Arrive, customer.Service, customer.Patience); err != nil {
		t.Fatalf("Add(%+v) returned error: %v", customer, err)
	}
}

func assertError(t *testing.T, err error, want ErrorCode) {
	t.Helper()
	var operationError *OperationError
	if !errors.As(err, &operationError) {
		t.Fatalf("error = %v, want code %q", err, want)
	}
	if operationError.Code != want {
		t.Fatalf("error code = %q, want %q", operationError.Code, want)
	}
}

func TestHeadCustomerDeadlineAtReleaseIsServed(t *testing.T) {
	simulator := mustNewSimulator(t, 1)
	mustAdd(t, simulator, Customer{ID: 1, Arrive: 0, Service: 3, Patience: 99})
	mustAdd(t, simulator, Customer{ID: 2, Arrive: 1, Service: 2, Patience: 2})
	if err := simulator.AdvanceTo(3); err != nil {
		t.Fatal(err)
	}
	outcome, err := simulator.Outcome(2)
	if err != nil {
		t.Fatal(err)
	}
	want := Outcome{Status: Serving, Server: 1, Start: 3, End: 5, Wait: 2}
	if outcome != want {
		t.Fatalf("Outcome(2)=%+v want %+v", outcome, want)
	}
	t.Logf("input=[Add(1,0,3,99) Add(2,1,2,2) AdvanceTo(3)] output=%+v decision=completion-and-queue-start-before-abandonment", outcome)
}

func TestArrivalAtCompletionGetsSmallestReleasedServer(t *testing.T) {
	simulator := mustNewSimulator(t, 2)
	mustAdd(t, simulator, Customer{ID: 1, Arrive: 0, Service: 2, Patience: 99})
	mustAdd(t, simulator, Customer{ID: 2, Arrive: 0, Service: 4, Patience: 99})
	mustAdd(t, simulator, Customer{ID: 3, Arrive: 2, Service: 1, Patience: 0})
	if err := simulator.AdvanceTo(2); err != nil {
		t.Fatal(err)
	}
	outcome, err := simulator.Outcome(3)
	if err != nil {
		t.Fatal(err)
	}
	want := Outcome{Status: Serving, Server: 1, Start: 2, End: 3, Wait: 0}
	if outcome != want {
		t.Fatalf("Outcome(3)=%+v want %+v", outcome, want)
	}
	t.Logf("input=[two busy servers,Add(3,2,1,0),AdvanceTo(2)] output=%+v decision=smallest free server after completion", outcome)
}

func TestZeroPatienceWithoutFreeServerAbandonsWithoutMaxQueue(t *testing.T) {
	simulator := mustNewSimulator(t, 1)
	mustAdd(t, simulator, Customer{ID: 1, Arrive: 0, Service: 2, Patience: 99})
	mustAdd(t, simulator, Customer{ID: 2, Arrive: 1, Service: 1, Patience: 0})
	if err := simulator.AdvanceTo(1); err != nil {
		t.Fatal(err)
	}
	outcome, err := simulator.Outcome(2)
	if err != nil {
		t.Fatal(err)
	}
	want := Outcome{Status: Abandoned, AbandonTime: 1}
	if outcome != want {
		t.Fatalf("Outcome(2)=%+v want %+v", outcome, want)
	}
	stats := simulator.Stats()
	if stats.MaxQueueSize != 0 || stats.Abandoned != 1 {
		t.Fatalf("Stats()=%+v want max queue 0 and one abandonment", stats)
	}
	t.Logf("input=[busy server, Add(2,1,1,0), AdvanceTo(1)] output=%+v stats=%+v decision=abandon-before-max-queue-record", outcome, stats)
}

func TestSameTimeArrivalsOrderedByID(t *testing.T) {
	simulator := mustNewSimulator(t, 1)
	mustAdd(t, simulator, Customer{ID: 30, Arrive: 0, Service: 1, Patience: 99})
	mustAdd(t, simulator, Customer{ID: 10, Arrive: 1, Service: 1, Patience: 0})
	mustAdd(t, simulator, Customer{ID: 20, Arrive: 1, Service: 1, Patience: 0})
	if err := simulator.AdvanceTo(1); err != nil {
		t.Fatal(err)
	}
	outcome10, _ := simulator.Outcome(10)
	outcome20, _ := simulator.Outcome(20)
	if outcome10.Status != Serving || outcome10.Server != 1 {
		t.Fatalf("Outcome(10)=%+v want service on server 1", outcome10)
	}
	if outcome20.Status != Abandoned || outcome20.AbandonTime != 1 {
		t.Fatalf("Outcome(20)=%+v want abandonment", outcome20)
	}
	t.Logf("input submission=[30,10,20] output10=%+v output20=%+v decision=same-time arrivals use id order", outcome10, outcome20)
}

func TestMultipleFreeServersAssignedInAscendingOrder(t *testing.T) {
	simulator := mustNewSimulator(t, 3)
	for _, c := range []Customer{{1, 0, 1, 99}, {2, 0, 1, 99}, {3, 0, 1, 99}, {5, 0, 1, 9}} {
		mustAdd(t, simulator, c)
	}
	for _, c := range []Customer{{6, 1, 1, 0}, {7, 1, 1, 0}, {8, 1, 1, 0}} {
		mustAdd(t, simulator, c)
	}
	if err := simulator.AdvanceTo(1); err != nil {
		t.Fatal(err)
	}
	o5, _ := simulator.Outcome(5)
	o6, _ := simulator.Outcome(6)
	o7, _ := simulator.Outcome(7)
	o8, _ := simulator.Outcome(8)
	if o5.Server != 1 {
		t.Fatalf("queued customer 5 server=%d want 1", o5.Server)
	}
	if o6.Server != 2 || o7.Server != 3 {
		t.Fatalf("arrival servers=%d,%d want 2,3", o6.Server, o7.Server)
	}
	if o8.Status != Abandoned {
		t.Fatalf("customer 8=%+v want abandoned", o8)
	}
	t.Logf("output queued=%d arrivals=[%d,%d] abandoned=%+v decision=queue first then ascending free server IDs", o5.Server, o6.Server, o7.Server, o8)
}

func TestAdvanceToSplitsAreEquivalent(t *testing.T) {
	customers := []Customer{{1, 0, 2, 1}, {2, 1, 3, 3}, {3, 2, 1, 0}, {4, 3, 2, 1}, {5, 4, 1, 9}}
	oneShot := mustNewSimulator(t, 1)
	split := mustNewSimulator(t, 1)
	for _, c := range customers {
		mustAdd(t, oneShot, c)
		mustAdd(t, split, c)
	}
	if err := oneShot.AdvanceTo(10); err != nil {
		t.Fatal(err)
	}
	for _, at := range []int64{0, 2, 2, 5, 7, 10} {
		if err := split.AdvanceTo(at); err != nil {
			t.Fatal(err)
		}
	}
	for _, c := range customers {
		left, _ := oneShot.Outcome(c.ID)
		right, _ := split.Outcome(c.ID)
		if left != right {
			t.Fatalf("customer %d one-shot=%+v split=%+v", c.ID, left, right)
		}
	}
	if oneShot.Stats() != split.Stats() {
		t.Fatalf("stats one-shot=%+v split=%+v", oneShot.Stats(), split.Stats())
	}
	t.Logf("input=%+v output=%+v decision=split AdvanceTo sequence equals one-shot", customers, split.Stats())
}

func TestRejectionsDoNotChangeState(t *testing.T) {
	simulator := mustNewSimulator(t, 1)
	mustAdd(t, simulator, Customer{ID: 1, Arrive: 0, Service: 1, Patience: 0})
	if err := simulator.AdvanceTo(1); err != nil {
		t.Fatal(err)
	}
	before := simulator.Stats()
	cases := []struct {
		c    Customer
		code ErrorCode
	}{
		{Customer{0, 1, 1, 0}, ErrInvalidID},
		{Customer{2, -1, 1, 0}, ErrInvalidArrival},
		{Customer{2, 1, 0, 0}, ErrInvalidService},
		{Customer{2, 1, 1, -1}, ErrInvalidPatience},
		{Customer{2, 1, 1_000_000_001, 0}, ErrInvalidService},
		{Customer{1, 3, 1, 0}, ErrDuplicateID},
		{Customer{2, 1, 1, 0}, ErrLateArrival},
	}
	for _, tc := range cases {
		assertError(t, simulator.Add(tc.c.ID, tc.c.Arrive, tc.c.Service, tc.c.Patience), tc.code)
	}
	assertError(t, simulator.AdvanceTo(-1), ErrInvalidTargetTime)
	assertError(t, simulator.AdvanceTo(0), ErrTargetBeforeWater)
	_, err := simulator.Outcome(404)
	assertError(t, err, ErrCustomerNotFound)
	if simulator.Stats() != before {
		t.Fatalf("stats before=%+v after=%+v", before, simulator.Stats())
	}
	o1, _ := simulator.Outcome(1)
	if o1.Status != Served || o1.Server != 1 || o1.Start != 0 || o1.End != 1 || o1.Wait != 0 {
		t.Fatalf("o1=%+v", o1)
	}
	t.Logf("input=%d rejected operations output stats=%+v outcome1=%+v decision=rejections are atomic", len(cases)+3, before, o1)
}

func TestNewSimulatorRejectsInvalidServerCount(t *testing.T) {
	for _, servers := range []int{-1, 0, 65} {
		assertError(t, func() error { _, err := NewSimulator(servers); return err }(), ErrInvalidServers)
	}
}

func TestConcurrentOperationsAreSafe(t *testing.T) {
	simulator := mustNewSimulator(t, 4)
	for id := int64(1); id <= 100; id++ {
		mustAdd(t, simulator, Customer{id, 100 + id, 1 + id%5, int64(id % 7)})
	}
	var wg sync.WaitGroup
	start := make(chan struct{})
	for worker := 0; worker < 8; worker++ {
		wg.Add(1)
		go func(worker int) {
			defer wg.Done()
			<-start
			for step := int64(0); step < 25; step++ {
				_ = simulator.AdvanceTo(100 + step*10)
				_ = simulator.Stats()
				_, _ = simulator.Outcome(1 + (int64(worker)+step)%100)
			}
		}(worker)
	}
	close(start)
	wg.Wait()
	if err := simulator.AdvanceTo(400); err != nil {
		t.Fatal(err)
	}
	stats := simulator.Stats()
	counts := map[Status]int{}
	for id := int64(1); id <= 100; id++ {
		o, err := simulator.Outcome(id)
		if err != nil {
			t.Fatal(err)
		}
		counts[o.Status]++
	}
	if got := stats.Served + stats.Abandoned + counts[Waiting] + counts[Serving]; got != 100 {
		t.Fatalf("accounted=%d stats=%+v counts=%+v", got, stats, counts)
	}
	t.Logf("input=100 customers,8 workers output stats=%+v counts=%+v decision=concurrency preserves population invariant", stats, counts)
}

type naiveCustomer struct {
	customer Customer
	status   Status
	server   int
	start    int64
	end      int64
	wait     int64
	abandon  int64
}

type naiveSimulator struct {
	servers   int
	queuedIDs []int64
	items     map[int64]*naiveCustomer
	busy      map[int]int64
	served    int
	abandoned int
	totalWait int64
	maxQueue  int
}

func newNaiveSimulator(servers int) *naiveSimulator {
	return &naiveSimulator{servers: servers, items: make(map[int64]*naiveCustomer), busy: make(map[int]int64)}
}

func (n *naiveSimulator) run(customers []Customer, horizon int64) {
	arrivals := make(map[int64][]int64)
	var latest int64
	for _, c := range customers {
		n.items[c.ID] = &naiveCustomer{customer: c, status: NotArrived}
		arrivals[c.Arrive] = append(arrivals[c.Arrive], c.ID)
		if c.Arrive > latest {
			latest = c.Arrive
		}
	}
	for at := int64(0); at <= horizon; at++ {
		for server := 1; server <= n.servers; server++ {
			if end, busy := n.busy[server]; busy && end == at {
				delete(n.busy, server)
				customer := n.findServedAt(server, at)
				customer.status = Served
				n.served++
				n.totalWait += customer.wait
			}
		}
		n.startQueued(at)
		ids := append([]int64(nil), arrivals[at]...)
		sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
		for _, id := range ids {
			customer := n.items[id]
			if server, ok := n.smallestFreeServer(); ok {
				n.begin(customer, server, at)
			} else {
				customer.status = Waiting
				n.queuedIDs = append(n.queuedIDs, id)
			}
		}
		kept := n.queuedIDs[:0]
		for _, id := range n.queuedIDs {
			customer := n.items[id]
			if customer.customer.Arrive+customer.customer.Patience <= at {
				customer.status = Abandoned
				customer.abandon = customer.customer.Arrive + customer.customer.Patience
				n.abandoned++
			} else {
				kept = append(kept, id)
			}
		}
		n.queuedIDs = kept
		if len(n.queuedIDs) > n.maxQueue {
			n.maxQueue = len(n.queuedIDs)
		}
	}
	_ = latest
}

func (n *naiveSimulator) findServedAt(server int, end int64) *naiveCustomer {
	for _, customer := range n.items {
		if customer.status == Serving && customer.server == server && customer.end == end {
			return customer
		}
	}
	return nil
}

func (n *naiveSimulator) startQueued(at int64) {
	for len(n.queuedIDs) > 0 {
		server, ok := n.smallestFreeServer()
		if !ok {
			return
		}
		id := n.queuedIDs[0]
		n.queuedIDs = n.queuedIDs[1:]
		n.begin(n.items[id], server, at)
	}
}

func (n *naiveSimulator) smallestFreeServer() (int, bool) {
	for server := 1; server <= n.servers; server++ {
		if _, busy := n.busy[server]; !busy {
			return server, true
		}
	}
	return 0, false
}

func (n *naiveSimulator) begin(customer *naiveCustomer, server int, at int64) {
	customer.status = Serving
	customer.server = server
	customer.start = at
	customer.end = at + customer.customer.Service
	customer.wait = at - customer.customer.Arrive
	n.busy[server] = customer.end
}

func TestRandomScenariosMatchNaiveEveryInteger(t *testing.T) {
	rng := rand.New(rand.NewSource(20261001))
	for scenario := 0; scenario < 2000; scenario++ {
		servers := 1 + rng.Intn(4)
		count := 1 + rng.Intn(10)
		used := map[int64]bool{}
		customers := make([]Customer, 0, count)
		for len(customers) < count {
			id := int64(1 + rng.Intn(30))
			if used[id] {
				continue
			}
			used[id] = true
			customers = append(customers, Customer{
				ID:       id,
				Arrive:   int64(rng.Intn(10)),
				Service:  int64(1 + rng.Intn(7)),
				Patience: int64(rng.Intn(8)),
			})
		}
		var horizon int64 = 28
		full := mustNewSimulator(t, servers)
		split := mustNewSimulator(t, servers)
		shuffled := append([]Customer(nil), customers...)
		rng.Shuffle(len(shuffled), func(i, j int) { shuffled[i], shuffled[j] = shuffled[j], shuffled[i] })
		for _, c := range shuffled {
			mustAdd(t, full, c)
			mustAdd(t, split, c)
		}
		if err := full.AdvanceTo(horizon); err != nil {
			t.Fatal(err)
		}
		at := int64(-1)
		for at < horizon {
			at += int64(1 + rng.Intn(4))
			if at > horizon {
				at = horizon
			}
			if err := split.AdvanceTo(at); err != nil {
				t.Fatal(err)
			}
		}
		naive := newNaiveSimulator(servers)
		naive.run(customers, horizon)
		for _, c := range customers {
			actual, err := full.Outcome(c.ID)
			if err != nil {
				t.Fatal(err)
			}
			splitOutcome, err := split.Outcome(c.ID)
			if err != nil {
				t.Fatal(err)
			}
			reference := naive.items[c.ID]
			want := Outcome{Status: reference.status, Server: reference.server, Start: reference.start, End: reference.end, Wait: reference.wait, AbandonTime: reference.abandon}
			if actual != want || splitOutcome != want {
				t.Fatalf("scenario %d input=%+v servers=%d full=%+v split=%+v naive=%+v decision=field-by-field outcomes must match", scenario, customers, servers, actual, splitOutcome, want)
			}
		}
		wantStats := Stats{Served: naive.served, Abandoned: naive.abandoned, TotalWait: naive.totalWait, MaxQueueSize: naive.maxQueue}
		if full.Stats() != wantStats || split.Stats() != wantStats {
			t.Fatalf("scenario %d input=%+v full=%+v split=%+v naive=%+v decision=stats must match", scenario, customers, full.Stats(), split.Stats(), wantStats)
		}
		t.Logf("scenario=%d input servers=%d customers=%+v output=%+v decision=matches naive completion->queue-start->arrivals->abandonment at every integer", scenario, servers, customers, full.Stats())
	}
}
