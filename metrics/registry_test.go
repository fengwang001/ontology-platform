package metrics

import (
	"errors"
	"fmt"
	"math"
	"strings"
	"sync"
	"testing"
	"time"
)

// fakeClock 是可手动推进的注入时钟；并发测试下也可安全读写。
type fakeClock struct {
	mu sync.Mutex
	t  time.Time
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

// captureLogger 捕获日志，测试中可断言打印了输入、输出与判定依据。
type captureLogger struct {
	mu   sync.Mutex
	logs strings.Builder
}

func (l *captureLogger) Printf(format string, args ...any) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.logs.WriteString(fmt.Sprintf(format, args...))
	l.logs.WriteByte('\n')
}

func (l *captureLogger) String() string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.logs.String()
}

func newTestRegistry(t *testing.T, n int, ttl time.Duration) (*Registry, *fakeClock, *captureLogger) {
	t.Helper()
	clk := &fakeClock{t: time.Date(2026, 9, 30, 10, 0, 0, 0, time.UTC)}
	lg := &captureLogger{}
	r, err := New(n, ttl, WithClock(clk.now), WithLogger(lg))
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return r, clk, lg
}

func rejectReason(t *testing.T, err error) RejectReason {
	t.Helper()
	var rej *RejectError
	if !errors.As(err, &rej) {
		t.Fatalf("expected *RejectError, got %T: %v", err, err)
	}
	return rej.Reason
}

func findMetric(snap Snapshot, name string) MetricSnapshot {
	for _, m := range snap.Metrics {
		if m.Name == name {
			return m
		}
	}
	panic("metric not found: " + name)
}

func counterConservation(t *testing.T, m MetricSnapshot, wantTotal int64) {
	t.Helper()
	var sum int64
	for _, s := range m.Series {
		sum += s.Value
	}
	if m.Overflow != nil {
		sum += m.Overflow.Value
	}
	if sum != wantTotal || m.AcceptedTotal != wantTotal {
		t.Fatalf("%s conservation: series sum=%d accepted=%d want=%d", m.Name, sum, m.AcceptedTotal, wantTotal)
	}
}

// histogramConservation 核对每个序列的累计桶计数单调且末桶等于总数，
// 并核对各序列之和等于已接受总数。
func histogramConservation(t *testing.T, m MetricSnapshot) {
	t.Helper()
	var totalCount uint64
	check := func(s SeriesSnapshot) {
		if len(s.BucketCounts) == 0 || s.BucketCounts[len(s.BucketCounts)-1] != s.Count {
			t.Fatalf("%s cumulative last bucket=%v count=%d", m.Name, s.BucketCounts, s.Count)
		}
		for i := 1; i < len(s.BucketCounts); i++ {
			if s.BucketCounts[i] < s.BucketCounts[i-1] {
				t.Fatalf("%s bucket counts not cumulative: %v", m.Name, s.BucketCounts)
			}
		}
		totalCount += s.Count
	}
	for _, s := range m.Series {
		check(s)
	}
	if m.Overflow != nil {
		check(*m.Overflow)
	}
	if totalCount != uint64(m.AcceptedTotal) {
		t.Fatalf("%s count total=%d accepted=%d", m.Name, totalCount, m.AcceptedTotal)
	}
}

// TestRegisterIdempotentAndConflicts 覆盖幂等注册、同名不同定义、桶上界非法。
func TestRegisterIdempotentAndConflicts(t *testing.T) {
	r, _, lg := newTestRegistry(t, 2, time.Minute)

	def := Definition{Type: Counter, Labels: []string{"svc", "host"}}
	if err := r.Register("req", def); err != nil {
		t.Fatalf("register: %v", err)
	}
	// 标签顺序不同仍是同一个定义。
	if err := r.Register("req", Definition{Type: Counter, Labels: []string{"host", "svc"}}); err != nil {
		t.Fatalf("idempotent register: %v", err)
	}
	// 同名不同类型。
	err := r.Register("req", Definition{Type: Histogram, Labels: []string{"svc", "host"}, Buckets: []float64{1}})
	if got := rejectReason(t, err); got != ReasonDefinitionMismatch {
		t.Fatalf("want %s, got %s", ReasonDefinitionMismatch, got)
	}
	// 同名不同标签集合。
	err = r.Register("req", Definition{Type: Counter, Labels: []string{"svc"}})
	if got := rejectReason(t, err); got != ReasonDefinitionMismatch {
		t.Fatalf("want %s, got %s", ReasonDefinitionMismatch, got)
	}
	if n := len(findMetric(r.Export(), "req").Series); n != 0 {
		t.Fatalf("rejected registration changed state: %d series", n)
	}

	// 桶上界不严格递增 / 非有限数必须整体拒绝。
	for _, buckets := range [][]float64{
		{1, 1},
		{2, 1},
		{1, math.NaN()},
		{1, math.Inf(1)},
	} {
		err := r.Register("h"+fmt.Sprint(buckets), Definition{Type: Histogram, Labels: nil, Buckets: buckets})
		if got := rejectReason(t, err); got != ReasonBucketsNotSorted {
			t.Fatalf("buckets %v: want %s, got %s", buckets, ReasonBucketsNotSorted, got)
		}
	}
	// 合法直方图注册成功后，重复注册（含桶上界相同）幂等。
	good := []float64{1, 2.5, 10}
	if err := r.Register("lat", Definition{Type: Histogram, Labels: []string{"svc"}, Buckets: good}); err != nil {
		t.Fatalf("register histogram: %v", err)
	}
	if err := r.Register("lat", Definition{Type: Histogram, Labels: []string{"svc"}, Buckets: append([]float64(nil), good...)}); err != nil {
		t.Fatalf("idempotent histogram: %v", err)
	}
	logs := lg.String()
	if !strings.Contains(logs, "idempotent") || !strings.Contains(logs, "rejected") {
		t.Fatalf("logs missing input/decision evidence:\n%s", logs)
	}
}

// TestCounterOverflowFold 覆盖满额后新序列折叠进唯一溢出序列。
func TestCounterOverflowFold(t *testing.T) {
	r, clk, _ := newTestRegistry(t, 2, time.Hour)
	if err := r.Register("req", Definition{Type: Counter, Labels: []string{"host"}}); err != nil {
		t.Fatal(err)
	}

	mustAdd := func(host string, delta int64) {
		t.Helper()
		if err := r.AddCounter("req", Labels{"host": host}, delta); err != nil {
			t.Fatalf("add %s: %v", host, err)
		}
		clk.advance(time.Second)
	}
	mustAdd("a", 5)
	mustAdd("b", 7)
	mustAdd("a", 3)
	// 名额已满：c、d 全部折叠进唯一溢出序列。
	mustAdd("c", 11)
	mustAdd("d", 13)
	mustAdd("c", 2)

	m := findMetric(r.Export(), "req")
	if len(m.Series) != 2 {
		t.Fatalf("want 2 normal series, got %d", len(m.Series))
	}
	if m.Overflow == nil || m.Overflow.Value != 26 {
		t.Fatalf("overflow=%+v, want value 26", m.Overflow)
	}
	counterConservation(t, m, 41)
}

// TestHistogramBoundariesAndCumulative 覆盖桶边界 <= 语义与累计导出。
func TestHistogramBoundariesAndCumulative(t *testing.T) {
	r, _, _ := newTestRegistry(t, 1, time.Hour)
	buckets := []float64{1, 2, 4}
	if err := r.Register("lat", Definition{Type: Histogram, Labels: []string{"route"}, Buckets: buckets}); err != nil {
		t.Fatal(err)
	}
	for _, v := range []float64{1, 1, 2, 4, 4, 8, -3} {
		if err := r.Observe("lat", Labels{"route": "/x"}, v); err != nil {
			t.Fatalf("observe %g: %v", v, err)
		}
	}
	// N=1：第二个标签集合进入溢出序列。
	if err := r.Observe("lat", Labels{"route": "/y"}, 2); err != nil {
		t.Fatal(err)
	}

	m := findMetric(r.Export(), "lat")
	got := m.Series[0].BucketCounts
	// 独立计数为 [<=1:3(1,1,-3), <=2:1(2), <=4:2(4,4), +Inf:1(8)]
	// 累计导出应为 [3 4 6 7]，末桶等于总数。
	want := []uint64{3, 4, 6, 7}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("cumulative buckets=%v, want %v", got, want)
		}
	}
	if m.Overflow.BucketCounts[len(buckets)] != 1 {
		t.Fatalf("overflow cumulative tail=%v, want 1", m.Overflow.BucketCounts)
	}
	histogramConservation(t, m)
}

// TestReclaimFreesSlotAndRestartsAtZero 覆盖回收并入溢出、释放名额、
// 旧标签集合再现时作为新序列从零开始。
func TestReclaimFreesSlotAndRestartsAtZero(t *testing.T) {
	r, clk, _ := newTestRegistry(t, 2, 10*time.Minute)
	if err := r.Register("req", Definition{Type: Counter, Labels: []string{"host"}}); err != nil {
		t.Fatal(err)
	}
	mustAdd := func(host string, delta int64) {
		t.Helper()
		if err := r.AddCounter("req", Labels{"host": host}, delta); err != nil {
			t.Fatal(err)
		}
	}
	mustAdd("a", 5)
	mustAdd("b", 7)

	// 推进到恰为 T：空闲 10m 整，必须保留；再推进 1ns 后回收。
	clk.advance(10 * time.Minute)
	if n := r.ReclaimExpired(); n != 0 {
		t.Fatalf("idle == T must be kept, reclaimed=%d", n)
	}
	clk.advance(1)
	if n := r.ReclaimExpired(); n != 2 {
		t.Fatalf("want 2 reclaimed, got %d", n)
	}

	m := findMetric(r.Export(), "req")
	if len(m.Series) != 0 || m.Overflow == nil || m.Overflow.Value != 12 {
		t.Fatalf("after reclaim: series=%d overflow=%+v", len(m.Series), m.Overflow)
	}
	counterConservation(t, m, 12)

	// 名额释放：新集合 c 占名额；旧集合 a 再现，作为新序列从零开始。
	mustAdd("c", 100)
	mustAdd("a", 1)
	m = findMetric(r.Export(), "req")
	if len(m.Series) != 2 {
		t.Fatalf("want 2 new series, got %d", len(m.Series))
	}
	values := map[string]int64{}
	for _, s := range m.Series {
		values[s.Labels["host"]] = s.Value
	}
	if values["c"] != 100 || values["a"] != 1 {
		t.Fatalf("new series values=%v, want a=1 c=100", values)
	}
	// 溢出仍持有回收前的 12；总和为 12 + 100 + 1。
	if m.Overflow.Value != 12 {
		t.Fatalf("overflow changed: %d", m.Overflow.Value)
	}
	counterConservation(t, m, 113)
}

// TestReclaimHistogramMergesBuckets 覆盖直方图回收时桶累计也并入溢出。
func TestReclaimHistogramMergesBuckets(t *testing.T) {
	r, clk, _ := newTestRegistry(t, 1, time.Minute)
	if err := r.Register("lat", Definition{Type: Histogram, Labels: []string{"route"}, Buckets: []float64{1, 10}}); err != nil {
		t.Fatal(err)
	}
	mustObs := func(route string, v float64) {
		t.Helper()
		if err := r.Observe("lat", Labels{"route": route}, v); err != nil {
			t.Fatal(err)
		}
	}
	mustObs("/a", 0.5)
	mustObs("/a", 5)
	mustObs("/b", 50) // N=1，/b 直接进溢出

	clk.advance(time.Minute + time.Nanosecond)
	if n := r.ReclaimExpired(); n != 1 {
		t.Fatalf("reclaimed=%d", n)
	}
	m := findMetric(r.Export(), "lat")
	if len(m.Series) != 0 {
		t.Fatalf("want 0 active, got %d", len(m.Series))
	}
	// /a 独立计数 [1,1,0] + /b [0,0,1] => 累计 [1 2 3]。
	want := []uint64{1, 2, 3}
	for i := range want {
		if m.Overflow.BucketCounts[i] != want[i] {
			t.Fatalf("overflow buckets=%v want %v", m.Overflow.BucketCounts, want)
		}
	}
	histogramConservation(t, m)
}
