package crew

import (
	"math/rand"
	"testing"
)

func TestNextReportBoundaryAfterExistingDuty(t *testing.T) {
	system := setupPerson(t, testConfig())
	if rejection := register(t, system, 0, "p", "d", 0, 500, 0); rejection != nil {
		t.Fatalf("register: %v", rejection)
	}
	start, ok, rejection := system.NextReport(0, "p", 0, "", 3000)
	if rejection != nil || !ok || start != 1100 {
		t.Fatalf("got start=%d ok=%v rejection=%v, want 1100", start, ok, rejection)
	}
}

func TestNextReportRandomMatchesNaive(t *testing.T) {
	random := rand.New(rand.NewSource(7781))
	config := testConfig()
	config.MinimumRest = 60 + random.Intn(180)
	config.SevenDayLimit = 5000
	config.TwentyEightDayLimit = 15000
	actual := NewSystem(config)
	reference := newNaiveSystem(config)
	_ = actual.AddPerson(0, "p")
	_ = reference.addPerson(0, "p")

	for index := 0; index < 18; index++ {
		start := index * 1000
		if index%4 == 1 {
			start += 30
		}
		duty := DutyPeriod{
			PersonID: "p",
			ID:       "d" + string(rune('a'+index)),
			Start:    start,
			End:      start + 200 + random.Intn(200),
			Segments: index % 5,
		}
		_ = actual.Register(start, duty)
		_ = reference.register(start, duty)
	}

	for _, now := range []int{0, 400, 1200, 2500, 5000, 9000} {
		gotStart, gotOK, rejection := actual.NextReport(now, "p", 1, "", 20000)
		if rejection != nil {
			t.Fatalf("now=%d: %v", now, rejection)
		}
		wantStart, wantOK := reference.nextReport(now, "p", 1, "", 20000)
		if gotOK != wantOK || gotStart != wantStart {
			t.Fatalf("now=%d got=(%d,%v) want=(%d,%v)", now, gotStart, gotOK, wantStart, wantOK)
		}
	}
}
