package allocation

import "testing"

func testConfig() Config {
	return Config{
		Tiers: []ScaleTier{
			{MinSize: 0, Percent: 100},
			{MinSize: 30, Percent: 110},
			{MinSize: 60, Percent: 120},
			{MinSize: 90, Percent: 150},
		},
		NewCourseBonusPercent: 20,
		LabPercent:            150,
		Ranks: []RankLimit{
			{Rank: "P", Min: 100, Max: 300},
			{Rank: "A", Min: 200, Max: 400},
		},
		ConfirmDeadline: 10,
	}
}

// 规模档位恰等归高档。
func TestScaleTierBoundaryEqual(t *testing.T) {
	cfg := testConfig()
	cases := []struct {
		size, want int
	}{
		{0, 100}, {29, 100},
		{30, 110}, {59, 110},
		{60, 120}, {89, 120},
		{90, 150}, {200, 150},
	}
	for _, c := range cases {
		got, err := scalePercent(cfg.Tiers, c.size)
		if err != nil || got != c.want {
			t.Fatalf("size=%d got=%d want=%d err=%v", c.size, got, c.want, err)
		}
	}
}

// 三系数连乘后一次性取整 vs 分步取整必须可观察出差异。
func TestTripleProductRoundingDiffersFromStepwise(t *testing.T) {
	// hours=7, scale=110%, new=120%, lab=150%
	// 一次性: floor(7*110*120*150/1_000_000) = floor(13.86) = 13
	// 分步取整（每步立即 floor）：
	//   floor(7*110/100)=7; floor(7*120/100)=8; floor(8*150/100)=12
	got, err := computeWorkload(7, 110, 20, 150, true)
	if err != nil {
		t.Fatal(err)
	}
	stepwise := (7 * 110 / 100)
	stepwise = stepwise * 120 / 100
	stepwise = stepwise * 150 / 100
	if got != 13 {
		t.Fatalf("once=%d want 13", got)
	}
	if got == stepwise {
		t.Fatalf("expected observable difference, once=%d stepwise=%d", got, stepwise)
	}
	t.Logf("once=%d stepwise=%d (difference demonstrated)", got, stepwise)

	// 非新开课 newPct 必须恰好为 100，不产生加成。
	plain, _ := computeWorkload(10, 100, 20, 100, false)
	if plain != 10 {
		t.Fatalf("plain=%d want 10", plain)
	}
}

func TestWorkloadOverflowRejected(t *testing.T) {
	if _, err := computeWorkload(1<<40, 1<<40, 0, 1<<40, false); err == nil {
		t.Fatal("expected overflow error")
	}
}
