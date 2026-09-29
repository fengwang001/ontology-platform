package metrics

import (
	"errors"
	"math"
	"sync"
	"testing"
	"time"
)

type fakeClock struct {
	mu sync.Mutex
	t  time.Time
}

func newFakeClock() *fakeClock {
	return &fakeClock{t: time.Unix(1_000_000, 0)}
}

func (c *fakeClock) now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.t
}

func (c *fakeClock) advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.t = c.t.Add(d)
}

// testLogger routes every input/output/decision audit line to t.Logf, so
// `go test -v` shows inputs, outputs and the reason behind each decision.
type testLogger struct{ t *testing.T }

func (l testLogger) Printf(format string, args ...any) {
	l.t.Helper()
	l.t.Logf(format, args...)
}

func newTestRegistry(t *testing.T, maxSeries int, ttl time.Duration) (*Registry, *fakeClock) {
	t.Helper()
	clock := newFakeClock()
	r, err := NewRegistry(maxSeries, ttl, clock.now, testLogger{t})
	if err != nil {
		t.Fatalf("NewRegistry: %v", err)
	}
	return r, clock
}

func labelsOf(pairs ...string) map[string]string {
	if len(pairs)%2 != 0 {
		panic("pairs must be key/value")
	}
	m := make(map[string]string, len(pairs)/2)
	for i := 0; i < len(pairs); i += 2 {
		m[pairs[i]] = pairs[i+1]
	}
	return m
}

func seriesSum(snap MetricSnapshot) float64 {
	var sum float64
	for _, s := range snap.Series {
		sum += s.Count
	}
	return sum
}

func findMetric(t *testing.T, snaps []MetricSnapshot, name string) MetricSnapshot {
	t.Helper()
	for _, s := range snaps {
		if s.Name == name {
			return s
		}
	}
	t.Fatalf("metric %q missing from export", name)
	return MetricSnapshot{}
}

func overflowSeries(snap MetricSnapshot) *SeriesSnapshot {
	for i := range snap.Series {
		if snap.Series[i].Overflow {
			return &snap.Series[i]
		}
	}
	return nil
}

func rejectReason(t *testing.T, err error) Reason {
	t.Helper()
	var re *RejectError
	if !errors.As(err, &re) {
		t.Fatalf("expected *RejectError, got %v", err)
	}
	return re.Reason
}

func equalFloat(a, b []float64) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// TestRegisterIdempotentAndConflicting covers idempotent re-registration
// (label order independent) versus same-name/different-definition rejection.
func TestRegisterIdempotentAndConflicting(t *testing.T) {
	r, _ := newTestRegistry(t, 2, time.Minute)

	def := Definition{Name: "reqs", Kind: KindCounter, AllowedLabels: []string{"zone", "svc"}}
	if err := r.Register(def); err != nil {
		t.Fatalf("register: %v", err)
	}
	// Same definition with labels in a different order: idempotent.
	def.AllowedLabels = []string{"svc", "zone"}
	if err := r.Register(def); err != nil {
		t.Fatalf("idempotent register: %v", err)
	}

	conflict := Definition{Name: "reqs", Kind: KindHistogram, AllowedLabels: []string{"zone", "svc"}, BucketBounds: []float64{1}}
	err := r.Register(conflict)
	if err == nil {
		t.Fatal("conflicting registration must be rejected")
	}
	if got := rejectReason(t, err); got != RejectDuplicateDef {
		t.Fatalf("reason = %s, want %s", got, RejectDuplicateDef)
	}

	// Existing counter must still work: the rejected registration changed nothing.
	if err := r.AddCounter("reqs", labelsOf("zone", "a", "svc", "b"), 1); err != nil {
		t.Fatalf("counter after rejected re-register: %v", err)
	}
	if got := r.RejectionCounts()[RejectDuplicateDef]; got != 1 {
		t.Fatalf("duplicate-def tally = %d, want 1", got)
	}
}

// TestCounterCapAndOverflow fills all slots, folds later label sets into the
// single overflow series and checks the exact per-metric sum invariant.
func TestCounterCapAndOverflow(t *testing.T) {
	r, _ := newTestRegistry(t, 2, time.Minute)
	if err := r.Register(Definition{Name: "reqs", Kind: KindCounter, AllowedLabels: []string{"zone"}}); err != nil {
		t.Fatal(err)
	}

	mustAdd := func(zone string, v float64) {
		t.Helper()
		if err := r.AddCounter("reqs", labelsOf("zone", zone), v); err != nil {
			t.Fatalf("add %s: %v", zone, err)
		}
	}
	mustAdd("a", 1)
	mustAdd("b", 2)
	mustAdd("a", 10) // ordinary, keeps accumulating
	mustAdd("c", 4)  // cap reached -> overflow
	mustAdd("d", 5)  // overflow again, same unique series

	snap := findMetric(t, r.Export(), "reqs")
	if len(snap.Series) != 3 {
		t.Fatalf("series = %d, want 2 ordinary + 1 overflow", len(snap.Series))
	}
	ov := overflowSeries(snap)
	if ov == nil || ov.Count != 9 {
		t.Fatalf("overflow count = %v, want 9", ov)
	}
	if snap.Total != 22 || seriesSum(snap) != snap.Total {
		t.Fatalf("total=%v seriesSum=%v, want both 22", snap.Total, seriesSum(snap))
	}
}

// TestHistogramBoundariesAndCumulative checks exact-boundary bucket placement,
// overflow folding and cumulative export whose last bucket equals the total.
func TestHistogramBoundariesAndCumulative(t *testing.T) {
	r, _ := newTestRegistry(t, 1, time.Minute)
	if err := r.Register(Definition{
		Name:          "latency",
		Kind:          KindHistogram,
		AllowedLabels: []string{"route"},
		BucketBounds:  []float64{1, 2, 4}, // plus implicit +Inf bucket
	}); err != nil {
		t.Fatal(err)
	}

	mustObserve := func(route string, v float64) {
		t.Helper()
		if err := r.Observe("latency", labelsOf("route", route), v); err != nil {
			t.Fatalf("observe %v: %v", v, err)
		}
	}
	mustObserve("x", 1) // == first bound -> bucket 0
	mustObserve("x", 2) // == second bound -> bucket 1
	mustObserve("x", 4) // == third bound -> bucket 2
	mustObserve("x", 4)
	mustObserve("y", 100) // no free slot -> overflow, implicit +Inf bucket
	mustObserve("y", 0)   // overflow, bucket 0

	snap := findMetric(t, r.Export(), "latency")
	ordinary := snap.Series[0]
	if !equalFloat(ordinary.Buckets, []float64{1, 2, 4, 4}) {
		t.Fatalf("ordinary cumulative buckets = %v, want [1 2 4 4]", ordinary.Buckets)
	}
	ov := overflowSeries(snap)
	if !equalFloat(ov.Buckets, []float64{1, 1, 1, 2}) {
		t.Fatalf("overflow cumulative buckets = %v, want [1 1 1 2]", ov.Buckets)
	}
	if !equalFloat(snap.BucketTotals, []float64{2, 3, 5, 6}) {
		t.Fatalf("metric bucket totals = %v, want [2 3 5 6]", snap.BucketTotals)
	}
	if snap.Total != 6 || seriesSum(snap) != 6 || snap.BucketTotals[len(snap.BucketTotals)-1] != 6 {
		t.Fatalf("conservation broken: total=%v seriesSum=%v lastBucket=%v",
			snap.Total, seriesSum(snap), snap.BucketTotals[len(snap.BucketTotals)-1])
	}
}

// TestReclaimMergesAndSlotReuse verifies idle recycling merges a series into
// overflow, frees its slot, lets a new set take it, and restarts the old set
// from zero.
func TestReclaimMergesAndSlotReuse(t *testing.T) {
	r, clock := newTestRegistry(t, 1, time.Minute)
	if err := r.Register(Definition{Name: "reqs", Kind: KindCounter, AllowedLabels: []string{"zone"}}); err != nil {
		t.Fatal(err)
	}

	if err := r.AddCounter("reqs", labelsOf("zone", "a"), 7); err != nil {
		t.Fatal(err)
	}
	clock.advance(30 * time.Second)
	if n := r.ReclaimIdle(); n != 0 {
		t.Fatalf("reclaimed %d before TTL, want 0", n)
	}
	clock.advance(31 * time.Second) // 61s > 1m
	if n := r.ReclaimIdle(); n != 1 {
		t.Fatalf("reclaimed = %d, want 1", n)
	}

	snap := findMetric(t, r.Export(), "reqs")
	if len(snap.Series) != 1 || !snap.Series[0].Overflow || snap.Series[0].Count != 7 {
		t.Fatalf("after reclaim want only overflow with count 7, got %+v", snap.Series)
	}
	if snap.Total != 7 {
		t.Fatalf("total = %v, want 7", snap.Total)
	}

	// New label set takes the freed slot; the old set reappears at zero and,
	// with the slot taken, folds into overflow.
	if err := r.AddCounter("reqs", labelsOf("zone", "b"), 3); err != nil {
		t.Fatal(err)
	}
	if err := r.AddCounter("reqs", labelsOf("zone", "a"), 2); err != nil {
		t.Fatal(err)
	}
	snap = findMetric(t, r.Export(), "reqs")
	if len(snap.Series) != 2 {
		t.Fatalf("series = %d, want ordinary+overflow", len(snap.Series))
	}
	ov := overflowSeries(snap)
	if ov.Count != 9 { // merged 7 + restarted a=2
		t.Fatalf("overflow = %v, want 9", ov.Count)
	}
	var ordinary *SeriesSnapshot
	for i := range snap.Series {
		if !snap.Series[i].Overflow {
			ordinary = &snap.Series[i]
		}
	}
	if ordinary == nil || ordinary.Labels["zone"] != "b" || ordinary.Count != 3 {
		t.Fatalf("ordinary = %+v, want zone=b count=3", ordinary)
	}
	if seriesSum(snap) != snap.Total || snap.Total != 12 {
		t.Fatalf("total=%v seriesSum=%v, want both 12 (7+3+2)", snap.Total, seriesSum(snap))
	}
}

// TestInvalidReports covers every rejection class and proves rejected
// registrations/reports leave state untouched.
func TestInvalidReports(t *testing.T) {
	r, _ := newTestRegistry(t, 2, time.Minute)
	mustRegister := func(def Definition) {
		t.Helper()
		if err := r.Register(def); err != nil {
			t.Fatalf("register %s: %v", def.Name, err)
		}
	}
	mustRegister(Definition{Name: "c", Kind: KindCounter, AllowedLabels: []string{"zone"}})
	mustRegister(Definition{Name: "h", Kind: KindHistogram, AllowedLabels: []string{"zone"}, BucketBounds: []float64{1, 2}})

	wantReject := func(do func() error, reason Reason) {
		t.Helper()
		err := do()
		if err == nil {
			t.Fatalf("expected rejection %s", reason)
		}
		if got := rejectReason(t, err); got != reason {
			t.Fatalf("reason = %s, want %s", got, reason)
		}
	}

	wantReject(func() error { return r.AddCounter("nope", labelsOf("zone", "a"), 1) }, RejectUnknownMetric)
	wantReject(func() error { return r.Observe("nope", labelsOf("zone", "a"), 1) }, RejectUnknownMetric)
	wantReject(func() error { return r.AddCounter("c", labelsOf("zone", "a", "extra", "x"), 1) }, RejectLabelNotAllowed)
	wantReject(func() error { return r.AddCounter("c", map[string]string{}, 1) }, RejectLabelMissing)
	wantReject(func() error { return r.AddCounter("c", labelsOf("zone", "a"), -1) }, RejectCounterNegative)
	wantReject(func() error { return r.AddCounter("c", labelsOf("zone", "a"), math.NaN()) }, RejectValueNaN)
	wantReject(func() error { return r.Observe("h", labelsOf("zone", "a"), math.NaN()) }, RejectValueNaN)
	wantReject(func() error { return r.Observe("c", labelsOf("zone", "a"), 1) }, RejectKindMismatch)
	wantReject(func() error { return r.AddCounter("h", labelsOf("zone", "a"), 1) }, RejectKindMismatch)

	badDefs := []Definition{
		{Name: "b1", Kind: KindHistogram, BucketBounds: []float64{2, 1}},
		{Name: "b2", Kind: KindHistogram, BucketBounds: []float64{1, 1}},
		{Name: "b3", Kind: KindHistogram, BucketBounds: []float64{math.NaN()}},
		{Name: "b4", Kind: KindHistogram, BucketBounds: nil},
		{Name: "b5", Kind: Kind("gauge")},
	}
	for _, def := range badDefs {
		err := r.Register(def)
		if err == nil {
			t.Fatalf("bad registration %+v accepted", def)
		}
		got := rejectReason(t, err)
		if got != RejectBadBuckets && got != RejectKindMismatch {
			t.Fatalf("bad def %s reason = %s", def.Name, got)
		}
	}

	// Nothing rejected above could create a metric or move a counter.
	snap := findMetric(t, r.Export(), "c")
	if snap.Total != 0 || len(snap.Series) != 0 {
		t.Fatalf("counter state changed by rejected reports: %+v", snap)
	}
	if _, ok := indexMetric(r.Export(), "b1"); ok {
		t.Fatal("rejected histogram registration created state")
	}
}

func indexMetric(snaps []MetricSnapshot, name string) (MetricSnapshot, bool) {
	for _, s := range snaps {
		if s.Name == name {
			return s, true
		}
	}
	return MetricSnapshot{}, false
}

// TestConcurrentConservation hammers counters and histograms from many
// goroutines while exporting concurrently, with periodic reclamation; every
// observed snapshot must satisfy: sum(series) == accepted total and the last
// cumulative bucket equals the histogram total.
func TestConcurrentConservation(t *testing.T) {
	r, clock := newTestRegistry(t, 4, 50*time.Millisecond)
	if err := r.Register(Definition{Name: "reqs", Kind: KindCounter, AllowedLabels: []string{"zone"}}); err != nil {
		t.Fatal(err)
	}
	if err := r.Register(Definition{
		Name:          "lat",
		Kind:          KindHistogram,
		AllowedLabels: []string{"zone"},
		BucketBounds:  []float64{1, 10, 100},
	}); err != nil {
		t.Fatal(err)
	}

	const writers = 16
	const perWriter = 500 // uses zones 0..9 -> at most 4 ordinary, rest overflow

	stop := make(chan struct{})
	var exporterWg sync.WaitGroup
	exporterWg.Add(1)
	go func() {
		defer exporterWg.Done()
		for {
			select {
			case <-stop:
				return
			default:
				for _, snap := range r.Export() {
					if seriesSum(snap) != snap.Total {
						t.Errorf("inconsistent snapshot for %s: sum=%v total=%v", snap.Name, seriesSum(snap), snap.Total)
						return
					}
					if snap.Kind == KindHistogram && snap.BucketTotals[len(snap.BucketTotals)-1] != snap.Total {
						t.Errorf("histogram %s last bucket %v != total %v", snap.Name, snap.BucketTotals[len(snap.BucketTotals)-1], snap.Total)
						return
					}
				}
			}
		}
	}()

	reclaimerDone := make(chan struct{})
	go func() {
		defer close(reclaimerDone)
		for {
			select {
			case <-stop:
				return
			default:
				clock.advance(60 * time.Millisecond)
				r.ReclaimIdle()
			}
		}
	}()

	var wg sync.WaitGroup
	for w := 0; w < writers; w++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			for i := 0; i < perWriter; i++ {
				zone := "z" + itoa((id+i)%10)
				if err := r.AddCounter("reqs", labelsOf("zone", zone), 1); err != nil {
					t.Errorf("add: %v", err)
					return
				}
				v := float64((id*7 + i*3) % 200) // spans all four buckets
				if err := r.Observe("lat", labelsOf("zone", zone), v); err != nil {
					t.Errorf("observe: %v", err)
					return
				}
			}
		}(w)
	}
	wg.Wait()
	close(stop)
	exporterWg.Wait()
	<-reclaimerDone

	// Drain any still-idle series into overflow and make the final check.
	clock.advance(time.Hour)
	r.ReclaimIdle()
	for _, name := range []string{"reqs", "lat"} {
		snap := findMetric(t, r.Export(), name)
		if want := float64(writers * perWriter); snap.Total != want {
			t.Fatalf("%s total = %v, want %v", name, snap.Total, want)
		}
		if seriesSum(snap) != snap.Total {
			t.Fatalf("%s final series sum %v != total %v", name, seriesSum(snap), snap.Total)
		}
		if snap.Kind == KindHistogram && snap.BucketTotals[len(snap.BucketTotals)-1] != snap.Total {
			t.Fatalf("%s final last bucket != total", name)
		}
	}
}

// TestSerialReplayDeterminism replays the exact same ordered report sequence
// twice (with the same reclaim timings) and requires an identical partition
// into ordinary and overflow series.
func TestSerialReplayDeterminism(t *testing.T) {
	type op struct {
		zone string
		v    float64
		wait time.Duration // advance clock, then maybe reclaim
	}
	ops := []op{
		{"a", 1, 0}, {"b", 2, 0}, {"c", 3, 0}, {"a", 4, 0},
		{"d", 5, 61 * time.Second},
		{"a", 6, 0}, {"e", 7, 0}, {"b", 8, 61 * time.Second},
		{"f", 9, 0}, {"a", 10, 0},
	}
	run := func() []SeriesSnapshot {
		r, clock := newTestRegistry(t, 2, time.Minute)
		if err := r.Register(Definition{Name: "m", Kind: KindCounter, AllowedLabels: []string{"zone"}}); err != nil {
			t.Fatal(err)
		}
		for _, o := range ops {
			if err := r.AddCounter("m", labelsOf("zone", o.zone), o.v); err != nil {
				t.Fatal(err)
			}
			if o.wait > 0 {
				clock.advance(o.wait)
				r.ReclaimIdle()
			}
		}
		return findMetric(t, r.Export(), "m").Series
	}

	first := run()
	second := run()
	if len(first) != len(second) {
		t.Fatalf("partition length differs: %d vs %d", len(first), len(second))
	}
	for i := range first {
		if first[i].Overflow != second[i].Overflow ||
			first[i].Count != second[i].Count ||
			first[i].Labels["zone"] != second[i].Labels["zone"] {
			t.Fatalf("series %d differs:\n%+v\nvs\n%+v", i, first[i], second[i])
		}
	}
	var total float64
	for _, o := range ops {
		total += o.v
	}
	var sum float64
	for _, s := range first {
		sum += s.Count
	}
	if sum != total {
		t.Fatalf("replayed sum %v != %v", sum, total)
	}
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var buf [20]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	return string(buf[i:])
}
