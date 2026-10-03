package frequency

import (
	"sync"
	"testing"
)

func TestPeekMatchesAdmitAndDoesNotMutate(t *testing.T) {
	c := mustController(t, 1000, 10, 2, 4, 10)
	c.Admit("u", "c1", "x", 0)

	before := c.inspect("u")
	peek := c.Peek("u", "c1", "x", 10)
	middle := c.inspect("u")
	admit := c.Admit("u", "c1", "x", 10)
	if peek.Reason != admit.Reason || peek.Evidence != admit.Evidence {
		t.Fatalf("peek = %+v, admit = %+v", peek, admit)
	}
	if len(middle.Records) != len(before.Records) || middle.CreativeRun != before.CreativeRun || middle.LastTime != before.LastTime {
		t.Fatalf("Peek mutated state: before %+v, after %+v", before, middle)
	}
}

func TestAdmitBatchRollback(t *testing.T) {
	interval := mustController(t, 1000, 10, 100, 100, 10)
	result := interval.AdmitBatch("u", []Request{
		{Campaign: "c", Creative: "x", Now: 0},
		{Campaign: "c", Creative: "x", Now: 5},
	})
	if result.Allowed || result.Index != 1 || result.Reason != ReasonCreativeInterval {
		t.Fatalf("unexpected interval batch result: %+v", result)
	}
	if state := interval.inspect("u"); len(state.Records) != 0 || state.LastTime != 0 {
		t.Fatalf("interval batch did not roll back: %+v", state)
	}

	daily := mustController(t, 1000, 10, 100, 1, 10)
	result = daily.AdmitBatch("u", []Request{
		{Campaign: "c", Creative: "x", Now: 0},
		{Campaign: "c", Creative: "y", Now: 1},
	})
	if result.Allowed || result.Index != 1 || result.Reason != ReasonCampaignDaily {
		t.Fatalf("unexpected daily batch result: %+v", result)
	}
	if state := daily.inspect("u"); len(state.Records) != 0 {
		t.Fatalf("daily batch did not roll back: %+v", state)
	}

	result = daily.AdmitBatch("u", nil)
	if result.Index != -1 || result.Reason != ReasonInvalidArgument {
		t.Fatalf("empty batch result = %+v", result)
	}
	result = daily.AdmitBatch("u", make([]Request, 1001))
	if result.Index != -1 || result.Reason != ReasonInvalidArgument {
		t.Fatalf("oversized batch result = %+v", result)
	}
	result = daily.AdmitBatch("u", []Request{{Campaign: "", Creative: "x", Now: 1}})
	if result.Index != 0 || result.Reason != ReasonInvalidArgument {
		t.Fatalf("invalid batch request result = %+v", result)
	}
}

func TestUsersAreIndependentAndPriorities(t *testing.T) {
	c := mustController(t, 1000, 2, 1, 1, 10)
	if d := c.Admit("u1", "c", "x", 0); !d.Allowed {
		t.Fatalf("u1 admit: %+v", d)
	}
	if d := c.Admit("u2", "c", "x", 0); !d.Allowed {
		t.Fatalf("u2 was affected by u1: %+v", d)
	}

	if d := c.Admit("", "c", "x", 0); d.Reason != ReasonInvalidArgument {
		t.Fatalf("empty user reason = %q", d.Reason)
	}
	if d := c.Admit("u1", "", "x", 1); d.Reason != ReasonInvalidArgument {
		t.Fatalf("empty campaign reason = %q", d.Reason)
	}
	if d := c.Admit("u1", "c", "", 1); d.Reason != ReasonInvalidArgument {
		t.Fatalf("empty creative reason = %q", d.Reason)
	}
	if d := c.Admit("u1", "c", "x", -1); d.Reason != ReasonInvalidArgument {
		t.Fatalf("negative now reason = %q", d.Reason)
	}
	if d := c.Admit("u1", "clock", "y", 0); !d.Allowed {
		t.Fatalf("setup admit failed: %+v", d)
	}
	if d := c.Admit("u1", "c", "y", -1); d.Reason != ReasonInvalidArgument {
		t.Fatalf("out-of-range now should be invalid: %+v", d)
	}

	clock := mustController(t, 1000, 10, 100, 100, 10)
	if d := clock.Admit("u", "c", "x", 10); !d.Allowed {
		t.Fatalf("setup clock admit failed: %+v", d)
	}
	if d := clock.Admit("u", "c", "y", 9); d.Reason != ReasonClockRollback {
		t.Fatalf("clock rollback priority = %q (%+v)", d.Reason, d.Evidence)
	}

	saturated := mustController(t, 1000, 1, 1, 1, 10)
	saturated.Admit("u", "c", "x", 0)
	if d := saturated.Admit("u", "c", "x", 5); d.Reason != ReasonCreativeInterval {
		t.Fatalf("creative priority over global/daily: %+v", d)
	}
	if d := saturated.Admit("u", "c", "y", 5); d.Reason != ReasonGlobalWindow {
		t.Fatalf("global priority over daily: %+v", d)
	}
}

func TestConcurrentOperations(t *testing.T) {
	c := mustController(t, 100, 10, 2, 4, 5)
	var wg sync.WaitGroup
	for worker := 0; worker < 12; worker++ {
		user := "independent-user-" + string(rune('a'+worker))
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < 100; i++ {
				now := int64(i)
				cre := "x"
				if i%2 == 0 {
					cre = "y"
				}
				if d := c.Admit(user, "camp", cre, now); d.Reason == ReasonClockRollback || d.Reason == ReasonInvalidArgument {
					t.Errorf("independent user unexpected decision: %+v", d)
					return
				}
			}
		}()
	}

	user := "shared-user"
	for worker := 0; worker < 8; worker++ {
		wg.Add(1)
		go func(worker int) {
			defer wg.Done()
			for i := 0; i < 100; i++ {
				now := int64(i*20 + worker)
				c.Admit(user, "camp", "x", now)
			}
		}(worker)
	}

	wg.Wait()
	state := c.inspect(user)
	if int64(len(state.Records)) > c.config.GlobalLimit {
		t.Fatalf("window records after concurrent calls = %d", len(state.Records))
	}
	for _, record := range state.Records {
		if record.time+c.config.GlobalWindow <= state.LastTime {
			t.Fatalf("expired record retained: %+v", record)
		}
	}
}

func TestConstructorValidation(t *testing.T) {
	invalid := [][5]int64{
		{0, 1, 1, 1, 1},
		{1_000_000_001, 1, 1, 1, 1},
		{1, 0, 1, 1, 1},
		{1, 1_000_001, 1, 1, 1},
		{1, 1, 0, 1, 1},
		{1, 1, 1_000_001, 1, 1},
		{1, 1, 1, 0, 1},
		{1, 1, 1, 1, 0},
		{1, 1, 1, 1, 1_000_000_001},
	}
	for i, args := range invalid {
		if _, err := NewController(args[0], args[1], args[2], args[3], args[4]); err == nil {
			t.Fatalf("invalid case %d unexpectedly succeeded", i)
		}
	}
}
