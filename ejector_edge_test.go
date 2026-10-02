package ontology

import (
	"errors"
	"reflect"
	"testing"
)

func newTestEjector(t *testing.T, n, k int, b, durationCap int64, p, wn, q int) *OutlierEjector {
	t.Helper()
	ejector, err := NewOutlierEjector(n, k, b, durationCap, p, wn, q)
	if err != nil {
		t.Fatalf("NewOutlierEjector() error = %v", err)
	}
	return ejector
}

func reportResult(t *testing.T, ejector *OutlierEjector, host int, ok bool, now int64, want Result) {
	t.Helper()
	got, err := ejector.Report(host, ok, now)
	if err != nil || got != want {
		t.Fatalf("Report(%d, %v, %d) = (%q, %v), want %q", host, ok, now, got, err, want)
	}
}

func assertState(t *testing.T, ejector *OutlierEjector, host, c, e int, u int64, win []bool) {
	t.Helper()
	state := ejector.hosts[host]
	gotWindow := append([]bool(nil), state.window...)
	if len(gotWindow) == 0 {
		gotWindow = nil
	}
	if len(win) == 0 {
		win = nil
	}
	if state.consecutiveFailures != c || state.ejectionCount != e || state.ejectionEnd != u || !reflect.DeepEqual(gotWindow, win) {
		t.Fatalf("host %d state = (c=%d, e=%d, u=%d, win=%v), want (c=%d, e=%d, u=%d, win=%v)",
			host, state.consecutiveFailures, state.ejectionCount, state.ejectionEnd, gotWindow, c, e, u, win)
	}
}

func TestInvalidConfig(t *testing.T) {
	valid := func() (int, int, int64, int64, int, int, int) {
		return 4, 3, 10, 25, 50, 4, 75
	}
	cases := []struct {
		name string
		edit func(*[7]any)
	}{
		{"N zero", func(v *[7]any) { v[0] = 0 }},
		{"K zero", func(v *[7]any) { v[1] = 0 }},
		{"B zero", func(v *[7]any) { v[2] = int64(0) }},
		{"B too large", func(v *[7]any) { v[2] = int64(1_000_000_001) }},
		{"cap below B", func(v *[7]any) { v[3] = int64(9) }},
		{"cap too large", func(v *[7]any) { v[3] = int64(1_000_000_001) }},
		{"P negative", func(v *[7]any) { v[4] = -1 }},
		{"P over 100", func(v *[7]any) { v[4] = 101 }},
		{"Wn zero", func(v *[7]any) { v[5] = 0 }},
		{"Wn over 64", func(v *[7]any) { v[5] = 65 }},
		{"Q zero", func(v *[7]any) { v[6] = 0 }},
		{"Q over 100", func(v *[7]any) { v[6] = 101 }},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			n, k, b, durationCap, p, wn, q := valid()
			values := [7]any{n, k, b, durationCap, p, wn, q}
			tc.edit(&values)
			_, err := NewOutlierEjector(
				values[0].(int), values[1].(int), values[2].(int64),
				values[3].(int64), values[4].(int), values[5].(int), values[6].(int),
			)
			if !errors.Is(err, ErrInvalidConfig) {
				t.Fatalf("error = %v, want ErrInvalidConfig", err)
			}
		})
	}
}

func TestConsecutiveEjectionAndLinearCap(t *testing.T) {
	ejector := newTestEjector(t, 4, 3, 10, 25, 50, 4, 75)

	reportResult(t, ejector, 0, false, 1, ResultRecorded)
	reportResult(t, ejector, 0, false, 2, ResultRecorded)
	assertState(t, ejector, 0, 2, 0, 0, []bool{false, false})
	reportResult(t, ejector, 0, false, 3, ResultEjected)
	assertState(t, ejector, 0, 0, 1, 13, []bool{})

	reportResult(t, ejector, 0, false, 12, ResultIgnored)
	assertState(t, ejector, 0, 0, 1, 13, []bool{})
	reportResult(t, ejector, 0, false, 13, ResultRecorded)
	assertState(t, ejector, 0, 1, 1, 13, []bool{false})
	reportResult(t, ejector, 0, false, 14, ResultRecorded)
	reportResult(t, ejector, 0, false, 15, ResultEjected)
	assertState(t, ejector, 0, 0, 2, 35, []bool{})

	reportResult(t, ejector, 0, false, 35, ResultRecorded)
	reportResult(t, ejector, 0, false, 36, ResultRecorded)
	reportResult(t, ejector, 0, false, 37, ResultEjected)
	assertState(t, ejector, 0, 0, 3, 62, []bool{})
}

func TestExactKAndWindowBoundaries(t *testing.T) {
	t.Run("K minus one does not eject", func(t *testing.T) {
		ejector := newTestEjector(t, 1, 3, 10, 10, 100, 4, 100)
		reportResult(t, ejector, 0, false, 1, ResultRecorded)
		reportResult(t, ejector, 0, false, 2, ResultRecorded)
		assertState(t, ejector, 0, 2, 0, 0, []bool{false, false})
	})

	t.Run("window below size does not trigger", func(t *testing.T) {
		ejector := newTestEjector(t, 1, 100, 10, 10, 100, 4, 75)
		for _, now := range []int64{1, 2, 3} {
			reportResult(t, ejector, 0, false, now, ResultRecorded)
		}
		assertState(t, ejector, 0, 3, 0, 0, []bool{false, false, false})
	})

	t.Run("exact window threshold triggers below K", func(t *testing.T) {
		ejector := newTestEjector(t, 1, 100, 10, 10, 100, 4, 75)
		events := []Event{
			{0, false, 1},
			{0, true, 2},
			{0, false, 3},
			{0, false, 4},
		}
		for i, event := range events {
			want := ResultRecorded
			if i == len(events)-1 {
				want = ResultEjected
			}
			reportResult(t, ejector, event.Host, event.OK, event.Now, want)
		}
		assertState(t, ejector, 0, 0, 1, 14, []bool{})
	})

	t.Run("one failure below exact threshold does not trigger", func(t *testing.T) {
		ejector := newTestEjector(t, 1, 100, 10, 10, 100, 4, 75)
		reportResult(t, ejector, 0, false, 1, ResultRecorded)
		reportResult(t, ejector, 0, true, 2, ResultRecorded)
		reportResult(t, ejector, 0, false, 3, ResultRecorded)
		reportResult(t, ejector, 0, true, 4, ResultRecorded)
		assertState(t, ejector, 0, 0, 0, 0, []bool{false, true, false, true})
	})
}

func TestWindowEvictionSuccessAndIgnore(t *testing.T) {
	ejector := newTestEjector(t, 1, 100, 10, 10, 100, 4, 100)

	reportResult(t, ejector, 0, true, 12, ResultRecorded)
	assertState(t, ejector, 0, 0, 0, 0, []bool{true})
	reportResult(t, ejector, 0, true, 13, ResultRecorded)
	reportResult(t, ejector, 0, false, 14, ResultRecorded)
	reportResult(t, ejector, 0, false, 15, ResultRecorded)
	reportResult(t, ejector, 0, true, 16, ResultRecorded)
	assertState(t, ejector, 0, 0, 0, 0, []bool{true, false, false, true})

	ejector.hosts[0].ejectionCount = 2
	ejector.hosts[0].ejectionEnd = 20
	reportResult(t, ejector, 0, true, 19, ResultIgnored)
	assertState(t, ejector, 0, 0, 2, 20, []bool{true, false, false, true})

	reportResult(t, ejector, 0, true, 20, ResultRecorded)
	assertState(t, ejector, 0, 0, 1, 20, []bool{false, false, true, true})
	reportResult(t, ejector, 0, true, 21, ResultRecorded)
	assertState(t, ejector, 0, 0, 0, 20, []bool{false, true, true, true})
	reportResult(t, ejector, 0, true, 22, ResultRecorded)
	assertState(t, ejector, 0, 0, 0, 20, []bool{true, true, true, true})
}

func TestPercentageBoundaryBlockingAndRecoveryRetry(t *testing.T) {
	ejector := newTestEjector(t, 4, 3, 10, 25, 50, 4, 50)

	reportResult(t, ejector, 1, false, 1, ResultRecorded)
	reportResult(t, ejector, 1, true, 2, ResultRecorded)
	reportResult(t, ejector, 1, false, 3, ResultRecorded)
	reportResult(t, ejector, 1, false, 4, ResultEjected)

	reportResult(t, ejector, 2, true, 5, ResultRecorded)
	reportResult(t, ejector, 2, false, 6, ResultRecorded)
	reportResult(t, ejector, 2, true, 7, ResultRecorded)
	reportResult(t, ejector, 2, false, 8, ResultEjected)

	reportResult(t, ejector, 3, false, 9, ResultRecorded)
	reportResult(t, ejector, 3, false, 10, ResultRecorded)
	reportResult(t, ejector, 3, false, 11, ResultBlocked)
	assertState(t, ejector, 3, 3, 0, 0, []bool{false, false, false})
	reportResult(t, ejector, 3, false, 12, ResultBlocked)
	assertState(t, ejector, 3, 4, 0, 0, []bool{false, false, false, false})

	reportResult(t, ejector, 3, false, 14, ResultEjected)
	assertState(t, ejector, 3, 0, 1, 24, []bool{})

	ejected, err := ejector.Ejected(1, 14)
	if err != nil || ejected {
		t.Fatalf("host 1 at 14 ejected = (%v, %v), want false", ejected, err)
	}
	healthy, err := ejector.Healthy(14)
	if err != nil || !reflect.DeepEqual(healthy, []int{0, 1}) {
		t.Fatalf("Healthy(14) = (%v, %v), want [0 1]", healthy, err)
	}
}

func TestPExtremes(t *testing.T) {
	blocked := newTestEjector(t, 1, 1, 10, 10, 0, 1, 1)
	reportResult(t, blocked, 0, false, 1, ResultBlocked)
	assertState(t, blocked, 0, 1, 0, 0, []bool{false})

	allowed := newTestEjector(t, 3, 1, 10, 10, 100, 1, 1)
	for host := 0; host < 3; host++ {
		reportResult(t, allowed, host, false, int64(host+1), ResultEjected)
	}
	healthy, err := allowed.Healthy(4)
	if err != nil || len(healthy) != 0 {
		t.Fatalf("Healthy(4) = (%v, %v), want empty", healthy, err)
	}
}

func TestRejectionOrderAndNoStateChange(t *testing.T) {
	ejector := newTestEjector(t, 2, 2, 10, 10, 100, 2, 50)
	reportResult(t, ejector, 0, true, 5, ResultRecorded)

	if _, err := ejector.Report(2, false, -1); !errors.Is(err, ErrInvalidHost) {
		t.Fatalf("bad host/time error = %v, want ErrInvalidHost", err)
	}
	if _, err := ejector.Report(0, false, -1); !errors.Is(err, ErrInvalidTime) {
		t.Fatalf("bad time error = %v, want ErrInvalidTime", err)
	}
	if _, err := ejector.Report(0, false, 4); !errors.Is(err, ErrClockRollback) {
		t.Fatalf("rollback error = %v, want ErrClockRollback", err)
	}
	if _, err := ejector.Ejected(2, -1); !errors.Is(err, ErrInvalidHost) {
		t.Fatalf("Ejected bad host/time error = %v, want ErrInvalidHost", err)
	}
	if _, err := ejector.Ejected(0, 4); !errors.Is(err, ErrClockRollback) {
		t.Fatalf("Ejected rollback error = %v, want ErrClockRollback", err)
	}
	if _, err := ejector.Healthy(4); !errors.Is(err, ErrClockRollback) {
		t.Fatalf("Healthy rollback error = %v, want ErrClockRollback", err)
	}

	reportResult(t, ejector, 0, true, 5, ResultRecorded)
	assertState(t, ejector, 0, 0, 0, 0, []bool{true, true})
	if ejector.maxNow != 5 {
		t.Fatalf("maxNow = %d, want 5", ejector.maxNow)
	}
}

func TestReportBatchOrderingAndAtomicity(t *testing.T) {
	t.Run("sorts by time and returns in input order", func(t *testing.T) {
		ejector := newTestEjector(t, 1, 3, 10, 10, 100, 4, 100)
		results, err := ejector.ReportBatch([]Event{
			{0, false, 5},
			{0, false, 3},
			{0, false, 4},
		})
		if err != nil {
			t.Fatalf("ReportBatch() error = %v", err)
		}
		want := []Result{ResultEjected, ResultRecorded, ResultRecorded}
		if !reflect.DeepEqual(results, want) {
			t.Fatalf("results = %v, want %v", results, want)
		}
		assertState(t, ejector, 0, 0, 1, 15, []bool{})
	})

	t.Run("equal timestamps preserve input order", func(t *testing.T) {
		ejector := newTestEjector(t, 1, 3, 10, 10, 100, 4, 100)
		results, err := ejector.ReportBatch([]Event{
			{0, false, 7},
			{0, false, 7},
			{0, false, 7},
		})
		if err != nil {
			t.Fatalf("ReportBatch() error = %v", err)
		}
		want := []Result{ResultRecorded, ResultRecorded, ResultEjected}
		if !reflect.DeepEqual(results, want) {
			t.Fatalf("results = %v, want %v", results, want)
		}
	})

	t.Run("first invalid event is host error", func(t *testing.T) {
		ejector := newTestEjector(t, 1, 2, 10, 10, 100, 2, 50)
		_, err := ejector.ReportBatch([]Event{
			{0, false, 2},
			{2, false, -1},
			{0, false, -1},
		})
		if !errors.Is(err, ErrInvalidHost) {
			t.Fatalf("error = %v, want ErrInvalidHost", err)
		}
		assertState(t, ejector, 0, 0, 0, 0, []bool{})
		if ejector.maxNow != 0 {
			t.Fatalf("maxNow = %d, want 0", ejector.maxNow)
		}
	})

	t.Run("first invalid event is time error", func(t *testing.T) {
		ejector := newTestEjector(t, 1, 2, 10, 10, 100, 2, 50)
		_, err := ejector.ReportBatch([]Event{
			{0, false, 2},
			{0, false, -1},
		})
		if !errors.Is(err, ErrInvalidTime) {
			t.Fatalf("error = %v, want ErrInvalidTime", err)
		}
		assertState(t, ejector, 0, 0, 0, 0, []bool{})
	})

	t.Run("rollback rejects entire batch atomically", func(t *testing.T) {
		ejector := newTestEjector(t, 1, 2, 10, 10, 100, 2, 50)
		reportResult(t, ejector, 0, false, 5, ResultRecorded)
		_, err := ejector.ReportBatch([]Event{
			{0, false, 6},
			{0, false, 4},
		})
		if !errors.Is(err, ErrClockRollback) {
			t.Fatalf("error = %v, want ErrClockRollback", err)
		}
		assertState(t, ejector, 0, 1, 0, 0, []bool{false})
		if ejector.maxNow != 5 {
			t.Fatalf("maxNow = %d, want 5", ejector.maxNow)
		}
	})

	t.Run("empty batch changes nothing", func(t *testing.T) {
		ejector := newTestEjector(t, 1, 2, 10, 10, 100, 2, 50)
		results, err := ejector.ReportBatch(nil)
		if err != nil || len(results) != 0 {
			t.Fatalf("empty ReportBatch = (%v, %v), want empty", results, err)
		}
	})
}
