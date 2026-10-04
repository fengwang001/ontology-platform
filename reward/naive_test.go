package reward

import (
	"errors"
	"fmt"
	"math/rand"
	"sort"
	"strings"
	"testing"
)

type testParams struct {
	base, w, l, gmin, k, ak, rho, wc int64
	tiers                            []Tier
}

// naiveModel 是朴素参考实现：名次用全量排序 + O(N^2) 严格高分计数。
type naiveModel struct {
	base, w, l, gmin, k, ak, rho, wc int64
	tiers                            []Tier
	scores                           map[string]int64
	games                            map[string]int64
	frozen                           bool
	last                             int64
	res                              *naiveSettlement
}

type naiveSettlement struct {
	ts      int64
	entries map[string]*Entry
	claimed map[string]bool
}

func newNaive(p testParams) *naiveModel {
	return &naiveModel{
		base: p.base, w: p.w, l: p.l, gmin: p.gmin, k: p.k, ak: p.ak,
		rho: p.rho, wc: p.wc, tiers: p.tiers,
		scores: map[string]int64{}, games: map[string]int64{},
	}
}

func floorDivNaive(a, b int64) int64 {
	q := a / b
	if r := a % b; r != 0 && a < 0 {
		q--
	}
	return q
}

func (m *naiveModel) report(now int64, winner, loser string) error {
	if winner == "" || loser == "" || winner == loser {
		return ErrInvalid
	}
	if now < m.last {
		return ErrClock
	}
	if m.frozen {
		return ErrFrozen
	}
	m.last = now
	if _, ok := m.scores[winner]; !ok {
		m.scores[winner] = m.base
	}
	if _, ok := m.scores[loser]; !ok {
		m.scores[loser] = m.base
	}
	m.scores[winner] += m.w
	m.games[winner]++
	m.scores[loser] -= m.l
	if m.scores[loser] < 0 {
		m.scores[loser] = 0
	}
	m.games[loser]++
	return nil
}

func (m *naiveModel) settle(now int64) error {
	if now < m.last {
		return ErrClock
	}
	if m.frozen {
		return ErrFrozen
	}
	m.last = now
	m.frozen = true
	var elig []string
	entries := map[string]*Entry{}
	for name := range m.scores {
		e := &Entry{Player: name}
		if m.games[name] >= m.gmin {
			e.Eligible = true
			elig = append(elig, name)
		}
		entries[name] = e
	}
	sort.Slice(elig, func(i, j int) bool {
		if m.scores[elig[i]] != m.scores[elig[j]] {
			return m.scores[elig[i]] > m.scores[elig[j]]
		}
		return elig[i] < elig[j]
	})
	n := int64(len(elig))
	for _, name := range elig {
		var rank int64 = 1
		for _, other := range elig {
			if m.scores[other] > m.scores[name] {
				rank++
			}
		}
		e := entries[name]
		e.Rank = rank
		ti := len(m.tiers) - 1
		if rank == 1 {
			ti = 0
		} else {
			for j, t := range m.tiers {
				if rank*1000 <= n*t.P {
					ti = j
					break
				}
			}
		}
		e.Tier = ti + 1
		e.Amount = m.tiers[ti].A
		if rank <= m.k {
			e.Amount += m.ak
		}
	}
	m.res = &naiveSettlement{ts: now, entries: entries, claimed: map[string]bool{}}
	return nil
}

func (m *naiveModel) start(now int64) error {
	if now < m.last {
		return ErrClock
	}
	if !m.frozen {
		return ErrOpen
	}
	m.last = now
	for name := range m.scores {
		m.scores[name] = m.base + floorDivNaive((m.scores[name]-m.base)*m.rho, 100)
		m.games[name] = 0
	}
	m.frozen = false
	return nil
}

func (m *naiveModel) claim(now int64, player string) (int64, error) {
	if player == "" {
		return 0, ErrInvalid
	}
	if now < m.last {
		return 0, ErrClock
	}
	if m.res == nil {
		return 0, ErrNoSettle
	}
	e, ok := m.res.entries[player]
	if !ok {
		return 0, ErrAbsent
	}
	if now >= m.res.ts+m.wc {
		return 0, ErrExpired
	}
	if !e.Eligible {
		return 0, ErrInelig
	}
	if m.res.claimed[player] {
		return 0, ErrClaimed
	}
	m.res.claimed[player] = true
	e.Claimed = true
	m.last = now
	return e.Amount, nil
}

func errCode(err error) string {
	switch {
	case err == nil:
		return "ok"
	case errors.Is(err, ErrInvalid):
		return "invalid"
	case errors.Is(err, ErrClock):
		return "clock"
	case errors.Is(err, ErrFrozen):
		return "frozen"
	case errors.Is(err, ErrOpen):
		return "open"
	case errors.Is(err, ErrNoSettle):
		return "nosettle"
	case errors.Is(err, ErrAbsent):
		return "absent"
	case errors.Is(err, ErrExpired):
		return "expired"
	case errors.Is(err, ErrInelig):
		return "ineligible"
	case errors.Is(err, ErrClaimed):
		return "claimed"
	default:
		return err.Error()
	}
}

func randomParams(rng *rand.Rand) testParams {
	ntiers := 1 + rng.Intn(4)
	psSet := map[int64]bool{1000: true}
	if ntiers > 1 {
		for len(psSet) < ntiers {
			psSet[int64(1+rng.Intn(999))] = true
		}
	}
	ps := make([]int64, 0, ntiers)
	for v := range psSet {
		ps = append(ps, v)
	}
	sort.Slice(ps, func(i, j int) bool { return ps[i] < ps[j] })
	tiers := make([]Tier, ntiers)
	amount := int64(1_000_000_000)
	for i := range tiers {
		if i > 0 {
			amount = rng.Int63n(amount + 1)
		}
		tiers[i] = Tier{P: ps[i], A: amount}
	}
	return testParams{
		base:  int64(rng.Intn(2000)),
		w:     int64(1 + rng.Intn(500)),
		l:     int64(1 + rng.Intn(500)),
		gmin:  int64(rng.Intn(4)),
		k:     int64(rng.Intn(4)),
		ak:    rng.Int63n(1_000_000_001),
		rho:   int64(rng.Intn(101)),
		wc:    int64(1 + rng.Intn(50)),
		tiers: tiers,
	}
}

var _ = fmt.Sprintf
var _ = strings.Builder{}

func TestRandomAgainstNaive1500(t *testing.T) {
	const sequences = 1500
	rng := rand.New(rand.NewSource(20261004))
	sampleLogs := make([]string, 0, 2)
	for seq := 0; seq < sequences; seq++ {
		p := randomParams(rng)
		sys, err := New(p.base, p.w, p.l, p.gmin, p.k, p.ak, p.tiers, p.rho, p.wc)
		if err != nil {
			t.Fatalf("seq %d New: %v", seq, err)
		}
		model := newNaive(p)
		names := []string{"a", "b", "c", "d", "e", "f", "g", "h"}
		pick := func() string { return names[rng.Intn(len(names))] }

		var log strings.Builder
		fmt.Fprintf(&log, "seq %d params={base:%d w:%d l:%d gmin:%d k:%d ak:%d rho:%d wc:%d tiers:%v}\n",
			seq, p.base, p.w, p.l, p.gmin, p.k, p.ak, p.rho, p.wc, p.tiers)
		var now int64
		steps := 20 + rng.Intn(60)
		for step := 0; step < steps; step++ {
			if rng.Intn(10) == 0 && now > 0 {
				now -= int64(rng.Intn(3))
			} else {
				now += int64(rng.Intn(4))
			}
			if now < 0 {
				now = 0
			}
			switch rng.Intn(5) {
			case 0:
				winner, loser := pick(), pick()
				switch rng.Intn(12) {
				case 0:
					winner = ""
				case 1:
					loser = ""
				case 2:
					loser = winner
				}
				got, want := sys.Report(now, winner, loser), model.report(now, winner, loser)
				fmt.Fprintf(&log, "  s%d Report now=%d w=%q l=%q => %s | naive %s\n",
					step, now, winner, loser, errCode(got), errCode(want))
				if errCode(got) != errCode(want) {
					t.Fatalf("seq %d step %d Report mismatch:\n%s", seq, step, log.String())
				}
			case 1:
				got, want := sys.Settle(now), model.settle(now)
				fmt.Fprintf(&log, "  s%d Settle now=%d => %s | naive %s\n", step, now, errCode(got), errCode(want))
				if errCode(got) != errCode(want) {
					t.Fatalf("seq %d step %d Settle mismatch:\n%s", seq, step, log.String())
				}
			case 2:
				got, want := sys.Start(now), model.start(now)
				fmt.Fprintf(&log, "  s%d Start now=%d => %s | naive %s\n", step, now, errCode(got), errCode(want))
				if errCode(got) != errCode(want) {
					t.Fatalf("seq %d step %d Start mismatch:\n%s", seq, step, log.String())
				}
			case 3:
				player := pick()
				if rng.Intn(10) == 0 {
					player = "ghost"
				}
				gAmt, gErr := sys.Claim(now, player)
				wAmt, wErr := model.claim(now, player)
				fmt.Fprintf(&log, "  s%d Claim now=%d p=%q => amt=%d %s | naive amt=%d %s [point-check]\n",
					step, now, player, gAmt, errCode(gErr), wAmt, errCode(wErr))
				if errCode(gErr) != errCode(wErr) || gAmt != wAmt {
					t.Fatalf("seq %d step %d Claim mismatch:\n%s", seq, step, log.String())
				}
			case 4:
				player := pick()
				ge, gerr := sys.Result(player)
				_, werr := modelResultEntry(model, player)
				if (gerr == nil) != (werr == nil) {
					t.Fatalf("seq %d step %d Result presence got=%v naive=%v:\n%s",
						seq, step, gerr, werr, log.String())
				}
				if gerr == nil {
					we := model.res.entries[player]
					if ge != *we {
						t.Fatalf("seq %d step %d Result got=%+v naive=%+v:\n%s",
							seq, step, ge, *we, log.String())
					}
				}
				fmt.Fprintf(&log, "  s%d Result p=%q => %+v %s\n", step, player, ge, errCode(gerr))
			}
			gScores, gFrozen, _ := sys.Ladder().Current()
			if gFrozen != model.frozen {
				t.Fatalf("seq %d step %d frozen got=%v naive=%v:\n%s",
					seq, step, gFrozen, model.frozen, log.String())
			}
			if len(gScores) != len(model.scores) {
				t.Fatalf("seq %d step %d registered got=%d naive=%d:\n%s",
					seq, step, len(gScores), len(model.scores), log.String())
			}
			for name, ms := range model.scores {
				gs, ok := gScores[name]
				if !ok || gs.Score != ms || gs.Games != model.games[name] {
					t.Fatalf("seq %d step %d ladder[%s] got=%+v naive={%d %d}:\n%s",
						seq, step, name, gs, ms, model.games[name], log.String())
				}
			}
			if model.res == nil {
				if sys.result != nil {
					t.Fatalf("seq %d step %d unexpected settlement:\n%s", seq, step, log.String())
				}
			} else if sys.result == nil || sys.result.ts != model.res.ts {
				t.Fatalf("seq %d step %d settlement ts mismatch:\n%s", seq, step, log.String())
			} else {
				if len(sys.result.entries) != len(model.res.entries) {
					t.Fatalf("seq %d step %d entries len mismatch:\n%s", seq, step, log.String())
				}
				for name, we := range model.res.entries {
					ge, ok := sys.result.entries[name]
					if !ok || *ge != *we {
						t.Fatalf("seq %d step %d entry[%s] got=%+v naive=%+v:\n%s",
							seq, step, name, deref(ge), deref(we), log.String())
					}
					if sys.result.claimed[name] != model.res.claimed[name] {
						t.Fatalf("seq %d step %d claimed[%s] mismatch:\n%s",
							seq, step, name, log.String())
					}
				}
			}
		}
		if seq < 2 {
			sampleLogs = append(sampleLogs, log.String())
		}
	}
	for _, l := range sampleLogs {
		t.Log("\n" + l)
	}
	t.Logf("compared %d random operation sequences against the full-sort naive model", sequences)
}

func modelResultEntry(m *naiveModel, player string) (Entry, error) {
	if m.res == nil {
		return Entry{}, ErrNoSettle
	}
	e, ok := m.res.entries[player]
	if !ok {
		return Entry{}, ErrAbsent
	}
	return *e, nil
}

func deref(e *Entry) Entry {
	if e == nil {
		return Entry{}
	}
	return *e
}

func TestTouchedIndependentOfPopulation(t *testing.T) {
	// 10^3 与 10^5 两档对照：Claim 读取的玩家记录数恒为 2（条目 + 领取表）。
	for _, n := range []int{1_000, 100_000} {
		t.Run(fmt.Sprintf("N=%d", n), func(t *testing.T) {
			sys := newSpecSystem(t, 1)
			var games []game
			for i := 0; i < n; i++ {
				wins(&games, fmt.Sprintf("p%06d", i), fmt.Sprintf("p%06d", i), 1)
			}
			play(t, sys, games)
			ts := int64(len(games)) + 10
			if err := sys.Settle(ts); err != nil {
				t.Fatal(err)
			}
			sys.resetTouched()
			target := fmt.Sprintf("p%06d", n/2)
			if _, err := sys.Claim(ts+1, target); err != nil {
				t.Fatal(err)
			}
			got := sys.touchedCount()
			if got > 2 {
				t.Fatalf("N=%d touched=%d, must be <=2 independent of population", n, got)
			}
			t.Logf("N=%d: Claim read %d player records", n, got)
		})
	}
}
