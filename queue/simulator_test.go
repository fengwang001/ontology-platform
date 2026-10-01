package queue

import (
	"errors"
	"fmt"
	"math/rand"
	"sort"
	"sync"
	"testing"
)

type snapshot struct {
	Outcomes map[int64]Outcome
	Stats    Statistics
}

func mustAdd(t *testing.T, s *Simulator, customer Customer) {
	t.Helper()
	if err := s.Add(customer); err != nil {
		t.Fatalf("Add(%+v): %v", customer, err)
	}
}

func collectSnapshot(t *testing.T, s *Simulator, customers []Customer) snapshot {
	t.Helper()
	result := snapshot{Outcomes: make(map[int64]Outcome, len(customers))}
	for _, customer := range customers {
		outcome, err := s.Outcome(customer.ID)
		if err != nil {
			t.Fatalf("Outcome(%d): %v", customer.ID, err)
		}
		result.Outcomes[customer.ID] = outcome
	}
	result.Stats = s.Stats()
	return result
}

func runSimulator(t *testing.T, customers []Customer, servers int, cuts []int64) snapshot {
	t.Helper()
	s, err := New(servers)
	if err != nil {
		t.Fatalf("New(%d): %v", servers, err)
	}
	for _, customer := range customers {
		mustAdd(t, s, customer)
	}
	for _, cut := range cuts {
		if err := s.AdvanceTo(cut); err != nil {
			t.Fatalf("AdvanceTo(%d): %v", cut, err)
		}
	}
	return collectSnapshot(t, s, customers)
}

type naiveCustomer struct {
	customer Customer
	status   string
	server   int
	start    int64
	end      int64
}

type naiveService struct {
	id  int64
	end int64
}

func runNaive(customers []Customer, servers int, end int64) snapshot {
	states := make(map[int64]*naiveCustomer, len(customers))
	arrivals := make(map[int64][]int64)
	for _, customer := range customers {
		states[customer.ID] = &naiveCustomer{customer: customer, status: StatusNotArrived}
		arrivals[customer.Arrive] = append(arrivals[customer.Arrive], customer.ID)
	}
	for when := range arrivals {
		sort.Slice(arrivals[when], func(i, j int) bool {
			return arrivals[when][i] < arrivals[when][j]
		})
	}

	queue := make([]int64, 0)
	busy := make([]bool, servers+1)
	serviceByServer := make(map[int]*naiveService, servers)
	result := snapshot{Outcomes: make(map[int64]Outcome, len(customers))}

	for when := int64(0); when <= end; when++ {
		for server := 1; server <= servers; server++ {
			service := serviceByServer[server]
			if service == nil || service.end != when {
				continue
			}
			state := states[service.id]
			state.status = StatusServed
			result.Stats.Served++
			result.Stats.TotalWaiting += state.start - state.customer.Arrive
			busy[server] = false
			delete(serviceByServer, server)
		}

		for server := 1; server <= servers; server++ {
			for !busy[server] && len(queue) > 0 {
				id := queue[0]
				queue = queue[1:]
				state := states[id]
				deadline := state.customer.Arrive + state.customer.Patience
				if deadline < when {
					state.status = StatusAbandoned
					result.Stats.Abandoned++
					continue
				}
				state.status = StatusServing
				state.server = server
				state.start = when
				state.end = when + state.customer.Service
				busy[server] = true
				serviceByServer[server] = &naiveService{id: id, end: state.end}
			}
		}

		for _, id := range arrivals[when] {
			state := states[id]
			server := 0
			for candidate := 1; candidate <= servers; candidate++ {
				if !busy[candidate] {
					server = candidate
					break
				}
			}
			if server == 0 {
				deadline := state.customer.Arrive + state.customer.Patience
				if deadline <= when {
					state.status = StatusAbandoned
					result.Stats.Abandoned++
					continue
				}
				state.status = StatusWaiting
				queue = append(queue, id)
				continue
			}
			state.status = StatusServing
			state.server = server
			state.start = when
			state.end = when + state.customer.Service
			busy[server] = true
			serviceByServer[server] = &naiveService{id: id, end: state.end}
		}

		kept := queue[:0]
		for _, id := range queue {
			state := states[id]
			if state.customer.Arrive+state.customer.Patience <= when {
				state.status = StatusAbandoned
				result.Stats.Abandoned++
				continue
			}
			kept = append(kept, id)
		}
		queue = kept

		if length := int64(len(queue)); length > result.Stats.MaxQueueLen {
			result.Stats.MaxQueueLen = length
		}
	}

	for id, state := range states {
		outcome := Outcome{Status: state.status}
		if state.status == StatusServing || state.status == StatusServed {
			outcome.Service = ServiceRecord{
				Server: state.server,
				Start:  state.start,
				End:    state.end,
				Wait:   state.start - state.customer.Arrive,
			}
		}
		if state.status == StatusAbandoned {
			outcome.AbandonTime = state.customer.Arrive + state.customer.Patience
		}
		result.Outcomes[id] = outcome
	}
	return result
}

func assertSnapshotEqual(t *testing.T, got, want snapshot, customers []Customer, cuts []int64, basis string) {
	t.Helper()
	if got.Stats != want.Stats {
		t.Fatalf("stats mismatch\nbasis=%s\ninput=%+v\ncuts=%v\ngot=%+v\nwant=%+v", basis, customers, cuts, got.Stats, want.Stats)
	}
	for id, wantOutcome := range want.Outcomes {
		if gotOutcome := got.Outcomes[id]; gotOutcome != wantOutcome {
			t.Fatalf("customer %d mismatch\nbasis=%s\ninput=%+v\ncuts=%v\ngot=%+v\nwant=%+v", id, basis, customers, cuts, gotOutcome, wantOutcome)
		}
	}
	t.Logf("PASS %s\ninput=%+v\ncuts=%v\nstats=%+v\noutcomes=%+v\nbasis=事件顺序、台号、边界和统计逐字段一致", basis, customers, cuts, got.Stats, got.Outcomes)
}

func TestHeadPatienceExactlyAtReleaseIsServed(t *testing.T) {
	customers := []Customer{
		{ID: 1, Arrive: 0, Service: 3, Patience: 10},
		{ID: 2, Arrive: 1, Service: 1, Patience: 2},
	}
	got := runSimulator(t, customers, 1, []int64{3})
	outcome := got.Outcomes[2]
	if outcome.Status != StatusServing || outcome.Service.Server != 1 ||
		outcome.Service.Start != 3 || outcome.Service.End != 4 || outcome.Service.Wait != 2 {
		t.Fatalf("outcome=%+v, want service starting exactly at patience deadline", outcome)
	}
	t.Logf("PASS 队首耐心边界 input=%+v output=%+v basis=完成阶段先于放弃阶段，deadline==start 算已服务", customers, outcome)
}

func TestCompletionAndArrivalSameTimeAssignsSmallestFreedServer(t *testing.T) {
	customers := []Customer{
		{ID: 1, Arrive: 0, Service: 2, Patience: 0},
		{ID: 2, Arrive: 0, Service: 5, Patience: 0},
		{ID: 3, Arrive: 2, Service: 1, Patience: 0},
	}
	got := runSimulator(t, customers, 2, []int64{2})
	outcome := got.Outcomes[3]
	if outcome.Status != StatusServing || outcome.Service.Server != 1 || outcome.Service.Start != 2 {
		t.Fatalf("outcome=%+v, want server 1 released by completion", outcome)
	}
	t.Logf("PASS 完成与到达同刻 input=%+v output=%+v basis=先释放台 1，再让同刻到达者取最小空闲台", customers, outcome)
}

func TestZeroPatienceAbandonsImmediatelyWithoutQueueLength(t *testing.T) {
	customers := []Customer{
		{ID: 1, Arrive: 0, Service: 5, Patience: 0},
		{ID: 2, Arrive: 0, Service: 1, Patience: 0},
	}
	got := runSimulator(t, customers, 1, []int64{0})
	if got.Stats.Abandoned != 1 || got.Stats.MaxQueueLen != 0 {
		t.Fatalf("stats=%+v, want immediate abandonment without queue length", got.Stats)
	}
	if got.Outcomes[2].Status != StatusAbandoned || got.Outcomes[2].AbandonTime != 0 {
		t.Fatalf("customer 2 outcome=%+v", got.Outcomes[2])
	}
	t.Logf("PASS 零耐心立即放弃 input=%+v output=%+v basis=无空闲台时不入队，三步后的队长仍为 0", customers, got)
}

func TestSameTimeArrivalsOrderedByID(t *testing.T) {
	customers := []Customer{
		{ID: 3, Arrive: 0, Service: 1, Patience: 10},
		{ID: 1, Arrive: 0, Service: 1, Patience: 10},
		{ID: 2, Arrive: 0, Service: 1, Patience: 10},
	}
	got := runSimulator(t, customers, 1, []int64{3})
	for id := int64(1); id <= 3; id++ {
		outcome := got.Outcomes[id]
		wantStart := id - 1
		if outcome.Status != StatusServed || outcome.Service.Start != wantStart || outcome.Service.Server != 1 {
			t.Fatalf("customer %d outcome=%+v, want start %d", id, outcome, wantStart)
		}
	}
	t.Logf("PASS 同刻到达按 id 排序 input提交序=%+v output=%+v basis=id 1、2、3 依次得到台 1", customers, got.Outcomes)
}

func TestMultipleIdleServersAssignedInAscendingOrder(t *testing.T) {
	customers := []Customer{
		{ID: 30, Arrive: 0, Service: 1, Patience: 0},
		{ID: 10, Arrive: 0, Service: 1, Patience: 0},
		{ID: 20, Arrive: 0, Service: 1, Patience: 0},
	}
	got := runSimulator(t, customers, 3, []int64{0})
	for id, server := range map[int64]int{10: 1, 20: 2, 30: 3} {
		outcome := got.Outcomes[id]
		if outcome.Status != StatusServing || outcome.Service.Server != server {
			t.Fatalf("customer %d outcome=%+v, want server %d", id, outcome, server)
		}
	}
	t.Logf("PASS 多空闲台升序分配 input=%+v output=%+v basis=同刻 id 升序，且每次选择编号最小的空闲台", customers, got.Outcomes)
}

func TestExpiredQueuedCustomersAreSkippedInQueueOrder(t *testing.T) {
	customers := []Customer{
		{ID: 1, Arrive: 0, Service: 3, Patience: 10},
		{ID: 2, Arrive: 0, Service: 1, Patience: 2},
		{ID: 3, Arrive: 1, Service: 1, Patience: 1},
		{ID: 4, Arrive: 2, Service: 1, Patience: 10},
	}
	want := runNaive(customers, 1, 5)
	got := runSimulator(t, customers, 1, []int64{5})
	assertSnapshotEqual(t, got, want, customers, []int64{5}, "队首过期后继续按队列次序处理后续顾客")
}

func TestAdvanceToCanBeSplitArbitrarily(t *testing.T) {
	customers := []Customer{
		{ID: 1, Arrive: 0, Service: 3, Patience: 20},
		{ID: 2, Arrive: 1, Service: 2, Patience: 1},
		{ID: 3, Arrive: 2, Service: 4, Patience: 3},
		{ID: 4, Arrive: 3, Service: 1, Patience: 0},
		{ID: 5, Arrive: 5, Service: 2, Patience: 2},
	}
	want := runNaive(customers, 2, 10)
	full := runSimulator(t, customers, 2, []int64{10})
	split := runSimulator(t, customers, 2, []int64{0, 0, 2, 2, 5, 7, 7, 10})
	assertSnapshotEqual(t, full, want, customers, []int64{10}, "一次推进到末尾对照朴素模拟")
	assertSnapshotEqual(t, split, want, customers, []int64{0, 0, 2, 2, 5, 7, 7, 10}, "任意递增拆分推进")
}

func TestSubmissionOrderDoesNotAffectResult(t *testing.T) {
	first := []Customer{
		{ID: 8, Arrive: 0, Service: 2, Patience: 1},
		{ID: 3, Arrive: 1, Service: 3, Patience: 4},
		{ID: 1, Arrive: 0, Service: 1, Patience: 0},
		{ID: 6, Arrive: 2, Service: 2, Patience: 1},
		{ID: 2, Arrive: 1, Service: 1, Patience: 2},
	}
	second := []Customer{first[3], first[1], first[4], first[0], first[2]}
	want := runNaive(first, 2, 8)
	assertSnapshotEqual(t, runSimulator(t, first, 2, []int64{8}), want, first, []int64{8}, "第一种提交次序")
	assertSnapshotEqual(t, runSimulator(t, second, 2, []int64{8}), want, second, []int64{8}, "第二种提交次序")
}

func TestRejectionsDoNotChangeState(t *testing.T) {
	if _, err := New(0); !errors.Is(err, ErrInvalidServerCount) {
		t.Fatalf("New(0) err=%v", err)
	}
	if _, err := New(65); !errors.Is(err, ErrInvalidServerCount) {
		t.Fatalf("New(65) err=%v", err)
	}

	s, err := New(1)
	if err != nil {
		t.Fatal(err)
	}
	mustAdd(t, s, Customer{ID: 1, Arrive: 5, Service: 1, Patience: 0})

	invalidCases := []Customer{
		{ID: 0, Arrive: 5, Service: 1, Patience: 0},
		{ID: 2, Arrive: -1, Service: 1, Patience: 0},
		{ID: 2, Arrive: 5, Service: 0, Patience: 0},
		{ID: 2, Arrive: 5, Service: 1, Patience: -1},
		{ID: 2, Arrive: 1_000_000_001, Service: 1, Patience: 0},
		{ID: 2, Arrive: 5, Service: 1_000_000_001, Patience: 0},
		{ID: 2, Arrive: 5, Service: 1, Patience: 1_000_000_001},
	}
	for _, customer := range invalidCases {
		if err := s.Add(customer); !errors.Is(err, ErrInvalidCustomer) {
			t.Fatalf("Add(%+v) err=%v", customer, err)
		}
	}
	if err := s.Add(Customer{ID: 1, Arrive: 6, Service: 1, Patience: 0}); !errors.Is(err, ErrDuplicateID) {
		t.Fatalf("duplicate Add err=%v", err)
	}
	if err := s.AdvanceTo(5); err != nil {
		t.Fatal(err)
	}
	if err := s.Add(Customer{ID: 2, Arrive: 5, Service: 1, Patience: 0}); !errors.Is(err, ErrArrivalBeforeWatermark) {
		t.Fatalf("late Add err=%v", err)
	}
	if err := s.AdvanceTo(-1); !errors.Is(err, ErrInvalidTargetTime) {
		t.Fatalf("negative AdvanceTo err=%v", err)
	}
	if err := s.AdvanceTo(4); !errors.Is(err, ErrInvalidTargetTime) {
		t.Fatalf("backward AdvanceTo err=%v", err)
	}
	if err := s.AdvanceTo(5); err != nil {
		t.Fatalf("equal-watermark AdvanceTo: %v", err)
	}
	if _, err := s.Outcome(99); !errors.Is(err, ErrCustomerNotFound) {
		t.Fatalf("missing Outcome err=%v", err)
	}

	outcome, err := s.Outcome(1)
	if err != nil {
		t.Fatal(err)
	}
	stats := s.Stats()
	if outcome.Status != StatusServing || outcome.Service.Server != 1 || outcome.Service.Start != 5 || outcome.Service.End != 6 ||
		stats != (Statistics{}) {
		t.Fatalf("state changed after rejected operations outcome=%+v stats=%+v", outcome, stats)
	}
	t.Logf("PASS 被拒绝操作不改状态 outcome=%+v stats=%+v basis=拒绝前快照与拒绝后台号、时刻和统计一致", outcome, stats)
}

func TestConcurrentOperationsAreSafe(t *testing.T) {
	s, err := New(4)
	if err != nil {
		t.Fatal(err)
	}

	var submit sync.WaitGroup
	for id := int64(1); id <= 80; id++ {
		submit.Add(1)
		go func(id int64) {
			defer submit.Done()
			arrive := int64(20 + (id-1)%8)
			service := int64(1 + id%5)
			patience := int64(id % 4)
			if err := s.Add(Customer{ID: id, Arrive: arrive, Service: service, Patience: patience}); err != nil {
				t.Errorf("concurrent Add(%d): %v", id, err)
			}
		}(id)
	}
	submit.Wait()

	var operate sync.WaitGroup
	operate.Add(1)
	go func() {
		defer operate.Done()
		for when := int64(0); when <= 30; when++ {
			if err := s.AdvanceTo(when); err != nil {
				t.Errorf("concurrent monotonic AdvanceTo(%d): %v", when, err)
			}
		}
	}()
	for worker := 0; worker < 8; worker++ {
		operate.Add(1)
		go func(worker int) {
			defer operate.Done()
			for range 31 {
				if _, err := s.Outcome(int64(worker*10 + 1)); err != nil {
					t.Errorf("concurrent Outcome: %v", err)
				}
				_ = s.Stats()
			}
		}(worker)
	}
	operate.Wait()

	stats := s.Stats()
	var waiting, serving int64
	for id := int64(1); id <= 80; id++ {
		outcome, err := s.Outcome(id)
		if err != nil {
			t.Fatal(err)
		}
		switch outcome.Status {
		case StatusWaiting:
			waiting++
		case StatusServing:
			serving++
		}
	}
	if stats.Served+stats.Abandoned+waiting+serving != 80 {
		t.Fatalf("invariant violated stats=%+v waiting=%d serving=%d, total=%d", stats, waiting, serving, stats.Served+stats.Abandoned+waiting+serving)
	}
	t.Logf("PASS 并发提交推进查询安全 stats=%+v waiting=%d serving=%d basis=互斥串行化、已到达顾客四类状态守恒且 -race 未发现数据竞争", stats, waiting, serving)
}

func TestRandomScenariosMatchNaiveSimulation(t *testing.T) {
	rng := rand.New(rand.NewSource(20261001))

	for scenario := 0; scenario < 2000; scenario++ {
		servers := 1 + rng.Intn(4)
		count := rng.Intn(11)
		customers := make([]Customer, count)
		var horizon int64
		for index := range customers {
			customers[index] = Customer{
				ID:       int64(index + 1),
				Arrive:   int64(rng.Intn(9)),
				Service:  int64(1 + rng.Intn(5)),
				Patience: int64(rng.Intn(6)),
			}
			horizon += customers[index].Service
		}
		horizon += 9
		rng.Shuffle(len(customers), func(i, j int) {
			customers[i], customers[j] = customers[j], customers[i]
		})

		cuts := []int64{0}
		for when := int64(1); when < horizon; when++ {
			if rng.Float64() < 0.25 {
				cuts = append(cuts, when)
			}
		}
		cuts = append(cuts, horizon)

		want := runNaive(customers, servers, horizon)
		full := runSimulator(t, customers, servers, []int64{horizon})
		split := runSimulator(t, customers, servers, cuts)
		assertSnapshotEqual(t, full, want, customers, []int64{horizon}, fmt.Sprintf("随机场景 %d 一次推进", scenario+1))
		assertSnapshotEqual(t, split, want, customers, cuts, fmt.Sprintf("随机场景 %d 拆分推进", scenario+1))
		assertSimulationInvariants(t, customers, full)

		t.Logf(
			"RANDOM %d/2000 input servers=%d customers=%+v output stats=%+v cuts=%v basis=与每个整数时刻执行完成、到达、放弃三步的朴素模拟逐顾客逐字段一致",
			scenario+1, servers, customers, want.Stats, cuts,
		)
	}
}

func assertSimulationInvariants(t *testing.T, customers []Customer, got snapshot) {
	t.Helper()
	byID := make(map[int64]Customer, len(customers))
	var waiting, serving int64
	for _, customer := range customers {
		byID[customer.ID] = customer
		outcome := got.Outcomes[customer.ID]
		switch outcome.Status {
		case StatusWaiting:
			waiting++
		case StatusServing:
			serving++
		}
		if (outcome.Status == StatusServing || outcome.Status == StatusServed) &&
			(outcome.Service.Start < customer.Arrive || outcome.Service.Wait != outcome.Service.Start-customer.Arrive) {
			t.Fatalf("customer %d violates service boundary: input=%+v outcome=%+v", customer.ID, customer, outcome)
		}
	}
	if got.Stats.Served+got.Stats.Abandoned+waiting+serving != int64(len(customers)) {
		t.Fatalf("status partition invariant failed stats=%+v waiting=%d serving=%d", got.Stats, waiting, serving)
	}

	type interval struct {
		id    int64
		start int64
		end   int64
	}
	byServer := make(map[int][]interval)
	for id, outcome := range got.Outcomes {
		if outcome.Status == StatusServing || outcome.Status == StatusServed {
			byServer[outcome.Service.Server] = append(byServer[outcome.Service.Server], interval{
				id: id, start: outcome.Service.Start, end: outcome.Service.End,
			})
		}
	}
	for server, intervals := range byServer {
		sort.Slice(intervals, func(i, j int) bool { return intervals[i].start < intervals[j].start })
		for i := 1; i < len(intervals); i++ {
			if intervals[i].start < intervals[i-1].end {
				t.Fatalf("server %d overlap: %+v and %+v", server, intervals[i-1], intervals[i])
			}
		}
	}
}
