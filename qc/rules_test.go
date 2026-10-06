package qc

import "testing"

func TestRuleBoundaries(t *testing.T) {
	t.Run("rule one strict three standard deviations", func(t *testing.T) {
		system := NewSystem()
		config := AssayConfig{
			Low:      LevelConfig{Target: 100, SD: 10},
			High:     LevelConfig{Target: 50, SD: 5},
			ValidFor: 100,
		}
		if err := system.RegisterAssay(1, "analyzer", "glucose", config); err != nil {
			t.Fatal(err)
		}
		record, err := system.RunQC(2, "analyzer", "glucose", 130, 50)
		if err != nil {
			t.Fatal(err)
		}
		if len(record.Triggered) != 0 || record.Outcome != OutcomeWarning {
			t.Fatalf("exact 3SD must not trigger a rule and is above 2SD: %+v", record)
		}
		if err := system.Calibrate(3, "analyzer", "glucose"); err != nil {
			t.Fatal(err)
		}
		record, err = system.RunQC(3, "analyzer", "glucose", 131, 50)
		if err != nil || len(record.Triggered) != 1 || record.Triggered[0] != Rule1 {
			t.Fatalf("3SD plus one must trigger rule one: %+v %v", record, err)
		}
	})

	t.Run("rule two strict two standard deviations", func(t *testing.T) {
		system := NewSystem()
		registerTestAssay(t, system, "rule-two")
		runTestQC(t, system, 2, "rule-two", 120, 50)
		record := runTestQC(t, system, 3, "rule-two", 120, 50)
		if len(record.Triggered) != 0 || record.Outcome != OutcomeNormal {
			t.Fatalf("exact 2SD must not trigger rule two: %+v", record)
		}
		if err := system.Calibrate(4, "analyzer", "rule-two"); err != nil {
			t.Fatal(err)
		}
		runTestQC(t, system, 5, "rule-two", 121, 50)
		record = runTestQC(t, system, 6, "rule-two", 121, 50)
		if len(record.Triggered) != 1 || record.Triggered[0] != Rule2 {
			t.Fatalf("two points over 2SD must trigger rule two: %+v", record)
		}
	})

	t.Run("rule three uses each level standard deviation", func(t *testing.T) {
		system := NewSystem()
		registerTestAssay(t, system, "rule-three")
		record := runTestQC(t, system, 2, "rule-three", 120, 40)
		if len(record.Triggered) != 0 || record.Outcome != OutcomeNormal {
			t.Fatalf("exact opposite 2SD must not trigger: %+v", record)
		}
		record = runTestQC(t, system, 3, "rule-three", 121, 39)
		if len(record.Triggered) != 1 || record.Triggered[0] != Rule3 {
			t.Fatalf("opposite levels beyond 2SD must trigger rule three: %+v", record)
		}
	})

	t.Run("rule four strict one standard deviation and four points", func(t *testing.T) {
		system := NewSystem()
		registerTestAssay(t, system, "rule-four")
		runRepeated(t, system, "rule-four", 110, 3, 2)
		record := runTestQC(t, system, 5, "rule-four", 110, 50)
		if len(record.Triggered) != 0 || record.Outcome != OutcomeNormal {
			t.Fatalf("exact 1SD must not trigger rule four: %+v", record)
		}
		runRepeated(t, system, "rule-four", 111, 3, 6)
		record = runTestQC(t, system, 9, "rule-four", 111, 50)
		if len(record.Triggered) != 1 || record.Triggered[0] != Rule4 {
			t.Fatalf("four points beyond 1SD must trigger rule four: %+v", record)
		}
	})

	t.Run("rule five ten same side and zero breaks", func(t *testing.T) {
		system := NewSystem()
		registerTestAssay(t, system, "rule-five")
		runRepeated(t, system, "rule-five", 101, 9, 2)
		record := runTestQC(t, system, 11, "rule-five", 101, 50)
		if len(record.Triggered) != 1 || record.Triggered[0] != Rule5 {
			t.Fatalf("ten same-side points must trigger rule five: %+v", record)
		}
		runRepeated(t, system, "rule-five", 101, 9, 12)
		runTestQC(t, system, 22, "rule-five", 100, 50)
		record = runTestQC(t, system, 23, "rule-five", 101, 50)
		if len(record.Triggered) != 0 {
			t.Fatalf("zero deviation must reset same-side continuity: %+v", record)
		}
	})

	t.Run("multiple rules preserve required order", func(t *testing.T) {
		system := NewSystem()
		registerTestAssay(t, system, "all-rules")
		runRepeated(t, system, "all-rules", 111, 9, 2)
		runRepeated(t, system, "all-rules", 121, 1, 11)
		record := runTestQC(t, system, 12, "all-rules", 131, 39)
		want := []Rule{Rule1, Rule2, Rule3, Rule4, Rule5}
		if len(record.Triggered) != len(want) {
			t.Fatalf("got %v, want %v", record.Triggered, want)
		}
		for index := range want {
			if record.Triggered[index] != want[index] {
				t.Fatalf("got %v, want %v", record.Triggered, want)
			}
		}
	})

	t.Run("warning differs from normal and rejection", func(t *testing.T) {
		system := NewSystem()
		registerTestAssay(t, system, "warning")
		record := runTestQC(t, system, 2, "warning", 120, 50)
		if record.Outcome != OutcomeNormal {
			t.Fatalf("exact 2SD is normal: %+v", record)
		}
		record = runTestQC(t, system, 3, "warning", 121, 50)
		if record.Outcome != OutcomeWarning || len(record.Triggered) != 0 {
			t.Fatalf("single point beyond 2SD without rules is warning: %+v", record)
		}
	})
}

func registerTestAssay(t *testing.T, system *System, assay string) {
	t.Helper()
	config := AssayConfig{
		Low:      LevelConfig{Target: 100, SD: 10},
		High:     LevelConfig{Target: 50, SD: 5},
		ValidFor: 10,
	}
	if err := system.RegisterAssay(1, "analyzer", assay, config); err != nil {
		t.Fatal(err)
	}
}

func runTestQC(t *testing.T, system *System, now int64, assay string, low, high int64) QCRecord {
	t.Helper()
	record, err := system.RunQC(now, "analyzer", assay, low, high)
	if err != nil {
		t.Fatal(err)
	}
	return record
}

func runRepeated(t *testing.T, system *System, assay string, low int64, times int, start int64) {
	t.Helper()
	for index := 0; index < times; index++ {
		runTestQC(t, system, start+int64(index), assay, low, 50)
	}
}
