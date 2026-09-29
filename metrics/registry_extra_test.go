package metrics

import (
	"fmt"
	"math"
	"sync"
	"testing"
	"time"
)

// TestInvalidReports 覆盖各类非法上报，且拒绝不得改变任何状态。
func TestInvalidReports(t *testing.T) {
	r, _, _ := newTestRegistry(t, 2, time.Hour)
	if err := r.Register("req", Definition{Type: Counter, Labels: []string{"host", "svc"}}); err != nil {
		t.Fatal(err)
	}
	if err := r.Register("lat", Definition{Type: Histogram, Labels: []string{"host"}, Buckets: []float64{1}}); err != nil {
		t.Fatal(err)
	}

	cases := []struct {
		name   string
		reason RejectReason
		fn     func() error
	}{
		{"counter metric missing", ReasonMetricNotRegistered, func() error {
			return r.AddCounter("nope", nil, 1)
		}},
		{"observe metric missing", ReasonMetricNotRegistered, func() error {
			return r.Observe("nope", nil, 1)
		}},
		{"label missing", ReasonLabelMissing, func() error {
			return r.AddCounter("req", Labels{"host": "h1"}, 1)
		}},
		{"label not allowed", ReasonLabelNotAllowed, func() error {
			return r.AddCounter("req", Labels{"host": "h1", "svc": "s1", "zone": "z1"}, 1)
		}},
		{"negative delta", ReasonNegativeDelta, func() error {
			return r.AddCounter("req", Labels{"host": "h1", "svc": "s1"}, -1)
		}},
		{"observe on counter", ReasonDefinitionMismatch, func() error {
			return r.Observe("req", Labels{"host": "h1", "svc": "s1"}, 1)
		}},
		{"add on histogram", ReasonDefinitionMismatch, func() error {
			return r.AddCounter("lat", Labels{"host": "h1"}, 1)
		}},
		{"nan observation", ReasonNaNObservation, func() error {
			return r.Observe("lat", Labels{"host": "h1"}, math.NaN())
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := tc.fn()
			if got := rejectReason(t, err); got != tc.reason {
				t.Fatalf("want %s, got %s", tc.reason, got)
			}
			if r.RejectedCount(tc.reason) == 0 {
				t.Fatalf("reject counter for %s not incremented", tc.reason)
			}
		})
	}

	snap := r.Export()
	for _, m := range snap.Metrics {
		if m.AcceptedTotal != 0 || len(m.Series) != 0 || m.Overflow != nil {
			t.Fatalf("rejected reports changed state for %s: %+v", m.Name, m)
		}
	}
	wantRejected := map[RejectReason]int64{
		ReasonMetricNotRegistered: 2,
		ReasonLabelMissing:        1,
		ReasonLabelNotAllowed:     1,
		ReasonNegativeDelta:       1,
		ReasonDefinitionMismatch:  2,
		ReasonNaNObservation:      1,
	}
	for reason, want := range wantRejected {
		if got := r.RejectedCount(reason); got != want {
			t.Fatalf("rejected[%s]=%d, want %d", reason, got, want)
		}
	}
}

// op 是一条可重放的确定性上报。
type op struct {
	counter bool
	host    string
	value   int64
}

func replay(t *testing.T, ops []op, n int) []MetricSnapshot {
	t.Helper()
	clk := &fakeClock{t: time.Date(2026, 9, 30, 10, 0, 0, 0, time.UTC)}
	r, err := New(n, time.Hour, WithClock(clk.now))
	if err != nil {
		t.Fatal(err)
	}
	if err := r.Register("req", Definition{Type: Counter, Labels: []string{"host"}}); err != nil {
		t.Fatal(err)
	}
	if err := r.Register("lat", Definition{Type: Histogram, Labels: []string{"host"}, Buckets: []float64{1, 10}}); err != nil {
		t.Fatal(err)
	}
	for _, o := range ops {
		var err error
		if o.counter {
			err = r.AddCounter("req", Labels{"host": o.host}, o.value)
		} else {
			err = r.Observe("lat", Labels{"host": o.host}, float64(o.value))
		}
		if err != nil {
			t.Fatalf("replay op %+v: %v", o, err)
		}
		clk.advance(time.Second)
	}
	return r.Export().Metrics
}

func snapshotsKey(ms []MetricSnapshot) string {
	key := ""
	for _, m := range ms {
		key += m.Name + "|"
		for _, s := range m.Series {
			key += fmt.Sprintf("%s=%d;", s.Labels, s.Value)
			for _, c := range s.BucketCounts {
				key += fmt.Sprintf("%d,", c)
			}
		}
		if m.Overflow != nil {
			key += fmt.Sprintf("ov=%d%v;", m.Overflow.Value, m.Overflow.BucketCounts)
		}
		key += "||"
	}
	return key
}

// TestSerialReplayDeterministic 同一上报序列串行重放必须得到完全相同的序列划分。
func TestSerialReplayDeterministic(t *testing.T) {
	ops := []op{
		{counter: true, host: "a", value: 1},
		{counter: true, host: "b", value: 2},
		{counter: true, host: "a", value: 3},
		{counter: true, host: "c", value: 4},
		{false, "a", 50},
		{false, "b", 1},
		{counter: true, host: "b", value: 6},
		{false, "c", 2},
	}
	first := snapshotsKey(replay(t, ops, 2))
	for i := 0; i < 3; i++ {
		if got := snapshotsKey(replay(t, ops, 2)); got != first {
			t.Fatalf("replay %d differs:\n%s\nvs\n%s", i, got, first)
		}
	}
}

// TestConcurrentReportsAndExports 在并发上报/导出/回收下反复核对守恒，
// 并在收敛后核对总量与桶总计。
func TestConcurrentReportsAndExports(t *testing.T) {
	const n = 4
	clk := &fakeClock{t: time.Date(2026, 9, 30, 10, 0, 0, 0, time.UTC)}
	r, err := New(n, 50*time.Millisecond, WithClock(clk.now))
	if err != nil {
		t.Fatal(err)
	}
	if err := r.Register("req", Definition{Type: Counter, Labels: []string{"host"}}); err != nil {
		t.Fatal(err)
	}
	if err := r.Register("lat", Definition{Type: Histogram, Labels: []string{"host"}, Buckets: []float64{1, 5, 10}}); err != nil {
		t.Fatal(err)
	}

	const goroutines = 8
	const perG = 200
	var workers sync.WaitGroup
	var exporterWG sync.WaitGroup

	// 上报者：host 数远大于 N，保证大量序列折叠进溢出。
	for g := 0; g < goroutines; g++ {
		workers.Add(1)
		go func(g int) {
			defer workers.Done()
			for i := 0; i < perG; i++ {
				host := fmt.Sprintf("h%d", (g*perG+i)%20)
				if err := r.AddCounter("req", Labels{"host": host}, 1); err != nil {
					t.Errorf("add: %v", err)
					return
				}
				value := float64((g + i) % 12) // 覆盖 0..11，含边界 1、5、10
				if err := r.Observe("lat", Labels{"host": host}, value); err != nil {
					t.Errorf("observe: %v", err)
					return
				}
			}
		}(g)
	}

	// 导出者：每次导出都必须是一致快照且守恒。
	stop := make(chan struct{})
	exporterWG.Add(1)
	go func() {
		defer exporterWG.Done()
		for {
			select {
			case <-stop:
				return
			default:
				snap := r.Export()
				for _, m := range snap.Metrics {
					if m.Name == "req" {
						var sum int64
						for _, s := range m.Series {
							sum += s.Value
						}
						if m.Overflow != nil {
							sum += m.Overflow.Value
						}
						if sum != m.AcceptedTotal {
							t.Errorf("concurrent counter sum=%d accepted=%d", sum, m.AcceptedTotal)
						}
					} else {
						histogramConservation(t, m)
					}
				}
			}
		}
	}()

	// 时钟推进 + 回收者：验证回收并入后守恒仍然成立。
	workers.Add(1)
	go func() {
		defer workers.Done()
		for i := 0; i < 10; i++ {
			time.Sleep(5 * time.Millisecond)
			clk.advance(20 * time.Millisecond)
			r.ReclaimExpired()
		}
	}()

	workers.Wait()
	close(stop)
	exporterWG.Wait()

	snap := r.Export()
	req := findMetric(snap, "req")
	lat := findMetric(snap, "lat")
	totalReports := int64(goroutines * perG)
	counterConservation(t, req, totalReports)
	histogramConservation(t, lat)
	if lat.AcceptedTotal != totalReports {
		t.Fatalf("histogram accepted=%d want %d", lat.AcceptedTotal, totalReports)
	}
	// 回收后普通序列数不得超过 N；溢出不占名额。
	for _, m := range snap.Metrics {
		if len(m.Series) > n {
			t.Fatalf("%s active series=%d exceeds N=%d", m.Name, len(m.Series), n)
		}
	}
	// 并发完成后再回收一次，全部序列并入溢出，总量保持不变。
	clk.advance(time.Hour)
	r.ReclaimExpired()
	snap = r.Export()
	req = findMetric(snap, "req")
	lat = findMetric(snap, "lat")
	if len(req.Series) != 0 || req.Overflow == nil || req.Overflow.Value != totalReports {
		t.Fatalf("final counter req=%+v", req)
	}
	if len(lat.Series) != 0 || lat.Overflow == nil || lat.Overflow.Count != uint64(totalReports) {
		t.Fatalf("final histogram lat=%+v", lat)
	}
	if lat.Overflow.BucketCounts[len(lat.Buckets)] != uint64(totalReports) {
		t.Fatalf("final cumulative tail=%v want %d", lat.Overflow.BucketCounts, totalReports)
	}
}
