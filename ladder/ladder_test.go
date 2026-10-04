package ladder

import (
	"errors"
	"testing"
)

func TestNewInvalid(t *testing.T) {
	cases := []struct {
		name       string
		base, w, l int64
	}{
		{"base neg", -1, 10, 5},
		{"base too big", 1_000_001, 10, 5},
		{"w zero", 0, 0, 5},
		{"w too big", 0, 10_001, 5},
		{"l zero", 0, 10, 0},
		{"l too big", 0, 10, 10_001},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if _, err := New(c.base, c.w, c.l); !errors.Is(err, ErrInvalid) {
				t.Fatalf("want ErrInvalid, got %v", err)
			}
		})
	}
}

func TestReport(t *testing.T) {
	type op struct {
		now            int64
		winner, loser  string
		wantErr        error
		wantW, wantL   int64
		wantWG, wantLG int64
	}
	cases := []struct {
		name string
		ops  []op
	}{
		{
			name: "new players registered then scored",
			ops: []op{
				{1, "a", "b", nil, 1010, 995, 1, 1},
				{2, "a", "b", nil, 1020, 990, 2, 2},
			},
		},
		{
			name: "loser floor at zero",
			ops: []op{
				{1, "w", "l", nil, 1010, 995, 1, 1},
				{2, "w", "l", nil, 1020, 990, 2, 2},
			},
		},
		{
			name: "empty or same names rejected",
			ops: []op{
				{1, "", "b", ErrInvalid, 1000, 1000, 0, 0},
				{1, "a", "", ErrInvalid, 1000, 1000, 0, 0},
				{1, "a", "a", ErrInvalid, 1000, 1000, 0, 0},
			},
		},
		{
			name: "clock rollback rejected and no state change",
			ops: []op{
				{10, "a", "b", nil, 1010, 995, 1, 1},
				{5, "a", "b", ErrClock, 1010, 995, 1, 1},
				{10, "a", "b", nil, 1020, 990, 2, 2},
			},
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			ld, err := New(1000, 10, 5)
			if err != nil {
				t.Fatal(err)
			}
			for _, o := range c.ops {
				err := ld.Report(o.now, o.winner, o.loser)
				if !errors.Is(err, o.wantErr) {
					t.Fatalf("Report(%d,%q,%q) err=%v want %v", o.now, o.winner, o.loser, err, o.wantErr)
				}
				if o.wantErr != nil {
					continue
				}
				if got := ld.stats[o.winner]; got.Score != o.wantW || got.Games != o.wantWG {
					t.Fatalf("winner stats=%+v want score=%d games=%d", got, o.wantW, o.wantWG)
				}
				if got := ld.stats[o.loser]; got.Score != o.wantL || got.Games != o.wantLG {
					t.Fatalf("loser stats=%+v want score=%d games=%d", got, o.wantL, o.wantLG)
				}
			}
		})
	}
}

func TestLoserClampedToZero(t *testing.T) {
	ld, _ := New(10, 100, 1000)
	if err := ld.Report(1, "w", "l"); err != nil {
		t.Fatal(err)
	}
	if got := ld.stats["l"]; got.Score != 0 {
		t.Fatalf("loser score=%d want 0", got.Score)
	}
	// 再次失败仍触底 0，而非变成负数。
	if err := ld.Report(2, "w", "l"); err != nil {
		t.Fatal(err)
	}
	if got := ld.stats["l"]; got.Score != 0 || got.Games != 2 {
		t.Fatalf("loser stats=%+v want {0 2}", got)
	}
}

func TestFreezeRejectsReportsAndSnapshot(t *testing.T) {
	ld, _ := New(1000, 10, 5)
	if err := ld.Report(1, "a", "b"); err != nil {
		t.Fatal(err)
	}
	snap, err := ld.Freeze(2)
	if err != nil {
		t.Fatal(err)
	}
	if snap.TS != 2 || !ld.Frozen() {
		t.Fatalf("snap.TS=%d frozen=%v", snap.TS, ld.Frozen())
	}
	if got := snap.Players; len(got) != 2 || got[0] != "a" {
		t.Fatalf("snapshot order=%v want [a b]", got)
	}
	// 修改快照不得影响内部状态。
	snap.Stats["a"] = Stats{Score: -1, Games: -1}
	if err := ld.Report(3, "a", "b"); !errors.Is(err, ErrFrozen) {
		t.Fatalf("frozen report err=%v want ErrFrozen", err)
	}
	if got := ld.stats["a"]; got.Score != 1010 || got.Games != 1 {
		t.Fatalf("state changed under freeze: %+v", got)
	}
	if _, err := ld.Freeze(4); !errors.Is(err, ErrFrozen) {
		t.Fatalf("double freeze err=%v", err)
	}
	// 冻结期间时钟回退应被拒绝（不推进时钟，解冻后 now=4 仍合法）。
	if _, err := ld.Freeze(1); !errors.Is(err, ErrClock) {
		t.Fatalf("frozen clock err=%v", err)
	}
}
