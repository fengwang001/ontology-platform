package reward

import (
	"errors"
	"fmt"
	"testing"
)

var specTiers = []Tier{{P: 100, A: 500}, {P: 500, A: 200}, {P: 1000, A: 50}}

func newSpecSystem(t *testing.T, gmin int64) *System {
	t.Helper()
	sys, err := New(1000, 100, 100, gmin, 2, 1000, specTiers, 50, 1000)
	if err != nil {
		t.Fatal(err)
	}
	return sys
}

// game 是一条对局输入。
type game struct {
	winner, loser string
}

// play 顺序上报对局，now 从 1 起递增。
func play(t *testing.T, sys *System, games []game) {
	t.Helper()
	for i, g := range games {
		if err := sys.Report(int64(i+1), g.winner, g.loser); err != nil {
			t.Fatalf("report %d (%v): %v", i+1, g, err)
		}
	}
}

// sinkLoss 给 player 制造 times 场负局，每场负于独立沙包，使沙包至多 1 局不合格。
func sinkLoss(t *testing.T, games *[]game, tag string, player string, times int) {
	t.Helper()
	for i := 0; i < times; i++ {
		*games = append(*games, game{fmt.Sprintf("%s_sink_%d", tag, i), player})
	}
}

// wins 给 player 制造 times 场胜局（胜独立沙包）。
func wins(games *[]game, tag string, player string, times int) {
	for i := 0; i < times; i++ {
		*games = append(*games, game{player, fmt.Sprintf("%s_pad_%d", tag, i)})
	}
}

// score1300Etc 构造题例 7 人合格：1300,1300,1200,1100x3,1000，另加不合格沙包。
// Gmin=2：分数 = 1000+100*(胜-负)，1300=2胜0负、1200=2胜1负、1100=1胜1负、1000=1胜2负。
func score1300Etc(t *testing.T) []game {
	var games []game
	layout := []struct {
		p    string
		w, l int
	}{
		{"a", 2, 0}, {"b", 2, 0}, {"c", 2, 1},
		{"d", 1, 1}, {"e", 1, 1}, {"f", 1, 1},
		{"g", 1, 2},
	}
	for _, x := range layout {
		wins(&games, x.p+"_w", x.p, x.w)
		sinkLoss(t, &games, x.p+"_l", x.p, x.l)
	}
	return games
}

func TestNewInvalid(t *testing.T) {
	good := []Tier{{P: 100, A: 500}, {P: 500, A: 200}, {P: 1000, A: 50}}

	type P struct {
		base, w, l, gmin, k, ak int64
		tiers                   []Tier
		rho, wc                 int64
	}
	ok := P{1000, 100, 100, 2, 2, 1000, good, 50, 1000}
	mut := func(fn func(*P)) P {
		p := ok
		fn(&p)
		return p
	}
	bad := []P{
		mut(func(p *P) { p.base = -1 }),
		mut(func(p *P) { p.base = 1_000_001 }),
		mut(func(p *P) { p.w = 0 }),
		mut(func(p *P) { p.w = 10_001 }),
		mut(func(p *P) { p.l = 0 }),
		mut(func(p *P) { p.l = 10_001 }),
		mut(func(p *P) { p.gmin = -1 }),
		mut(func(p *P) { p.gmin = 10_001 }),
		mut(func(p *P) { p.k = -1 }),
		mut(func(p *P) { p.k = 1_000_001 }),
		mut(func(p *P) { p.ak = -1 }),
		mut(func(p *P) { p.ak = 1_000_000_001 }),
		mut(func(p *P) { p.rho = -1 }),
		mut(func(p *P) { p.rho = 101 }),
		mut(func(p *P) { p.wc = 0 }),
		mut(func(p *P) { p.wc = 10_000_000_001 }),
	}
	for i, p := range bad {
		if _, err := New(p.base, p.w, p.l, p.gmin, p.k, p.ak, p.tiers, p.rho, p.wc); !errors.Is(err, ErrInvalid) {
			t.Fatalf("case %d: err=%v want ErrInvalid", i, err)
		}
	}
	nine := []Tier{{100, 100}, {200, 90}, {300, 80}, {400, 70}, {500, 60}, {600, 50}, {700, 40}, {800, 30}, {1000, 20}}
	badTiers := [][]Tier{
		nil,
		{},
		{{P: 0, A: 0}, {P: 1000, A: 0}},
		{{P: 1001, A: 0}},
		{{P: 500, A: 0}, {P: 500, A: 0}},
		{{P: 500, A: 0}, {P: 100, A: 0}, {P: 1000, A: 0}},
		{{P: 500, A: 0}}, // 末档 p 必须为 1000
		{{P: 100, A: -1}, {P: 1000, A: 0}},
		{{P: 100, A: 10}, {P: 1000, A: 20}},
		nine,
	}
	for i, ts := range badTiers {
		if _, err := New(1000, 100, 100, 2, 2, 1000, ts, 50, 1000); !errors.Is(err, ErrInvalid) {
			t.Fatalf("tier case %d: err=%v want ErrInvalid", i, err)
		}
	}
}
