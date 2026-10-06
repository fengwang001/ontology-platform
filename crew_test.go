package crew

import (
	"bytes"
	"fmt"
	"sync"
	"testing"
)

func testConfig() Config {
	return Config{
		EarlyEnd:            360,
		NightStart:          1080,
		EarlyLimit:          600,
		DayLimit:            660,
		NightLimit:          540,
		ReductionPerSegment: 30,
		MinimumDutyLimit:    300,
		MinimumRest:         600,
		SevenDayLimit:       3000,
		TwentyEightDayLimit: 12000,
		MaximumExtension:    120,
	}
}

func register(t *testing.T, system *System, now int, personID, dutyID string, start, end, segments int) *Rejection {
	t.Helper()
	return system.Register(now, DutyPeriod{
		PersonID: personID,
		ID:       dutyID,
		Start:    start,
		End:      end,
		Segments: segments,
	})
}

func setupPerson(t *testing.T, config Config) *System {
	t.Helper()
	system := NewSystem(config)
	if rejection := system.AddPerson(0, "p"); rejection != nil {
		t.Fatalf("AddPerson: %v", rejection)
	}
	return system
}

func TestPeriodBoundaryAndExactDutyLimit(t *testing.T) {
	system := setupPerson(t, testConfig())

	if rejection := register(t, system, 0, "p", "boundary", 360, 360+660, 0); rejection != nil {
		t.Fatalf("start exactly at day boundary should accept: %v", rejection)
	}

	rejection := register(t, system, 0, "p", "one-minute-over", 2520, 2520+541, 0)
	if rejection == nil || rejection.Code != DutyLimitExceeded {
		t.Fatalf("got %v, want duty limit exceeded", rejection)
	}
}

func TestExactRestIsEnough(t *testing.T) {
	system := setupPerson(t, testConfig())
	if rejection := register(t, system, 0, "p", "first", 0, 500, 0); rejection != nil {
		t.Fatalf("first: %v", rejection)
	}
	if rejection := register(t, system, 0, "p", "second", 1100, 1400, 0); rejection != nil {
		t.Fatalf("rest exactly max(config, previous duty): %v", rejection)
	}
}

func TestTouchingIntervalsOverlap(t *testing.T) {
	system := setupPerson(t, testConfig())
	_ = register(t, system, 0, "p", "first", 0, 500, 0)
	rejection := register(t, system, 0, "p", "touch", 500, 700, 0)
	if rejection == nil || rejection.Code != OverlappingDuty {
		t.Fatalf("got %v, want overlap", rejection)
	}
}

func TestInsertInMiddleChecksBothSides(t *testing.T) {
	config := testConfig()
	config.MinimumRest = 100
	system := setupPerson(t, config)
	_ = register(t, system, 0, "p", "first", 0, 200, 0)
	_ = register(t, system, 0, "p", "last", 600, 800, 0)
	r := register(t, system, 0, "p", "middle", 350, 450, 0)
	if r == nil || r.Code != InsufficientRest {
		t.Fatalf("got %v, want rest violation against neighboring duties", r)
	}
}

func TestRollingWindowUsesOverlapNotWholeDuty(t *testing.T) {
	config := testConfig()
	config.SevenDayLimit = 350
	config.MinimumRest = 50
	system := setupPerson(t, config)
	first := DutyPeriod{PersonID: "p", ID: "first", Start: SevenDays - 300, End: SevenDays - 50, Segments: 0}
	if r := system.Register(0, first); r != nil {
		t.Fatalf("first: %v", r)
	}
	second := DutyPeriod{PersonID: "p", ID: "second", Start: SevenDays + 200, End: SevenDays + 351, Segments: 0}
	r := system.Register(0, second)
	if r == nil || r.Code != SevenDayLimitExceeded || r.WindowStart != 301 {
		t.Fatalf("got %#v, want earliest overlap-based violation at 301", r)
	}
}

func TestQualificationExpiryMustBeAfterRelease(t *testing.T) {
	system := setupPerson(t, testConfig())
	if rejection := system.SetQualification(0, "p", "A", 600); rejection != nil {
		t.Fatalf("SetQualification: %v", rejection)
	}
	duty := DutyPeriod{PersonID: "p", ID: "d", Start: 0, End: 600, Qualification: "A"}
	if rejection := system.Register(0, duty); rejection == nil || rejection.Code != QualificationInvalid {
		t.Fatalf("got %v, want invalid qualification", rejection)
	}
	duty.End = 599
	if rejection := system.Register(0, duty); rejection != nil {
		t.Fatalf("expiry strictly after release should accept: %v", rejection)
	}
}

func TestExtensionRechecksCumulativeLimit(t *testing.T) {
	config := testConfig()
	config.SevenDayLimit = 1100
	system := setupPerson(t, config)
	first := DutyPeriod{PersonID: "p", ID: "first", Start: 0, End: 500, Segments: 0}
	second := DutyPeriod{PersonID: "p", ID: "second", Start: 1200, End: 1700, Segments: 0}
	if rejection := system.Register(0, first); rejection != nil {
		t.Fatalf("first: %v", rejection)
	}
	if rejection := system.Register(0, second); rejection != nil {
		t.Fatalf("second: %v", rejection)
	}
	rejection := system.Extend(0, "p", "second", 1820)
	if rejection == nil || rejection.Code != SevenDayLimitExceeded || rejection.WindowStart != 0 {
		t.Fatalf("got %#v, want seven-day violation on window 0", rejection)
	}
}

func TestRevokeStartedFutureDutiesDifferently(t *testing.T) {
	system := setupPerson(t, testConfig())
	_ = register(t, system, 10, "p", "future", 100, 500, 0)
	if rejection := system.Revoke(100, "p", "future"); rejection == nil || rejection.Code != AlreadyStartedOrReleased {
		t.Fatalf("started duty revoke got %v", rejection)
	}
	if rejection := system.Extend(100, "p", "future", 620); rejection != nil {
		t.Fatalf("started but not released duty can extend: %v", rejection)
	}
	if rejection := system.Revoke(100, "p", "future"); rejection == nil || rejection.Code != AlreadyStartedOrReleased {
		t.Fatalf("extended started duty revoke got %v", rejection)
	}
}

func TestClockRollbackDoesNotMutate(t *testing.T) {
	system := setupPerson(t, testConfig())
	_ = system.SetQualification(10, "p", "A", 10000)
	if rejection := system.SetQualification(5, "p", "B", 10000); rejection == nil || rejection.Code != ClockRollback {
		t.Fatalf("got %v, want clock rollback", rejection)
	}
	if rejection := system.SetQualification(10, "p", "B", 10000); rejection != nil {
		t.Fatalf("rejected operation must not advance clock: %v", rejection)
	}
}

func TestLogsInputOutput(t *testing.T) {
	system := setupPerson(t, testConfig())
	var logs bytes.Buffer
	system.SetLogger(&logs)
	_ = register(t, system, 0, "p", "d", 0, 100, 0)
	if !bytes.Contains(logs.Bytes(), []byte("op=register")) || !bytes.Contains(logs.Bytes(), []byte("accepted")) {
		t.Fatalf("logs missing input/output: %q", logs.String())
	}
}

func TestRejectionOrderAdjacentPairs(t *testing.T) {
	t.Run("invalid before clock", func(t *testing.T) {
		system := setupPerson(t, testConfig())
		_ = system.SetQualification(10, "p", "A", 10000)
		r := system.SetQualification(5, "p", "", 0)
		if r.Code != InvalidArgument {
			t.Fatalf("got %v, want invalid argument", r)
		}
	})
	t.Run("clock before person", func(t *testing.T) {
		system := NewSystem(testConfig())
		_ = system.AddPerson(10, "existing")
		r := system.SetQualification(5, "missing", "A", 100)
		if r.Code != ClockRollback {
			t.Fatalf("got %v, want clock rollback", r)
		}
	})
	t.Run("person before duty", func(t *testing.T) {
		system := setupPerson(t, testConfig())
		r := system.Revoke(0, "missing", "d")
		if r.Code != PersonNotFound {
			t.Fatalf("got %v, want person missing", r)
		}
	})
	t.Run("duty before immutable", func(t *testing.T) {
		system := setupPerson(t, testConfig())
		_ = register(t, system, 0, "p", "released", 0, 100, 0)
		r := system.Revoke(101, "p", "missing")
		if r.Code != DutyNotFound {
			t.Fatalf("got %v, want duty missing", r)
		}
	})
	t.Run("immutable before qualification", func(t *testing.T) {
		system := setupPerson(t, testConfig())
		_ = register(t, system, 0, "p", "d", 0, 100, 0)
		r := system.Extend(101, "p", "d", 220)
		if r.Code != AlreadyStartedOrReleased {
			t.Fatalf("got %v, want already released", r)
		}
	})
	t.Run("qualification before overlap", func(t *testing.T) {
		system := setupPerson(t, testConfig())
		_ = register(t, system, 0, "p", "a", 0, 500, 0)
		duty := DutyPeriod{PersonID: "p", ID: "b", Start: 0, End: 100, Qualification: "A"}
		r := system.Register(0, duty)
		if r.Code != QualificationInvalid {
			t.Fatalf("got %v, want qualification invalid", r)
		}
	})
	t.Run("overlap before rest", func(t *testing.T) {
		system := setupPerson(t, testConfig())
		_ = register(t, system, 0, "p", "a", 0, 500, 0)
		r := register(t, system, 0, "p", "b", 500, 700, 0)
		if r.Code != OverlappingDuty {
			t.Fatalf("got %v, want overlap", r)
		}
	})
	t.Run("rest before single limit", func(t *testing.T) {
		system := setupPerson(t, testConfig())
		_ = register(t, system, 0, "p", "a", 0, 500, 0)
		r := register(t, system, 0, "p", "b", 600, 2000, 0)
		if r.Code != InsufficientRest {
			t.Fatalf("got %v, want insufficient rest", r)
		}
	})
	t.Run("single before extension", func(t *testing.T) {
		system := setupPerson(t, testConfig())
		_ = register(t, system, 0, "p", "a", 0, 100, 0)
		_ = register(t, system, 0, "p", "b", 800, 900, 0)
		r := register(t, system, 0, "p", "c", SevenDays, SevenDays+2000, 0)
		if r.Code != DutyLimitExceeded {
			t.Fatalf("got %v, want single limit", r)
		}
	})
	t.Run("extension before seven day", func(t *testing.T) {
		config := testConfig()
		config.SevenDayLimit = 1200
		system := setupPerson(t, config)
		first := DutyPeriod{PersonID: "p", ID: "a", Start: 0, End: 500}
		second := DutyPeriod{PersonID: "p", ID: "b", Start: 2000, End: 2500}
		_ = system.Register(0, first)
		if r := system.Register(0, second); r != nil {
			t.Fatalf("second: %v", r)
		}
		_ = system.Extend(0, "p", "a", 620)
		r := system.Extend(0, "p", "b", 2600)
		if r.Code != ExtensionRuleViolated {
			t.Fatalf("got %v, want extension rule", r)
		}
	})
	t.Run("seven before twenty eight", func(t *testing.T) {
		config := testConfig()
		config.SevenDayLimit = 400
		config.TwentyEightDayLimit = 400
		system := setupPerson(t, config)
		r := register(t, system, 0, "p", "a", 0, 500, 0)
		if r.Code != SevenDayLimitExceeded || r.WindowStart != 0 {
			t.Fatalf("got %#v, want seven day at 0", r)
		}
	})
}

func TestConcurrentOperationsAreSerializable(t *testing.T) {
	config := testConfig()
	config.MinimumRest = 1
	system := NewSystem(config)
	if rejection := system.AddPerson(0, "p"); rejection != nil {
		t.Fatalf("AddPerson: %v", rejection)
	}
	var wait sync.WaitGroup
	for index := 0; index < 50; index++ {
		wait.Add(1)
		go func(index int) {
			defer wait.Done()
			start := 1000 + index*1000
			duty := DutyPeriod{PersonID: "p", ID: fmt.Sprintf("d-%d", index), Start: start, End: start + 2, Segments: 0}
			_ = system.Register(0, duty)
		}(index)
	}
	wait.Wait()
}

func BenchmarkRegisterWithLongHistory(b *testing.B) {
	config := testConfig()
	config.MinimumRest = 1
	config.EarlyLimit = 100
	config.DayLimit = 100
	config.NightLimit = 100
	config.MinimumDutyLimit = 100
	config.MaximumExtension = 0
	system := NewSystem(config)
	if rejection := system.AddPerson(0, "p"); rejection != nil {
		b.Fatal(rejection)
	}
	for index := 0; index < 20000; index++ {
		start := index * 110
		duty := DutyPeriod{PersonID: "p", ID: fmt.Sprintf("history-%d", index), Start: start, End: start + 2, Segments: 0}
		if rejection := system.Register(0, duty); rejection != nil {
			b.Fatalf("history setup at %d: %v", index, rejection)
		}
	}
	for index := 0; index < 1000; index++ {
		if rejection := system.AddPerson(0, string(rune('p'))); rejection == nil {
			b.Fatalf("unexpected accepted duplicate-person setup")
		}
	}
	candidate := DutyPeriod{PersonID: "p", ID: "candidate", Start: 2_300_000, End: 2_300_002, Segments: 0}
	b.ResetTimer()
	for index := 0; index < b.N; index++ {
		candidate.ID = fmt.Sprintf("candidate-%d", index)
		candidate.Start = 2_300_000 + index*110
		candidate.End = candidate.Start + 2
		if rejection := system.Register(0, candidate); rejection != nil {
			b.Fatalf("candidate: %v", rejection)
		}
	}
}
