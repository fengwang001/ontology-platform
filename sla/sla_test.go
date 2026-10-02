package sla

import (
	"sync"
	"testing"
)

func exampleConfig() Config {
	return Config{
		LengthMinutes:       43200,
		JitterMinutes:       5,
		Tier1Availability:   999000,
		Tier2Availability:   990000,
		Tier3Availability:   950000,
		Tier1Credit:         10,
		Tier2Credit:         25,
		Tier3Credit:         100,
		EscalationStep:      5,
		EscalationCapMonths: 4,
		AnnualCreditLimit:   2500,
		MonthlyExcludeLimit: 100,
	}
}

func mustNew(t *testing.T, config Config) *Settlement {
	t.Helper()
	s, err := New(config)
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func reportExampleFailures(t *testing.T, s *Settlement) {
	t.Helper()
	for _, failure := range [][2]int{{100, 130}, {130, 140}, {1000, 1060}, {500, 503}} {
		if err := s.Report(failure[0], failure[1]); err != nil {
			t.Fatal(err)
		}
	}
}

func TestWorkedExample(t *testing.T) {
	s := mustNew(t, exampleConfig())
	wantResults := []CloseResult{
		{DowntimeMinutes: 90, Availability: 997916, BasePercent: 10, AppliedPercent: 10, Credit: 1000},
		{DowntimeMinutes: 90, Availability: 997916, BasePercent: 10, AppliedPercent: 15, Credit: 1500},
		{DowntimeMinutes: 90, Availability: 997916, BasePercent: 10, AppliedPercent: 20, Credit: 0},
	}

	for month, want := range wantResults {
		if err := s.NewMonth(10000); err != nil {
			t.Fatal(err)
		}
		reportExampleFailures(t, s)
		if err := s.AddExclude(110, 120); err != nil {
			t.Fatal(err)
		}
		got, err := s.Close()
		if err != nil {
			t.Fatal(err)
		}
		if got != want {
			t.Fatalf("month %d = %+v, want %+v", month, got, want)
		}
	}
	if s.streak != 3 {
		t.Fatalf("streak = %d, want 3", s.streak)
	}
}

func TestMergeOverlapAdjacentAndExclusionSplit(t *testing.T) {
	config := exampleConfig()
	config.LengthMinutes = 100
	config.Tier1Availability = 999000
	config.Tier2Availability = 950000
	config.Tier3Availability = 900000
	config.JitterMinutes = 1
	config.AnnualCreditLimit = 1000
	config.MonthlyExcludeLimit = 100
	s := mustNew(t, config)

	if err := s.NewMonth(100); err != nil {
		t.Fatal(err)
	}
	for _, failure := range [][2]int{{0, 8}, {5, 15}, {15, 20}} {
		if err := s.Report(failure[0], failure[1]); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.AddExclude(9, 11); err != nil {
		t.Fatal(err)
	}
	result, err := s.Close()
	if err != nil {
		t.Fatal(err)
	}
	if result.DowntimeMinutes != 18 {
		t.Fatalf("D = %d, want 18", result.DowntimeMinutes)
	}
}

func TestSubtractBeforeJitterAndEqualJitter(t *testing.T) {
	config := exampleConfig()
	config.LengthMinutes = 100
	config.Tier1Availability = 999000
	config.Tier2Availability = 950000
	config.Tier3Availability = 900000
	config.AnnualCreditLimit = 1000
	config.MonthlyExcludeLimit = 100

	config.JitterMinutes = 4
	s := mustNew(t, config)
	if err := s.NewMonth(100); err != nil {
		t.Fatal(err)
	}
	if err := s.Report(10, 18); err != nil {
		t.Fatal(err)
	}
	if err := s.AddExclude(13, 18); err != nil {
		t.Fatal(err)
	}
	result, err := s.Close()
	if err != nil {
		t.Fatal(err)
	}
	if result.DowntimeMinutes != 0 {
		t.Fatalf("D = %d, want 0; 8-minute failure becomes 3 after a 5-minute exclusion", result.DowntimeMinutes)
	}

	config.JitterMinutes = 3
	s = mustNew(t, config)
	if err := s.NewMonth(100); err != nil {
		t.Fatal(err)
	}
	if err := s.Report(10, 18); err != nil {
		t.Fatal(err)
	}
	if err := s.AddExclude(13, 18); err != nil {
		t.Fatal(err)
	}
	result, err = s.Close()
	if err != nil {
		t.Fatal(err)
	}
	if result.DowntimeMinutes != 3 {
		t.Fatalf("D = %d, want 3 for a residual exactly equal to g", result.DowntimeMinutes)
	}
}

func TestAvailabilityFloorAndTierBoundaries(t *testing.T) {
	config := exampleConfig()
	config.LengthMinutes = 1000
	config.JitterMinutes = 1
	config.Tier1Availability = 900000
	config.Tier2Availability = 800000
	config.Tier3Availability = 700000
	config.AnnualCreditLimit = 100000
	config.MonthlyExcludeLimit = 1000

	cases := []struct {
		downtime int
		base     int
	}{
		{100, 0},
		{200, 10},
		{300, 25},
		{400, 100},
	}
	for _, tc := range cases {
		s := mustNew(t, config)
		if err := s.NewMonth(100); err != nil {
			t.Fatal(err)
		}
		if err := s.Report(0, tc.downtime); err != nil {
			t.Fatal(err)
		}
		result, err := s.Close()
		if err != nil {
			t.Fatal(err)
		}
		if result.Availability != 1_000_000-int64(tc.downtime)*1000 ||
			result.BasePercent != tc.base {
			t.Fatalf("D=%d result = %+v, want base %d", tc.downtime, result, tc.base)
		}
	}

	floorConfig := config
	floorConfig.LengthMinutes = 7
	floorConfig.MonthlyExcludeLimit = 7
	s := mustNew(t, floorConfig)
	if err := s.NewMonth(100); err != nil {
		t.Fatal(err)
	}
	if err := s.Report(0, 1); err != nil {
		t.Fatal(err)
	}
	result, err := s.Close()
	if err != nil {
		t.Fatal(err)
	}
	if result.Availability != 857142 {
		t.Fatalf("A = %d, want floor 857142", result.Availability)
	}
}

func TestCreditCeilEscalationCapAndAnnualTruncation(t *testing.T) {
	config := exampleConfig()
	config.LengthMinutes = 100
	config.JitterMinutes = 1
	config.Tier1Availability = 999000
	config.Tier2Availability = 900000
	config.Tier3Availability = 800000
	config.Tier1Credit = 90
	config.Tier2Credit = 95
	config.Tier3Credit = 100
	config.EscalationStep = 10
	config.EscalationCapMonths = 2
	config.AnnualCreditLimit = 200
	config.MonthlyExcludeLimit = 100
	s := mustNew(t, config)

	wantPercents := []int{90, 100, 100, 100}
	wantCredits := []int64{90, 100, 10, 0}
	for month := range wantPercents {
		if err := s.NewMonth(100); err != nil {
			t.Fatal(err)
		}
		if err := s.Report(0, 10); err != nil {
			t.Fatal(err)
		}
		result, err := s.Close()
		if err != nil {
			t.Fatal(err)
		}
		if result.AppliedPercent != wantPercents[month] || result.Credit != wantCredits[month] {
			t.Fatalf("month %d = %+v, want pct %d credit %d", month, result,
				wantPercents[month], wantCredits[month])
		}
	}
}

func TestCreditCeil(t *testing.T) {
	config := exampleConfig()
	config.LengthMinutes = 100
	config.Tier1Availability = 999000
	config.Tier2Availability = 900000
	config.Tier3Availability = 800000
	config.AnnualCreditLimit = 1000
	config.MonthlyExcludeLimit = 100
	s := mustNew(t, config)

	if err := s.NewMonth(99); err != nil {
		t.Fatal(err)
	}
	if err := s.Report(0, 10); err != nil {
		t.Fatal(err)
	}
	result, err := s.Close()
	if err != nil {
		t.Fatal(err)
	}
	if result.Credit != 10 {
		t.Fatalf("credit = %d, want ceil(99*10/100)=10", result.Credit)
	}
}

func TestAnnualResetAcrossYearKeepsStreak(t *testing.T) {
	config := exampleConfig()
	config.LengthMinutes = 100
	config.Tier1Availability = 999000
	config.Tier2Availability = 900000
	config.Tier3Availability = 800000
	config.EscalationStep = 1
	config.EscalationCapMonths = 12
	config.AnnualCreditLimit = 100
	config.MonthlyExcludeLimit = 100
	s := mustNew(t, config)

	var result CloseResult
	for month := 0; month < 13; month++ {
		if err := s.NewMonth(100); err != nil {
			t.Fatal(err)
		}
		if err := s.Report(0, 10); err != nil {
			t.Fatal(err)
		}
		var err error
		result, err = s.Close()
		if err != nil {
			t.Fatal(err)
		}
	}
	if result.AppliedPercent != 22 || result.Credit != 22 {
		t.Fatalf("month 13 = %+v, want pct 22 credit 22", result)
	}
}

func TestCleanMonthResetsStreak(t *testing.T) {
	s := mustNew(t, exampleConfig())
	for range 2 {
		if err := s.NewMonth(10000); err != nil {
			t.Fatal(err)
		}
		if err := s.Report(100, 130); err != nil {
			t.Fatal(err)
		}
		if _, err := s.Close(); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.NewMonth(10000); err != nil {
		t.Fatal(err)
	}
	result, err := s.Close()
	if err != nil {
		t.Fatal(err)
	}
	if result.BasePercent != 0 || result.AppliedPercent != 0 || s.streak != 0 {
		t.Fatalf("clean result = %+v streak=%d, want zero percent and streak 0", result, s.streak)
	}

	if err := s.NewMonth(10000); err != nil {
		t.Fatal(err)
	}
	if err := s.Report(100, 200); err != nil {
		t.Fatal(err)
	}
	result, err = s.Close()
	if err != nil {
		t.Fatal(err)
	}
	if result.AppliedPercent != 10 {
		t.Fatalf("after clean month pct = %d, want 10", result.AppliedPercent)
	}
}

func TestRevokeRemovesOnlyOneExactInterval(t *testing.T) {
	s := mustNew(t, exampleConfig())
	if err := s.NewMonth(10000); err != nil {
		t.Fatal(err)
	}
	if err := s.Report(100, 130); err != nil {
		t.Fatal(err)
	}
	if err := s.Report(100, 130); err != nil {
		t.Fatal(err)
	}
	if err := s.Revoke(100, 130); err != nil {
		t.Fatal(err)
	}
	result, err := s.Close()
	if err != nil {
		t.Fatal(err)
	}
	if result.DowntimeMinutes != 30 {
		t.Fatalf("D = %d, want one remaining 30-minute report", result.DowntimeMinutes)
	}

	s = mustNew(t, exampleConfig())
	if err := s.NewMonth(10000); err != nil {
		t.Fatal(err)
	}
	if err := s.Report(100, 130); err != nil {
		t.Fatal(err)
	}
	if err := s.Revoke(100, 131); err != ErrNotFound {
		t.Fatalf("Revoke error = %v, want ErrNotFound", err)
	}
	if len(s.reports) != 1 {
		t.Fatalf("reports length = %d, failed Revoke must not change state", len(s.reports))
	}
}

func TestExcludeUnionLimit(t *testing.T) {
	config := exampleConfig()
	config.LengthMinutes = 500
	config.MonthlyExcludeLimit = 100
	s := mustNew(t, config)
	if err := s.NewMonth(100); err != nil {
		t.Fatal(err)
	}
	if err := s.AddExclude(110, 120); err != nil {
		t.Fatal(err)
	}
	if err := s.AddExclude(115, 205); err != nil {
		t.Fatal(err)
	}
	if total := totalIntervalLength(s.excludes); total != 95 {
		t.Fatalf("union length = %d, want 95", total)
	}
	if err := s.AddExclude(300, 306); err != ErrExcludeLimitExceeded {
		t.Fatalf("AddExclude error = %v, want ErrExcludeLimitExceeded", err)
	}
	if total := totalIntervalLength(s.excludes); total != 95 {
		t.Fatalf("union length after rejection = %d, want unchanged 95", total)
	}

	s = mustNew(t, config)
	if err := s.NewMonth(100); err != nil {
		t.Fatal(err)
	}
	if err := s.AddExclude(0, 60); err != nil {
		t.Fatal(err)
	}
	if err := s.AddExclude(50, 100); err != nil {
		t.Fatal(err)
	}
	if total := totalIntervalLength(s.excludes); total != 100 {
		t.Fatalf("union length = %d, want exact limit 100", total)
	}
	if err := s.AddExclude(200, 201); err != ErrExcludeLimitExceeded {
		t.Fatalf("one-minute over limit error = %v, want ErrExcludeLimitExceeded", err)
	}
	if total := totalIntervalLength(s.excludes); total != 100 {
		t.Fatalf("union length after over-limit rejection = %d, want 100", total)
	}
}

func TestErrorsAndRejectedOperationsDoNotMutateState(t *testing.T) {
	if _, err := New(exampleConfig()); err != nil {
		t.Fatal(err)
	}
	invalid := exampleConfig()
	invalid.Tier1Credit = invalid.Tier2Credit
	if _, err := New(invalid); err != ErrInvalidArgument {
		t.Fatalf("New error = %v, want ErrInvalidArgument", err)
	}

	s := mustNew(t, exampleConfig())
	if err := s.Report(0, 1); err != ErrNoOpenMonth {
		t.Fatalf("Report error = %v, want ErrNoOpenMonth", err)
	}
	if err := s.AddExclude(0, 1); err != ErrNoOpenMonth {
		t.Fatalf("AddExclude error = %v, want ErrNoOpenMonth", err)
	}
	if err := s.Revoke(0, 1); err != ErrNoOpenMonth {
		t.Fatalf("Revoke error = %v, want ErrNoOpenMonth", err)
	}
	if _, err := s.Close(); err != ErrNoOpenMonth {
		t.Fatalf("Close error = %v, want ErrNoOpenMonth", err)
	}

	if err := s.NewMonth(0); err != ErrInvalidArgument {
		t.Fatalf("NewMonth(0) error = %v, want ErrInvalidArgument", err)
	}
	if err := s.NewMonth(1); err != nil {
		t.Fatal(err)
	}
	if err := s.NewMonth(2); err != ErrMonthAlreadyOpen {
		t.Fatalf("second NewMonth error = %v, want ErrMonthAlreadyOpen", err)
	}
	if err := s.Report(-1, 1); err != ErrInvalidArgument {
		t.Fatalf("Report invalid error = %v, want ErrInvalidArgument", err)
	}
	if err := s.Report(1, 1); err != ErrInvalidArgument {
		t.Fatalf("Report empty error = %v, want ErrInvalidArgument", err)
	}
	if err := s.Report(0, exampleConfig().LengthMinutes+1); err != ErrInvalidArgument {
		t.Fatalf("Report out of bounds error = %v, want ErrInvalidArgument", err)
	}
	if s.fee != 1 || len(s.reports) != 0 {
		t.Fatalf("state after rejected operations changed: fee=%d reports=%d", s.fee, len(s.reports))
	}

	invalidExcludeConfig := exampleConfig()
	invalidExcludeConfig.MonthlyExcludeLimit = 0
	s = mustNew(t, invalidExcludeConfig)
	if err := s.NewMonth(1); err != nil {
		t.Fatal(err)
	}
	if err := s.AddExclude(500, 501); err != ErrExcludeLimitExceeded {
		t.Fatalf("zero-limit AddExclude error = %v, want ErrExcludeLimitExceeded", err)
	}
	if err := s.AddExclude(-1, 0); err != ErrInvalidArgument {
		t.Fatalf("invalid AddExclude error = %v, want ErrInvalidArgument", err)
	}
}

func TestConcurrentOperationsAreSerializable(t *testing.T) {
	s := mustNew(t, exampleConfig())
	if err := s.NewMonth(10000); err != nil {
		t.Fatal(err)
	}

	const goroutines = 32
	const perGoroutine = 20
	var waitGroup sync.WaitGroup
	for worker := 0; worker < goroutines; worker++ {
		waitGroup.Add(1)
		go func(worker int) {
			defer waitGroup.Done()
			for i := 0; i < perGoroutine; i++ {
				start := (worker*perGoroutine + i) * 10
				if start+8 > exampleConfig().LengthMinutes {
					start = 100 + ((worker*perGoroutine + i) % 100)
				}
				_ = s.Report(start, start+8)
			}
		}(worker)
	}
	waitGroup.Wait()

	if len(s.reports) != goroutines*perGoroutine {
		t.Fatalf("registered reports = %d, want %d", len(s.reports), goroutines*perGoroutine)
	}
	result, err := s.Close()
	if err != nil {
		t.Fatal(err)
	}
	if result.DowntimeMinutes < 0 || result.DowntimeMinutes > exampleConfig().LengthMinutes {
		t.Fatalf("D out of range: %d", result.DowntimeMinutes)
	}
}
