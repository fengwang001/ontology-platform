package history

import "testing"

func TestFinishStateTransitions(t *testing.T) {
	tests := []struct {
		name       string
		results    []Result
		quarantine bool
		streak     int
		window     []Mark
	}{
		{
			name: "exactly threshold isolates and broken does not count as flaky",
			results: []Result{
				{Mark: Broken, Passed: false},
				{Mark: Flaky, Passed: true, Duration: 1},
				{Mark: Flaky, Passed: true, Duration: 1},
			},
			quarantine: true,
			streak:     0,
			window:     []Mark{Broken, Flaky, Flaky},
		},
		{
			name: "below threshold remains active",
			results: []Result{
				{Mark: Clean, Passed: true, Duration: 1},
				{Mark: Flaky, Passed: true, Duration: 1},
			},
			quarantine: false,
			window:     []Mark{Clean, Flaky},
		},
		{
			name: "exactly clean releases and clears window",
			results: []Result{
				{Mark: Flaky, Passed: true, Duration: 1},
				{Mark: Flaky, Passed: true, Duration: 1},
				{Mark: Clean, Passed: true, Duration: 1},
				{Mark: Clean, Passed: true, Duration: 1},
			},
			quarantine: false,
			streak:     0,
			window:     nil,
		},
		{
			name: "flaky after one clean resets release streak",
			results: []Result{
				{Mark: Flaky, Passed: true, Duration: 1},
				{Mark: Flaky, Passed: true, Duration: 1},
				{Mark: Clean, Passed: true, Duration: 1},
				{Mark: Flaky, Passed: true, Duration: 1},
				{Mark: Clean, Passed: true, Duration: 1},
			},
			quarantine: true,
			streak:     1,
			window:     []Mark{Flaky, Flaky},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			store := New(3)
			for index, result := range tt.results {
				store.Finish("x", result, 5, 2, 2)
				t.Logf("input step=%d result=%+v", index+1, result)
			}
			record := store.Snapshot("x")
			t.Logf("output record=%+v", record)
			if record.Quarantined != tt.quarantine {
				t.Fatalf("quarantined = %v, want %v", record.Quarantined, tt.quarantine)
			}
			if record.CleanStreak != tt.streak {
				t.Fatalf("clean streak = %d, want %d", record.CleanStreak, tt.streak)
			}
			if tt.window != nil && len(record.Window) != len(tt.window) {
				t.Fatalf("window len = %d, want %d", len(record.Window), len(tt.window))
			}
			if tt.window == nil && len(record.Window) != 0 {
				t.Fatalf("window = %v, want empty", record.Window)
			}
		})
	}
}

func TestSnapshotIsolationAndRecentSamples(t *testing.T) {
	store := New(2)
	store.Finish("x", Result{Mark: Flaky, Passed: true, Duration: 10}, 2, 2, 1)
	store.Finish("x", Result{Mark: Clean, Passed: true, Duration: 20}, 2, 2, 1)

	record := store.Snapshot("x")
	record.Samples[0] = 999
	record.Window[0] = Clean

	again := store.Snapshot("x")
	if got := (again.Samples[0] + again.Samples[1] + 1) / 2; got != 15 {
		t.Fatalf("rounded average = %d, want 15", got)
	}
	if len(again.Samples) != 2 || again.Samples[0] != 10 || again.Samples[1] != 20 {
		t.Fatalf("samples = %v, want [10 20]", again.Samples)
	}
	if again.Window[0] != Flaky {
		t.Fatalf("window = %v, want flaky preserved", again.Window)
	}
}
