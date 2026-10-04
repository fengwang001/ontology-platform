package season

import (
	"errors"
	"testing"

	"ontology/ladder"
)

func TestNewInvalid(t *testing.T) {
	ld, _ := ladder.New(0, 1, 1)
	for _, rho := range []int64{-1, 101} {
		if _, err := New(ld, rho); !errors.Is(err, ErrInvalid) {
			t.Fatalf("rho=%d err=%v want ErrInvalid", rho, err)
		}
	}
	if _, err := New(nil, 50); !errors.Is(err, ErrInvalid) {
		t.Fatalf("nil ladder err=%v", err)
	}
}

func TestStateMachine(t *testing.T) {
	ld, _ := ladder.New(1000, 100, 100)
	s, _ := New(ld, 50)
	// Open 时 Start 报未冻结。
	if err := s.Start(1); !errors.Is(err, ErrOpen) {
		t.Fatalf("start while open err=%v want ErrOpen", err)
	}
	if _, _, err := s.Settle(1); err != nil {
		t.Fatal(err)
	}
	// Frozen 时 Settle 报冻结。
	if _, _, err := s.Settle(2); !errors.Is(err, ErrFrozen) {
		t.Fatalf("settle while frozen err=%v want ErrFrozen", err)
	}
	// 时钟回退优先于状态检查：Frozen 下回退的 Settle/Start 报时钟回退。
	if _, _, err := s.Settle(0); !errors.Is(err, ErrClock) {
		t.Fatalf("settle rollback err=%v want ErrClock", err)
	}
	if err := s.Start(0); !errors.Is(err, ErrClock) {
		t.Fatalf("start rollback err=%v want ErrClock", err)
	}
	if err := s.Start(2); err != nil {
		t.Fatal(err)
	}
	if ld.Frozen() {
		t.Fatal("expected open after Start")
	}
	// 成功进入下一赛季循环。
	if _, _, err := s.Settle(3); err != nil {
		t.Fatalf("second settle err=%v", err)
	}
}

func TestSoftReset(t *testing.T) {
	cases := []struct {
		name string
		rho  int64
		// reports 是用于构造积分的对局序列（败者均为专用沙包）。
		reports          []report
		extraLoops       []loop
		scores           map[string]int64
		wantAfterRho50   map[string]int64 // 仅 rho=50 用
		wantAfterRhoEdge map[string]int64 // rho=0 / rho=100 用
	}{
		{
			name: "rho=50 negative delta floors toward minus infinity",
			rho:  50,
			reports: []report{
				{"p1301", "z1"},
				{"z2", "p997"}, {"z2", "p997"}, {"z2", "p997"},
			},
			// p0：1000 次负触底；p1000：一胜 301 负回平。用循环补。
			extraLoops: []loop{
				{winner: "z3", loser: "p0", times: 1000},
				{winner: "p1000", loser: "z4", times: 1},
				{winner: "z5", loser: "p1000", times: 301},
			},
			scores:         map[string]int64{"p1301": 1301, "p997": 997, "p0": 0, "p1000": 1000},
			wantAfterRho50: map[string]int64{"p1301": 1150, "p997": 998, "p0": 500, "p1000": 1000},
		},
		{
			name: "rho=0 everyone back to base",
			rho:  0,
			extraLoops: []loop{
				{winner: "hi", loser: "za", times: 3},    // 1903
				{winner: "zb", loser: "lo", times: 1000}, // 触底 0
			},
			scores:           map[string]int64{"hi": 1903, "lo": 0},
			wantAfterRhoEdge: map[string]int64{"hi": 1000, "lo": 1000},
		},
		{
			name:             "rho=100 scores unchanged",
			rho:              100,
			reports:          []report{{"hi", "zc"}},
			extraLoops:       []loop{{winner: "zd", loser: "lo", times: 1000}},
			scores:           map[string]int64{"hi": 1301, "lo": 0},
			wantAfterRhoEdge: map[string]int64{"hi": 1301, "lo": 0},
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			ld, _ := ladder.New(1000, 301, 1)
			s, _ := New(ld, c.rho)
			var now int64 = 1
			for _, r := range c.reports {
				if err := ld.Report(now, r.winner, r.loser); err != nil {
					t.Fatal(err)
				}
				now++
			}
			for _, l := range c.extraLoops {
				for i := int64(0); i < l.times; i++ {
					if err := ld.Report(now, l.winner, l.loser); err != nil {
						t.Fatal(err)
					}
					now++
				}
			}
			settleAt := now + 100
			ts, snap, err := s.Settle(settleAt)
			if err != nil {
				t.Fatal(err)
			}
			if ts != settleAt {
				t.Fatalf("ts=%d", ts)
			}
			for name, want := range c.scores {
				if snap.Stats[name].Score != want {
					t.Fatalf("before reset %s=%d want %d", name, snap.Stats[name].Score, want)
				}
			}
			if err := s.Start(settleAt + 100); err != nil {
				t.Fatal(err)
			}
			want := c.wantAfterRho50
			if c.rho != 50 {
				want = c.wantAfterRhoEdge
			}
			// Start 后立即再截榜，用第二份快照校验线上积分与对局数清零。
			_, snap2, err := s.Settle(settleAt + 200)
			if err != nil {
				t.Fatal(err)
			}
			for name, ws := range want {
				if snap.Stats[name].Score != c.scores[name] {
					t.Fatalf("old snapshot changed for %s: %d", name, snap.Stats[name].Score)
				}
				if st := snap2.Stats[name]; st.Score != ws || st.Games != 0 {
					t.Fatalf("after reset %s stats=%+v want score=%d games=0", name, st, ws)
				}
			}
		})
	}
}

type report struct{ winner, loser string }

type loop struct {
	winner, loser string
	times         int64
}

func TestSnapshotUnaffectedByStart(t *testing.T) {
	ld, _ := ladder.New(1000, 10, 5)
	s, _ := New(ld, 50)
	_ = ld.Report(1, "a", "b")
	_, snap, err := s.Settle(2)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Start(3); err != nil {
		t.Fatal(err)
	}
	if snap.Stats["a"].Score != 1010 || snap.Stats["a"].Games != 1 {
		t.Fatalf("snapshot mutated by Start: %+v", snap.Stats["a"])
	}
}
