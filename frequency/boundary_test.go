package frequency

import (
	"fmt"
	"testing"
)

func mustController(t *testing.T, wg, cg, k, cc, g0 int64) *Controller {
	t.Helper()
	c, err := NewController(wg, cg, k, cc, g0)
	if err != nil {
		t.Fatalf("NewController() error = %v", err)
	}
	return c
}

func TestCreativeIntervalBoundariesAndCap(t *testing.T) {
	c := mustController(t, 1000, 100, 100, 100, 10)

	cases := []struct {
		camp   string
		cre    string
		now    int64
		reason Reason
		s      int
	}{
		{"c1", "x", 0, ReasonOK, 0},
		{"c1", "x", 9, ReasonCreativeInterval, 1},
		{"c1", "x", 10, ReasonOK, 1},
		{"c1", "x", 29, ReasonCreativeInterval, 2},
		{"c1", "x", 30, ReasonOK, 2},
		{"c1", "x", 59, ReasonCreativeInterval, 3},
		{"c1", "x", 60, ReasonOK, 3},
	}
	for i, tc := range cases {
		got := c.Admit("u", tc.camp, tc.cre, tc.now)
		if got.Reason != tc.reason {
			t.Fatalf("case %d: reason = %q, want %q; evidence %+v", i, got.Reason, tc.reason, got.Evidence)
		}
		if got.Allowed && got.Evidence.CreativeRun != tc.s {
			t.Fatalf("case %d: next run = %d, want %d", i, got.Evidence.CreativeRun, tc.s)
		}
	}

	got := c.Admit("u", "c1", "y", 70)
	state := c.inspect("u")
	if !got.Allowed || state.CreativeRun != 1 {
		t.Fatalf("different creative did not restart run: %+v", got)
	}
	got = c.Admit("u", "c1", "y", 80)
	state = c.inspect("u")
	if !got.Allowed || state.CreativeRun != 2 {
		t.Fatalf("restarted creative run did not advance: %+v", got)
	}
}

func TestGlobalWindowBoundaryAndExample(t *testing.T) {
	c := mustController(t, 100, 3, 2, 4, 10)

	requests := []struct {
		camp, cre string
		now       int64
		want      Reason
	}{
		{"c1", "x", 0, ReasonOK},
		{"c1", "x", 5, ReasonCreativeInterval},
		{"c1", "x", 10, ReasonOK},
		{"c1", "x", 25, ReasonCreativeInterval},
		{"c1", "y", 26, ReasonOK},
		{"c2", "z", 27, ReasonGlobalWindow},
		{"c2", "z", 110, ReasonOK},
	}
	for i, req := range requests {
		got := c.Admit("u", req.camp, req.cre, req.now)
		if got.Reason != req.want {
			t.Fatalf("example case %d: got %q, want %q (%+v)", i, got.Reason, req.want, got.Evidence)
		}
	}

	state := c.inspect("u")
	if len(state.Records) != 2 || state.Records[0].time != 26 || state.Records[1].time != 110 {
		t.Fatalf("retained records = %+v", state.Records)
	}

	boundary := mustController(t, 100, 1, 100, 100, 10)
	if d := boundary.Admit("u", "c", "x", 0); !d.Allowed {
		t.Fatalf("initial admit failed: %+v", d)
	}
	if d := boundary.Admit("u", "c", "y", 99); d.Reason != ReasonGlobalWindow || d.Evidence.WindowCount != 1 {
		t.Fatalf("one before expiry: %+v", d)
	}
	if d := boundary.Admit("u", "c", "y", 100); !d.Allowed || d.Evidence.WindowCount != 0 {
		t.Fatalf("at exact expiry: %+v", d)
	}
}

func TestEffectiveCampaignLimitTightensToOne(t *testing.T) {
	c := mustController(t, 1000, 10, 1, 4, 100)

	sequence := []struct {
		camp, cre string
		now       int64
		want      Reason
		effective int64
	}{
		{"c1", "x", 0, ReasonOK, 4},
		{"c2", "y", 100, ReasonOK, 3},
		{"c2", "z", 200, ReasonOK, 2},
		{"c3", "w", 300, ReasonOK, 1},
		{"c1", "b", 400, ReasonCampaignDaily, 1},
	}
	for i, req := range sequence {
		got := c.Admit("u", req.camp, req.cre, req.now)
		if got.Reason != req.want || got.Evidence.EffectiveDaily != req.effective {
			t.Fatalf("case %d: got %+v, want reason %q effective %d", i, got, req.want, req.effective)
		}
	}
}

func TestDayBoundaryKeepsWindowAndCreativeRun(t *testing.T) {
	c := mustController(t, 100000, 10, 100, 2, 10)

	if d := c.Admit("u", "c1", "x", 86399); !d.Allowed {
		t.Fatalf("admit before midnight failed: %+v", d)
	}
	if d := c.Admit("u", "c1", "x", 86400); d.Reason != ReasonCreativeInterval {
		t.Fatalf("creative interval reset at midnight: %+v", d)
	}
	if d := c.Admit("u", "c1", "y", 86400); !d.Allowed || d.Evidence.WindowCount != 1 || d.Evidence.CampaignDayCount != 0 {
		t.Fatalf("new-day admit did not preserve global window/reset day: %+v", d)
	}
	if d := c.Admit("u", "c2", "z", 86401); !d.Allowed || d.Evidence.CampaignDayCount != 0 {
		t.Fatalf("second campaign on new day failed: %+v", d)
	}
	if d := c.Admit("u", "c1", "a", 86402); !d.Allowed || d.Evidence.CampaignDayCount != 1 {
		t.Fatalf("day limit boundary failed: %+v", d)
	}
	if d := c.Admit("u", "c1", "b", 86403); d.Reason != ReasonCampaignDaily || d.Evidence.CampaignDayCount != 2 || d.Evidence.EffectiveDaily != 2 {
		t.Fatalf("n_c == E should reject: %+v", d)
	}
}

func TestReclamationDoesNotGrowWithAdmits(t *testing.T) {
	for _, admitted := range []int64{1000, 100000} {
		t.Run(fmt.Sprintf("%d", admitted), func(t *testing.T) {
			c := mustController(t, 1000, 1000, 1_000_000, 1_000_000, 1)
			for now := int64(0); now < admitted; now++ {
				cre := "x"
				if now%2 == 1 {
					cre = "y"
				}
				if d := c.Admit("u", "c", cre, now); !d.Allowed {
					t.Fatalf("now %d rejected: %+v", now, d)
				}
			}
			state := c.inspect("u")
			wantRecords := min64(admitted, 1000)
			if int64(len(state.Records)) != wantRecords {
				t.Fatalf("retained records = %d, want %d", len(state.Records), wantRecords)
			}
			if state.Popped > admitted {
				t.Fatalf("popped = %d, admitted = %d", state.Popped, admitted)
			}
			t.Logf("admitted=%d retained=%d popped=%d", admitted, len(state.Records), state.Popped)
		})
	}
}

func min64(a, b int64) int64 {
	if a < b {
		return a
	}
	return b
}
