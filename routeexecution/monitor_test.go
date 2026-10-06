package routeexecution

import (
	"errors"
	"reflect"
	"sync"
	"testing"
)

func fullDurations(stops []Stop, values map[string]uint64) []TravelDuration {
	durations := []TravelDuration{}
	add := func(from string, to string, seconds uint64) {
		if from != to {
			durations = append(durations, TravelDuration{From: from, To: to, Seconds: seconds, Known: true})
		}
	}
	for _, stop := range stops {
		add("depot", stop.ID, values["depot>"+stop.ID])
	}
	for _, from := range stops {
		for _, to := range stops {
			add(from.ID, to.ID, values[from.ID+">"+to.ID])
		}
	}
	return durations
}

func baseConfig() Config {
	return Config{
		OriginID:                    "depot",
		DepartureSeconds:            0,
		MaxContinuousDrivingSeconds: 100,
		RestSeconds:                 5,
		DebounceSeconds:             5,
		LockWindowSeconds:           10,
	}
}

func TestWindowEndpointsAndSoftLate(t *testing.T) {
	config := baseConfig()
	stops := []Stop{
		{ID: "A", Window: TimeWindow{LeftSeconds: 10, RightSeconds: 20}, ServiceSeconds: 2, Type: SoftWindow},
		{ID: "B", Window: TimeWindow{LeftSeconds: 12, RightSeconds: 30}, ServiceSeconds: 1, Type: SoftWindow},
	}
	durations := fullDurations(stops, map[string]uint64{
		"depot>A": 20,
		"A>B":     8,
	})
	monitor, err := NewMonitor(config, stops, durations)
	if err != nil {
		t.Fatal(err)
	}
	snapshot := monitor.Snapshot()
	if snapshot.Stops[0].Status != StatusOnTime {
		t.Fatalf("arrival at right endpoint must be on time: %+v", snapshot.Stops[0])
	}
	if err := monitor.ReportArrival("A", 10, 20); err != nil {
		t.Fatal(err)
	}
	snapshot = monitor.Snapshot()
	if snapshot.Stops[0].Status != StatusOnTime {
		t.Fatalf("arrival at left endpoint must be on time: %+v", snapshot.Stops[0])
	}
	if err := monitor.ReportArrival("B", 20, 20); err != nil {
		t.Fatal(err)
	}
	snapshot = monitor.Snapshot()
	if snapshot.Stops[1].Status != StatusOnTime {
		t.Fatalf("arrival immediately after zero-duration service must be on time: %+v", snapshot.Stops[1])
	}
}

func TestHardWindowSkipUsesSkippedStopTravel(t *testing.T) {
	config := baseConfig()
	stops := []Stop{
		{ID: "A", Window: TimeWindow{LeftSeconds: 0, RightSeconds: 0}, ServiceSeconds: 2, Type: SoftWindow},
		{ID: "B", Window: TimeWindow{LeftSeconds: 0, RightSeconds: 1}, ServiceSeconds: 1, Type: HardWindow},
		{ID: "C", Window: TimeWindow{LeftSeconds: 0, RightSeconds: 100}, ServiceSeconds: 1, Type: SoftWindow},
	}
	durations := fullDurations(stops, map[string]uint64{
		"depot>A": 0,
		"depot>B": 0,
		"depot>C": 0,
		"A>B":     3,
		"A>C":     100,
		"B>C":     4,
	})
	monitor, err := NewMonitor(config, stops, durations)
	if err != nil {
		t.Fatal(err)
	}
	snapshot := monitor.Snapshot()
	if snapshot.Stops[1].Status != StatusSkipped {
		t.Fatalf("B should be skipped: %+v", snapshot.Stops[1])
	}
	if snapshot.Stops[2].ArrivalSeconds != 9 {
		t.Fatalf("C arrival should use skipped B->C duration after leaving A, got %d want 9", snapshot.Stops[2].ArrivalSeconds)
	}
}

func TestDrivingAndRestBoundaries(t *testing.T) {
	config := baseConfig()
	config.InitialContinuousSeconds = 3
	config.MaxContinuousDrivingSeconds = 5
	config.RestSeconds = 2
	stops := []Stop{
		{ID: "exact", Window: TimeWindow{LeftSeconds: 0, RightSeconds: 100}, ServiceSeconds: 1, Type: SoftWindow},
		{ID: "one", Window: TimeWindow{LeftSeconds: 0, RightSeconds: 100}, ServiceSeconds: 1, Type: SoftWindow},
		{ID: "dwell", Window: TimeWindow{LeftSeconds: 0, RightSeconds: 100}, ServiceSeconds: 2, Type: SoftWindow},
		{ID: "afterDwell", Window: TimeWindow{LeftSeconds: 0, RightSeconds: 100}, ServiceSeconds: 1, Type: SoftWindow},
	}
	durations := fullDurations(stops, map[string]uint64{
		"depot>exact":      2,
		"depot>one":        0,
		"depot>dwell":      0,
		"depot>afterDwell": 0,
		"exact>one":        1,
		"exact>dwell":      0,
		"exact>afterDwell": 0,
		"one>dwell":        2,
		"one>afterDwell":   0,
		"dwell>afterDwell": 1,
	})
	monitor, err := NewMonitor(config, stops, durations)
	if err != nil {
		t.Fatal(err)
	}
	snapshot := monitor.Snapshot()
	if snapshot.Stops[0].ArrivalSeconds != 2 {
		t.Fatalf("3+2 exactly at limit must not rest: %+v", snapshot.Stops[0])
	}
	if snapshot.Stops[1].ArrivalSeconds != 6 {
		t.Fatalf("one second over continuous limit requires 2s rest: %+v", snapshot.Stops[1])
	}
	if snapshot.Stops[3].ArrivalSeconds != 12 {
		t.Fatalf("dwell exactly rest duration must reset driving: %+v", snapshot.Stops[3])
	}
}

func TestETAThresholdBoundaries(t *testing.T) {
	config := baseConfig()
	config.DebounceSeconds = 5
	config.LockWindowSeconds = 10
	stops := []Stop{
		{ID: "A", Window: TimeWindow{LeftSeconds: 0, RightSeconds: 100}, ServiceSeconds: 0, Type: SoftWindow},
		{ID: "B", Window: TimeWindow{LeftSeconds: 0, RightSeconds: 100}, ServiceSeconds: 0, Type: SoftWindow},
	}
	durations := fullDurations(stops, map[string]uint64{
		"depot>A": 0,
		"depot>B": 20,
		"A>B":     20,
	})
	monitor, err := NewMonitor(config, stops, durations)
	if err != nil {
		t.Fatal(err)
	}
	if eta := monitor.Snapshot().Stops[1].PublishedETA; eta != 20 {
		t.Fatalf("initial ETA = %d, want 20", eta)
	}
	if err := monitor.ReportArrival("A", 5, 5); err != nil {
		t.Fatal(err)
	}
	if eta := monitor.Snapshot().Stops[1].PublishedETA; eta != 20 {
		t.Fatalf("difference exactly 5 must not update, got %d want 20", eta)
	}
}

func TestETALockBoundary(t *testing.T) {
	config := baseConfig()
	config.DebounceSeconds = 0
	config.LockWindowSeconds = 10
	stops := []Stop{
		{ID: "A", Window: TimeWindow{LeftSeconds: 0, RightSeconds: 100}, ServiceSeconds: 0, Type: SoftWindow},
		{ID: "B", Window: TimeWindow{LeftSeconds: 0, RightSeconds: 100}, ServiceSeconds: 0, Type: SoftWindow},
	}
	durations := fullDurations(stops, map[string]uint64{
		"depot>A": 0,
		"depot>B": 19,
		"A>B":     20,
	})
	monitor, err := NewMonitor(config, stops, durations)
	if err != nil {
		t.Fatal(err)
	}
	if err := monitor.ReportArrival("A", 0, 10); err != nil {
		t.Fatal(err)
	}
	if eta := monitor.Snapshot().Stops[1].PublishedETA; eta != 20 {
		t.Fatalf("new ETA exactly lock-window away must stay frozen at 19, got %d", eta)
	}
}

func TestRejectionPriorityAndAtomicity(t *testing.T) {
	config := baseConfig()
	config.DepartureSeconds = 1
	stops := []Stop{
		{ID: "A", Window: TimeWindow{LeftSeconds: 0, RightSeconds: 0}, ServiceSeconds: 0, Type: SoftWindow},
		{ID: "B", Window: TimeWindow{LeftSeconds: 0, RightSeconds: 10}, ServiceSeconds: 0, Type: SoftWindow},
	}
	durations := fullDurations(stops, map[string]uint64{"depot>A": 0, "depot>B": 1, "A>B": 1})
	monitor, err := NewMonitor(config, stops, durations)
	if err != nil {
		t.Fatal(err)
	}
	if err := monitor.ReportArrival("missing", 1, 0); err != ErrClockRollback {
		t.Fatalf("clock rollback precedes missing stop, got %v", err)
	}
	if err := monitor.ReportArrival("missing", 1, 1); err != ErrStopNotFound {
		t.Fatalf("missing stop precedes state/order, got %v", err)
	}
	if err := monitor.ReportArrival("A", 1, 2); err != nil {
		t.Fatal(err)
	}
	before := monitor.Snapshot()
	if err := monitor.ReportArrival("B", 1, 3); err != ErrInvalidOrder {
		t.Fatalf("arrival at depot departure is order error, got %v", err)
	}
	if got := monitor.Snapshot(); !snapshotsEqual(got, before) {
		t.Fatalf("rejected operation changed state\nbefore=%+v\nafter =%+v", before, got)
	}
}

func snapshotsEqual(a Snapshot, b Snapshot) bool {
	if a.ClockSeconds != b.ClockSeconds || len(a.Stops) != len(b.Stops) {
		return false
	}
	for index := range a.Stops {
		if a.Stops[index] != b.Stops[index] {
			return false
		}
	}
	return true
}

func TestConcurrentOperationsSerializeSafely(t *testing.T) {
	config := baseConfig()
	config.MaxContinuousDrivingSeconds = 100
	stops := []Stop{
		{ID: "A", Window: TimeWindow{LeftSeconds: 0, RightSeconds: 100}, ServiceSeconds: 0, Type: SoftWindow},
		{ID: "B", Window: TimeWindow{LeftSeconds: 0, RightSeconds: 100}, ServiceSeconds: 0, Type: SoftWindow},
	}
	durations := fullDurations(stops, map[string]uint64{
		"depot>A": 1,
		"depot>B": 1,
		"A>B":     1,
	})
	monitor, err := NewMonitor(config, stops, durations)
	if err != nil {
		t.Fatal(err)
	}

	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(2)
		go func() {
			defer wg.Done()
			_ = monitor.Snapshot()
		}()
		go func(i int) {
			defer wg.Done()
			_ = monitor.CancelStop("B", uint64(i)+1)
		}(i)
	}
	wg.Wait()
}

func TestSuffixSimulationDoesNotVisitPrefix(t *testing.T) {
	config := baseConfig()
	stops := make([]Stop, 20)
	values := map[string]uint64{}
	for index := range stops {
		id := string(rune('A' + index))
		stops[index] = Stop{
			ID:             id,
			Window:         TimeWindow{LeftSeconds: 0, RightSeconds: 1000},
			ServiceSeconds: 1,
			Type:           SoftWindow,
		}
		values["depot>"+id] = 1
	}
	for _, from := range stops {
		for _, to := range stops {
			if from.ID != to.ID {
				values[from.ID+">"+to.ID] = 1
			}
		}
	}
	monitor, err := NewMonitor(config, stops, fullDurations(stops, values))
	if err != nil {
		t.Fatal(err)
	}

	planned := make([]plannedStop, len(stops))
	for index := range stops {
		planned[index] = plannedStop{stop: stops[index]}
	}
	planned[10].reported = true
	planned[10].arrival = 12
	instrument := &simulationInstrument{}
	results := simulateSuffixInstrumented(planned, monitor.travel, config, monitor.results[9].nextSeed, 10, instrument)
	if instrument.visits != len(stops)-10 {
		t.Fatalf("suffix visits = %d, want %d", instrument.visits, len(stops)-10)
	}
	if results[9].result.ID != "" || !results[10].result.Reported {
		t.Fatalf("prefix must be untouched by suffix simulation: %+v", results[9])
	}
}

func TestReplayDeterminism(t *testing.T) {
	config := baseConfig()
	stops := []Stop{
		{ID: "A", Window: TimeWindow{LeftSeconds: 0, RightSeconds: 100}, ServiceSeconds: 1, Type: SoftWindow},
		{ID: "B", Window: TimeWindow{LeftSeconds: 0, RightSeconds: 100}, ServiceSeconds: 1, Type: SoftWindow},
		{ID: "C", Window: TimeWindow{LeftSeconds: 0, RightSeconds: 100}, ServiceSeconds: 1, Type: SoftWindow},
	}
	durations := fullDurations(stops, map[string]uint64{
		"depot>A": 1,
		"depot>B": 2,
		"depot>C": 3,
		"A>B":     1,
		"A>C":     2,
		"B>C":     1,
	})
	run := func() Snapshot {
		monitor, err := NewMonitor(config, stops, durations)
		if err != nil {
			t.Fatal(err)
		}
		if err := monitor.CancelStop("C", 1); err != nil {
			t.Fatal(err)
		}
		if err := monitor.ReportArrival("A", 2, 2); err != nil {
			t.Fatal(err)
		}
		if err := monitor.ReportArrival("B", 4, 4); err != nil {
			t.Fatal(err)
		}
		return monitor.Snapshot()
	}
	first := run()
	second := run()
	if !reflect.DeepEqual(first, second) {
		t.Fatalf("replay is not deterministic\nfirst =%+v\nsecond=%+v", first, second)
	}
}

func TestOperationLoggerCapturesInputsAndDecisions(t *testing.T) {
	config := baseConfig()
	stops := []Stop{
		{ID: "A", Window: TimeWindow{LeftSeconds: 0, RightSeconds: 100}, ServiceSeconds: 1, Type: SoftWindow},
	}
	durations := fullDurations(stops, map[string]uint64{"depot>A": 1})
	logs := make([]OperationLog, 0, 2)
	monitor, err := NewMonitor(config, stops, durations, WithOperationLogger(func(log OperationLog) {
		logs = append(logs, log)
	}))
	if err != nil {
		t.Fatal(err)
	}
	if err := monitor.ReportArrival("A", 1, 1); err != nil {
		t.Fatal(err)
	}
	if err := monitor.ReportArrival("A", 2, 2); err == nil {
		t.Fatal("duplicate report must be rejected")
	}
	if len(logs) != 2 {
		t.Fatalf("got %d logs, want 2", len(logs))
	}
	if !logs[0].Accepted || logs[0].Reason == "" || logs[0].Snapshot.ClockSeconds != 1 {
		t.Fatalf("accepted log is incomplete: %+v", logs[0])
	}
	if logs[1].Accepted || !errors.Is(logs[1].Error, ErrInvalidState) || logs[1].Snapshot.ClockSeconds != 1 {
		t.Fatalf("rejected log must preserve previous clock and explain reason: %+v", logs[1])
	}
}
