package hold_test

import (
	"errors"
	"testing"

	"ontology/hold"
)

func TestNewGate(t *testing.T) {
	cases := []struct {
		name      string
		yield     int64
		minSample int64
		wantErr   error
	}{
		{"ok", 90, 50, nil},
		{"ok_edge", 1, 1, nil},
		{"ok_max", 100, 1_000_000_000, nil},
		{"yield_zero", 0, 1, hold.ErrInvalidParam},
		{"yield_too_big", 101, 1, hold.ErrInvalidParam},
		{"nmin_zero", 90, 0, hold.ErrInvalidParam},
		{"nmin_too_big", 90, 1_000_000_001, hold.ErrInvalidParam},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := hold.NewGate(tc.yield, tc.minSample)
			if !errors.Is(err, tc.wantErr) {
				t.Fatalf("NewGate(%d,%d) err=%v, want %v", tc.yield, tc.minSample, err, tc.wantErr)
			}
		})
	}
}

func TestGateRecordAndResume(t *testing.T) {
	cases := []struct {
		name string
		// 每次 Record 的 (good, total)，逐步断言是否触发挂起
		records [][2]int64
		want    []bool
	}{
		// Y=90, Nmin=50：恰等阈值不挂起
		{"exact_threshold_not_suspend", [][2]int64{{90, 100}}, []bool{false}},
		// 89/100 < 90% 且样本够：挂起
		{"below_threshold_suspend", [][2]int64{{89, 100}}, []bool{true}},
		// 样本不足 Nmin 不判定
		{"below_nmin_no_judge", [][2]int64{{0, 49}}, []bool{false}},
		// 样本恰等 Nmin 时判定
		{"exact_nmin_judge", [][2]int64{{0, 50}}, []bool{true}},
		// 跨多次累计到 Nmin 后判定
		{"accumulate_then_judge", [][2]int64{{40, 40}, {4, 10}}, []bool{false, true}},
		// 累计后恰等不挂起
		{"accumulate_exact", [][2]int64{{40, 40}, {50, 60}}, []bool{false, false}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			g, err := hold.NewGate(90, 50)
			if err != nil {
				t.Fatal(err)
			}
			for idx, rec := range tc.records {
				got := g.Record(2, rec[0], rec[1])
				want := tc.want[idx]
				fGood, fTotal := g.Stats(2)
				t.Logf("Record(good=%d,total=%d) -> suspend=%v (fGood=%d fTotal=%d, 判据 fTotal>=50 && fGood*100<90*fTotal)",
					rec[0], rec[1], got, fGood, fTotal)
				if got != want {
					t.Fatalf("record %d: suspend=%v, want %v", idx, got, want)
				}
			}
		})
	}
}

func TestGateResumeClearsStats(t *testing.T) {
	g, err := hold.NewGate(90, 50)
	if err != nil {
		t.Fatal(err)
	}
	if !g.Record(2, 89, 100) || !g.Suspended() {
		t.Fatal("expected suspension")
	}
	if !g.Record(3, 0, 60) {
		t.Fatal("op3 should also suspend")
	}
	g.Resume()
	if g.Suspended() {
		t.Fatal("still suspended after Resume")
	}
	for _, op := range []int{2, 3} {
		if fGood, fTotal := g.Stats(op); fGood != 0 || fTotal != 0 {
			t.Fatalf("op%d stats not cleared: fGood=%d fTotal=%d", op, fGood, fTotal)
		}
	}
	// 恢复后重新累计：同样的不良率需重新攒够 Nmin 才挂起
	if g.Record(2, 0, 49) {
		t.Fatal("should not suspend below Nmin after resume")
	}
	if !g.Record(2, 0, 1) {
		t.Fatal("should suspend once Nmin reached again")
	}
}
