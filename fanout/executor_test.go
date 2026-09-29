package fanout

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

type shardScript struct {
	result ShardResult
	ok     bool
	err    error
	delay  time.Duration
	block  bool
	dups   int
}

type scriptedClient struct {
	mu       sync.Mutex
	scripts  map[string]shardScript
	inFlight atomic.Int64
	peak     atomic.Int64
}

func newScriptedClient(scripts map[string]shardScript) *scriptedClient {
	return &scriptedClient{scripts: scripts}
}

func (c *scriptedClient) Query(ctx context.Context, spec ShardSpec, agg Aggregation, k int, emit func(ShardResult)) error {
	cur := c.inFlight.Add(1)
	for {
		p := c.peak.Load()
		if cur <= p || c.peak.CompareAndSwap(p, cur) {
			break
		}
	}
	defer c.inFlight.Add(-1)

	c.mu.Lock()
	s := c.scripts[spec.Name]
	c.mu.Unlock()

	if s.block {
		<-ctx.Done()
		return ctx.Err()
	}
	if s.delay > 0 {
		select {
		case <-time.After(s.delay):
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	if !s.ok {
		return s.err
	}
	emit(s.result)
	for i := 0; i < s.dups; i++ {
		emit(s.result)
	}
	return nil
}

func testSpecs(names ...string) []ShardSpec {
	out := make([]ShardSpec, len(names))
	for i, n := range names {
		out[i] = ShardSpec{Name: n, MaxRows: 100, MaxValue: 10}
	}
	return out
}

func logCase(t *testing.T, name string, req Request, ans Answer, st Stats, err error) {
	t.Helper()
	t.Logf("CASE %s", name)
	t.Logf("  input: agg=%s k=%d concurrency=%d deadline=%s shards=%d",
		req.Agg, req.K, req.Concurrency, req.Deadline, len(req.Shards))
	for _, s := range req.Shards {
		t.Logf("    shard %s: MaxRows=%d MaxValue=%d", s.Name, s.MaxRows, s.MaxValue)
	}
	if err != nil {
		t.Logf("  rejected: %v", err)
		return
	}
	t.Logf("  stats: succeeded=%d failed=%d timedOut=%d boundViolation=%d duplicate=%d peakConcurrent=%d",
		st.Succeeded, st.Failed, st.TimedOut, st.BoundViolation, st.Duplicate, st.PeakConcurrent)
	t.Logf("  answer: conclusive=%v exact=%v range=[%d..%d] hasLower=%v hasUpper=%v top=%v",
		ans.Conclusive, ans.Exact, ans.Lower, ans.Upper, ans.HasLower, ans.HasUpper, ans.Top)
	switch ans.Agg {
	case AggCount, AggSum:
		t.Logf("  reason: lower=sum of received; upper adds missing rows/value bounds")
	case AggMin:
		t.Logf("  reason: true min <= smallest observed min; no lower endpoint while shards missing")
	case AggMax:
		t.Logf("  reason: lower=largest observed max; upper=max(that, highest missing MaxValue)")
	case AggTopK:
		t.Logf("  reason: prefix strictly greater than every missing MaxValue is certain")
	}
}

func runScripted(t *testing.T, name string, agg Aggregation, k, conc int, dl time.Duration,
	shards []ShardSpec, scripts map[string]shardScript) (Answer, Stats, *scriptedClient) {
	t.Helper()
	client := newScriptedClient(scripts)
	req := Request{Shards: shards, Agg: agg, K: k, Concurrency: conc, Deadline: dl, Client: client}
	ans, st, err := Execute(context.Background(), req)
	if err != nil {
		t.Fatalf("%s: unexpected error: %v", name, err)
	}
	logCase(t, name, req, ans, st, nil)
	return ans, st, client
}

var errShard = errors.New("shard rpc failed")

// TestCountRanges covers exact point values and the closed interval when
// shards are missing.
func TestCountRanges(t *testing.T) {
	shards := testSpecs("a", "b", "c")
	scripts := map[string]shardScript{
		"a": {ok: true, result: ShardResult{Count: 10}},
		"b": {ok: false, err: errShard},
		"c": {block: true},
	}
	ans, st, _ := runScripted(t, "count-missing", AggCount, 0, 3, time.Second, shards, scripts)
	if ans.Lower != 10 || ans.Upper != 210 || !ans.HasLower || !ans.HasUpper {
		t.Fatalf("count interval = [%d,%d], want [10,210]", ans.Lower, ans.Upper)
	}
	if ans.Exact || !ans.Conclusive {
		t.Fatalf("count flags exact=%v conclusive=%v", ans.Exact, ans.Conclusive)
	}
	if st.Succeeded != 1 || st.Failed != 1 || st.TimedOut != 1 {
		t.Fatalf("stats = %+v", st)
	}

	all := map[string]shardScript{
		"a": {ok: true, result: ShardResult{Count: 10}},
		"b": {ok: true, result: ShardResult{Count: 20}},
		"c": {ok: true, result: ShardResult{Count: 30}},
	}
	ans2, st2, _ := runScripted(t, "count-exact", AggCount, 0, 3, time.Second, shards, all)
	if ans2.Lower != 60 || ans2.Upper != 60 || !ans2.Exact || st2.Succeeded != 3 {
		t.Fatalf("exact count = %+v stats %+v", ans2, st2)
	}
}

func TestSumRanges(t *testing.T) {
	shards := []ShardSpec{
		{Name: "a", MaxRows: 100, MaxValue: 10},
		{Name: "b", MaxRows: 50, MaxValue: 4},
	}
	scripts := map[string]shardScript{
		"a": {ok: true, result: ShardResult{Sum: 250}},
		"b": {block: true},
	}
	ans, st, _ := runScripted(t, "sum-missing", AggSum, 0, 2, time.Second, shards, scripts)
	if ans.Lower != 250 || ans.Upper != 450 {
		t.Fatalf("sum interval = [%d,%d], want [250,450]", ans.Lower, ans.Upper)
	}
	if st.TimedOut != 1 {
		t.Fatalf("stats = %+v", st)
	}

	// Sum that exceeds MaxRows*MaxValue is a bound violation -> missing.
	bad := map[string]shardScript{
		"a": {ok: true, result: ShardResult{Sum: 250}},
		"b": {ok: true, result: ShardResult{Sum: 201}}, // > 50*4
	}
	ans2, st2, _ := runScripted(t, "sum-bound-violation", AggSum, 0, 2, time.Second, shards, bad)
	if st2.BoundViolation != 1 || st2.Succeeded != 1 || ans2.Lower != 250 || ans2.Upper != 450 {
		t.Fatalf("sum bound case = %+v %+v", ans2, st2)
	}
}

func TestMinMaxDirections(t *testing.T) {
	shards := testSpecs("a", "b")
	scripts := map[string]shardScript{
		"a": {ok: true, result: ShardResult{Min: 3, Max: 8}},
		"b": {block: true},
	}
	ansMin, _, _ := runScripted(t, "min-missing", AggMin, 0, 2, time.Second, shards, scripts)
	if ansMin.HasLower || !ansMin.HasUpper || ansMin.Upper != 3 {
		t.Fatalf("min one-sided: hasLower=%v upper=%d, want <=3 with no lower", ansMin.HasLower, ansMin.Upper)
	}
	ansMax, _, _ := runScripted(t, "max-missing", AggMax, 0, 2, time.Second, shards, scripts)
	if !ansMax.HasLower || !ansMax.HasUpper || ansMax.Lower != 8 || ansMax.Upper != 10 {
		t.Fatalf("max interval = [%d,%d], want [8,10]", ansMax.Lower, ansMax.Upper)
	}

	boundScripts := map[string]shardScript{
		"a": {ok: true, result: ShardResult{Min: 3, Max: 8}},
		"b": {ok: true, result: ShardResult{Min: 2, Max: 20}}, // 20 > MaxValue 10
	}
	ansMax2, st2, _ := runScripted(t, "max-bound-violation", AggMax, 0, 2, time.Second, shards, boundScripts)
	if st2.BoundViolation != 1 || st2.Succeeded != 1 || ansMax2.Lower != 8 || ansMax2.Upper != 10 {
		t.Fatalf("after bound violation: %+v %+v", ansMax2, st2)
	}

	exactScripts := map[string]shardScript{
		"a": {ok: true, result: ShardResult{Min: 3, Max: 8}},
		"b": {ok: true, result: ShardResult{Min: 2, Max: 5}},
	}
	ansMinE, _, _ := runScripted(t, "min-exact", AggMin, 0, 2, time.Second, shards, exactScripts)
	ansMaxE, _, _ := runScripted(t, "max-exact", AggMax, 0, 2, time.Second, shards, exactScripts)
	if !ansMinE.Exact || ansMinE.Lower != 2 || ansMinE.Upper != 2 {
		t.Fatalf("exact min = %+v", ansMinE)
	}
	if !ansMaxE.Exact || ansMaxE.Lower != 8 || ansMaxE.Upper != 8 {
		t.Fatalf("exact max = %+v", ansMaxE)
	}
}

func TestTopKCertainPrefix(t *testing.T) {
	shards := []ShardSpec{
		{Name: "a", MaxRows: 100, MaxValue: 10},
		{Name: "b", MaxRows: 100, MaxValue: 10},
		{Name: "c", MaxRows: 100, MaxValue: 5},
	}
	scripts := map[string]shardScript{
		"a": {ok: true, result: ShardResult{Top: []Pair{{"x", 9}, {"y", 5}, {"z", 1}}}},
		"b": {ok: true, result: ShardResult{Top: []Pair{{"w", 8}, {"q", 4}}}},
		"c": {block: true},
	}
	ans, st, _ := runScripted(t, "topk-prefix", AggTopK, 3, 3, time.Second, shards, scripts)
	if st.TimedOut != 1 {
		t.Fatalf("stats = %+v", st)
	}
	want := []RankedPair{
		{Pair{"x", 9}, true},
		{Pair{"w", 8}, true},
		{Pair{"y", 5}, false}, // 5 == 5, not strictly greater
	}
	if len(ans.Top) != len(want) {
		t.Fatalf("top len = %d, want %d (%v)", len(ans.Top), len(want), ans.Top)
	}
	for i := range want {
		if ans.Top[i] != want[i] {
			t.Fatalf("top[%d] = %+v, want %+v (full %v)", i, ans.Top[i], want[i], ans.Top)
		}
	}
	if ans.Exact {
		t.Fatalf("topk must not be exact with a missing shard")
	}

	shards2 := testSpecs("a", "c")
	scripts2 := map[string]shardScript{
		"a": {ok: true, result: ShardResult{Top: []Pair{{"x", 10}}}},
		"c": {block: true},
	}
	ans2, _, _ := runScripted(t, "topk-equal-bound", AggTopK, 1, 2, time.Second, shards2, scripts2)
	if ans2.Top[0].Certain {
		t.Fatalf("value equal to missing bound must not be certain: %+v", ans2.Top[0])
	}

	// All present -> full ranking is certain, exactly K entries.
	exact := map[string]shardScript{
		"a": {ok: true, result: ShardResult{Top: []Pair{{"x", 9}, {"y", 5}}}},
		"b": {ok: true, result: ShardResult{Top: []Pair{{"w", 8}}}},
		"c": {ok: true, result: ShardResult{Top: []Pair{{"q", 4}}}},
	}
	ans3, st3, _ := runScripted(t, "topk-exact", AggTopK, 2, 3, time.Second, shards, exact)
	if st3.Succeeded != 3 || !ans3.Exact {
		t.Fatalf("exact topk stats = %+v exact=%v", st3, ans3.Exact)
	}
	if len(ans3.Top) != 2 || !ans3.Top[0].Certain || !ans3.Top[1].Certain {
		t.Fatalf("exact topk ranking = %v", ans3.Top)
	}
}

func TestFailureCountersAndDeadline(t *testing.T) {
	shards := []ShardSpec{
		{Name: "ok", MaxRows: 100, MaxValue: 10},
		{Name: "err", MaxRows: 100, MaxValue: 10},
		{Name: "slow", MaxRows: 100, MaxValue: 10},
		{Name: "bad", MaxRows: 100, MaxValue: 10},
		{Name: "dup", MaxRows: 100, MaxValue: 10},
		{Name: "late", MaxRows: 100, MaxValue: 10},
	}
	scripts := map[string]shardScript{
		"ok":   {ok: true, result: ShardResult{Count: 1}},
		"err":  {ok: false, err: errShard},
		"slow": {block: true},
		"bad":  {ok: true, result: ShardResult{Count: 101}},
		"dup":  {ok: true, result: ShardResult{Count: 1}, dups: 2},
		"late": {ok: true, delay: 80 * time.Millisecond, result: ShardResult{Count: 2}},
	}
	ans, st, _ := runScripted(t, "four-fault-classes", AggCount, 0, 6, 40*time.Millisecond, shards, scripts)
	// ok and dup produce usable results; err fails; slow/late time out; bad
	// violates its bound; dup emitted 3 times -> 2 duplicates counted.
	if st.Succeeded != 2 {
		t.Fatalf("succeeded = %d, want 2: %+v", st.Succeeded, st)
	}
	if st.Failed != 1 || st.TimedOut != 2 || st.BoundViolation != 1 || st.Duplicate != 2 {
		t.Fatalf("fault counters = %+v, want failed=1 timedOut=2 boundViolation=1 duplicate=2", st)
	}
	// Late result (2 rows) must not alter the answer: lower 1+1=2, upper adds
	// slack for every non-successful shard (err, slow, bad, late: 4*100).
	if ans.Lower != 2 || ans.Upper != 402 {
		t.Fatalf("answer after deadline = [%d,%d], want [2,402]", ans.Lower, ans.Upper)
	}
}

// slowClient releases a gate once every shard has started, proving that all
// requests were simultaneously in flight.
type slowClient struct {
	started    atomic.Int64
	allStarted chan struct{}
	once       sync.Once
	total      int
	hold       time.Duration
}

func (c *slowClient) Query(ctx context.Context, spec ShardSpec, agg Aggregation, k int, emit func(ShardResult)) error {
	if c.started.Add(1) == int64(c.total) {
		c.once.Do(func() { close(c.allStarted) })
	}
	select {
	case <-time.After(c.hold):
	case <-ctx.Done():
		return ctx.Err()
	}
	emit(ShardResult{Count: 1, Sum: 1, Min: 1, Max: 1, Top: []Pair{{spec.Name, 1}}})
	return nil
}

func TestPeakConcurrency(t *testing.T) {
	for _, conc := range []int{1, 2, 5} {
		total := 6
		client := &slowClient{
			allStarted: make(chan struct{}),
			total:      conc, // gate fires once conc-limited batch is running
			hold:       30 * time.Millisecond,
		}
		req := Request{
			Shards:      testSpecs("s0", "s1", "s2", "s3", "s4", "s5")[:total],
			Agg:         AggCount,
			Concurrency: conc,
			Deadline:    time.Second,
			Client:      client,
		}
		ans, st, err := Execute(context.Background(), req)
		if err != nil {
			t.Fatalf("conc=%d: %v", conc, err)
		}
		logCase(t, "peak-concurrency", req, ans, st, nil)
		if st.PeakConcurrent != conc {
			t.Fatalf("conc=%d: peak = %d, want %d", conc, st.PeakConcurrent, conc)
		}
		select {
		case <-client.allStarted:
		default:
			t.Fatalf("conc=%d: never observed %d simultaneous in-flight requests", conc, conc)
		}
		if st.Succeeded != total {
			t.Fatalf("conc=%d: succeeded = %d, want %d", conc, st.Succeeded, total)
		}
	}
}

func TestRejections(t *testing.T) {
	good := Request{
		Shards:      testSpecs("a"),
		Agg:         AggCount,
		Concurrency: 1,
		Deadline:    time.Second,
		Client:      newScriptedClient(map[string]shardScript{"a": {ok: true, result: ShardResult{Count: 1}}}),
	}
	baseline := good
	if _, _, err := Execute(context.Background(), baseline); err != nil {
		t.Fatalf("baseline should pass: %v", err)
	}

	cases := []struct {
		name string
		mut  func(*Request)
		want RejectKind
	}{
		{"empty shards", func(r *Request) { r.Shards = nil }, RejectEmptyShards},
		{"duplicate names", func(r *Request) { r.Shards = testSpecs("a", "a") }, RejectDuplicateShardName},
		{"bad k", func(r *Request) { r.Agg = AggTopK; r.K = 0 }, RejectInvalidK},
		{"bad concurrency", func(r *Request) { r.Concurrency = 0 }, RejectInvalidConcurrency},
		{"bad deadline", func(r *Request) { r.Deadline = 0 }, RejectInvalidDeadline},
		{"unknown agg", func(r *Request) { r.Agg = "median" }, RejectUnknownAggregation},
		{"nil client", func(r *Request) { r.Client = nil }, RejectNilClient},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			req := good
			tc.mut(&req)
			_, _, err := Execute(context.Background(), req)
			var re *RejectError
			if !errors.As(err, &re) {
				t.Fatalf("want *RejectError, got %v", err)
			}
			if re.Kind != tc.want {
				t.Fatalf("kind = %s, want %s", re.Kind, tc.want)
			}
			logCase(t, "reject-"+tc.name, req, Answer{}, Stats{}, err)
		})
	}
}

func TestNoConclusion(t *testing.T) {
	shards := testSpecs("a", "b")
	scripts := map[string]shardScript{
		"a": {ok: false, err: errShard},
		"b": {block: true},
	}
	for _, agg := range []Aggregation{AggCount, AggSum, AggMin, AggMax, AggTopK} {
		ans, st, _ := runScripted(t, "no-conclusion-"+string(agg), agg, 3, 2, 50*time.Millisecond, shards, scripts)
		if ans.Conclusive || ans.Exact {
			t.Fatalf("%s with no successful shard must be inconclusive: %+v", agg, ans)
		}
		if ans.HasLower || ans.HasUpper || len(ans.Top) != 0 {
			t.Fatalf("%s inconclusive answer must expose no endpoints: %+v", agg, ans)
		}
		if st.Succeeded != 0 {
			t.Fatalf("%s: succeeded = %d", agg, st.Succeeded)
		}
	}
}

// orderClient delivers results on a schedule chosen by the test; two
// schedules that reverse arrival order must yield identical answers.
type orderClient struct {
	delays map[string]time.Duration
	res    map[string]ShardResult
}

func (c *orderClient) Query(ctx context.Context, spec ShardSpec, agg Aggregation, k int, emit func(ShardResult)) error {
	select {
	case <-time.After(c.delays[spec.Name]):
		emit(c.res[spec.Name])
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func TestOrderIndependence(t *testing.T) {
	shards := []ShardSpec{
		{Name: "a", MaxRows: 100, MaxValue: 10},
		{Name: "b", MaxRows: 100, MaxValue: 10},
		{Name: "c", MaxRows: 100, MaxValue: 10},
	}
	res := map[string]ShardResult{
		"a": {Count: 7, Sum: 70, Min: 2, Max: 9, Top: []Pair{{"a1", 9}, {"a2", 3}}},
		"b": {Count: 4, Sum: 30, Min: 5, Max: 6, Top: []Pair{{"b1", 8}}},
		"c": {Count: 9, Sum: 10, Min: 1, Max: 7, Top: []Pair{{"c1", 8}, {"c2", 1}}},
	}
	orderA := map[string]time.Duration{"a": 5 * time.Millisecond, "b": 20 * time.Millisecond, "c": 35 * time.Millisecond}
	orderB := map[string]time.Duration{"c": 5 * time.Millisecond, "b": 20 * time.Millisecond, "a": 35 * time.Millisecond}

	for _, agg := range []Aggregation{AggCount, AggSum, AggMin, AggMax, AggTopK} {
		reqA := Request{shards, agg, 2, 3, time.Second, &orderClient{orderA, res}}
		reqB := Request{shards, agg, 2, 3, time.Second, &orderClient{orderB, res}}
		ansA, stA, errA := Execute(context.Background(), reqA)
		ansB, stB, errB := Execute(context.Background(), reqB)
		if errA != nil || errB != nil {
			t.Fatalf("%s: unexpected errors %v %v", agg, errA, errB)
		}
		logCase(t, "order-A-"+string(agg), reqA, ansA, stA, nil)
		logCase(t, "order-B-"+string(agg), reqB, ansB, stB, nil)
		if !answersEqual(ansA, ansB) {
			t.Fatalf("%s: answers differ by arrival order:\nA=%+v\nB=%+v", agg, ansA, ansB)
		}
		if stA != stB {
			t.Fatalf("%s: stats differ by arrival order: %+v vs %+v", agg, stA, stB)
		}
	}
}

func answersEqual(a, b Answer) bool {
	if a.Agg != b.Agg || a.Exact != b.Exact || a.Conclusive != b.Conclusive ||
		a.Lower != b.Lower || a.Upper != b.Upper ||
		a.HasLower != b.HasLower || a.HasUpper != b.HasUpper ||
		len(a.Top) != len(b.Top) {
		return false
	}
	for i := range a.Top {
		if a.Top[i] != b.Top[i] {
			return false
		}
	}
	return true
}
