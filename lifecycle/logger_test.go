package lifecycle

import (
	"bytes"
	"strings"
	"testing"
	"time"
)

func TestSettleLoggerRecordsInputRingsBasis(t *testing.T) {
	eng, clock, logger := testEngine(t)
	mustT(t, eng.RegisterType(&ObjectType{
		Name: "Doc",
		Transitions: []TransitionDef{
			{Name: "e1", From: "A", To: "B", Trigger: TriggerExpiry, Duration: time.Minute},
		},
	}))
	mustT(t, eng.RegisterInstance("d1", "Doc", "A", t0))
	clock.Advance(2 * time.Minute)
	_, err := eng.Get("d1")
	if err != nil {
		t.Fatal(err)
	}

	calls := logger.CallsCopy()
	var call *SettleCall
	for i := range calls {
		if calls[i].Caller == "Get" {
			call = &calls[i]
		}
	}
	if call == nil || call.Instance != "d1" || !call.Now.Equal(clock.Now()) {
		t.Fatalf("missing/incorrect call input: %+v", call)
	}
	if len(call.Rings) != 1 || call.Rings[0].Transition != "e1" ||
		call.Rings[0].From != "A" || call.Rings[0].To != "B" ||
		!strings.Contains(call.Rings[0].Basis, "due at") {
		t.Fatalf("ring/basis log incorrect: %+v", call.Rings)
	}

	// WriterLogger renders the same information plus an error line.
	var buf bytes.Buffer
	wl := &WriterLogger{W: &buf}
	wl.LogSettle(*call)
	out := buf.String()
	if !strings.Contains(out, "call=Get") || !strings.Contains(out, "e1") ||
		!strings.Contains(out, "basis:") {
		t.Fatalf("writer log missing fields:\n%s", out)
	}

	errCall := *call
	errCall.Err = "boom"
	buf.Reset()
	wl.LogSettle(errCall)
	if !strings.Contains(buf.String(), "error: boom") {
		t.Fatalf("writer log missing error line:\n%s", buf.String())
	}
}
