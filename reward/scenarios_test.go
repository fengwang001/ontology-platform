package reward

import (
	"errors"
	"fmt"
	"sync"
	"testing"
)

func TestSpecExampleN7(t *testing.T) {
	sys := newSpecSystem(t, 2)
	games := score1300Etc(t)
	play(t, sys, games)
	ts := int64(len(games)) + 10
	if err := sys.Settle(ts); err != nil {
		t.Fatal(err)
	}
	type want struct {
		rank   int64
		tier   int
		amount int64
	}
	wants := map[string]want{
		"a": {1, 1, 1500}, "b": {1, 1, 1500},
		"c": {3, 2, 200},
		"d": {4, 3, 50}, "e": {4, 3, 50}, "f": {4, 3, 50},
		"g": {7, 3, 50},
	}
	for p, w := range wants {
		e, err := sys.Result(p)
		if err != nil {
			t.Fatalf("Result(%s): %v", p, err)
		}
		if e.Rank != w.rank || e.Tier != w.tier || e.Amount != w.amount || !e.Eligible || e.Claimed {
			t.Fatalf("%s entry=%+v want rank=%d tier=%d amount=%d", p, e, w.rank, w.tier, w.amount)
		}
		got, err := sys.Claim(ts+1, p)
		if err != nil || got != w.amount {
			t.Fatalf("Claim(%s)=%d,%v want %d", p, got, err, w.amount)
		}
		e2, _ := sys.Result(p)
		if !e2.Claimed {
			t.Fatalf("%s Claimed flag not set", p)
		}
	}
	if _, err := sys.Claim(ts+2, "a"); !errors.Is(err, ErrClaimed) {
		t.Fatalf("re-claim err=%v want ErrClaimed", err)
	}
}

func TestSpecExampleN6CrossMultiplyEquality(t *testing.T) {
	sys := newSpecSystem(t, 2)
	var games []game
	layout := []struct {
		p    string
		w, l int
	}{
		{"a", 2, 0}, {"b", 2, 0}, {"c", 2, 1},
		{"d", 1, 1}, {"e", 1, 1}, {"f", 1, 1},
	}
	for _, x := range layout {
		wins(&games, x.p+"_w", x.p, x.w)
		sinkLoss(t, &games, x.p+"_l", x.p, x.l)
	}
	wins(&games, "g", "g", 1) // g 仅 1 局：1100 分但不合格、不占名次
	play(t, sys, games)
	ts := int64(len(games)) + 10
	if err := sys.Settle(ts); err != nil {
		t.Fatal(err)
	}
	c, err := sys.Result("c")
	if err != nil || c.Rank != 3 || c.Tier != 2 || c.Amount != 200 {
		t.Fatalf("c=%+v err=%v want rank3 tier2 200 (3000<=3000 equality)", c, err)
	}
	d, _ := sys.Result("d")
	if d.Rank != 4 || d.Tier != 3 || d.Amount != 50 {
		t.Fatalf("d=%+v want rank4 tier3 50 (4000>3000)", d)
	}
	g, err := sys.Result("g")
	if err != nil || g.Eligible || g.Rank != 0 || g.Tier != 0 || g.Amount != 0 {
		t.Fatalf("g=%+v err=%v want ineligible zero entry", g, err)
	}
	if _, err := sys.Claim(ts+1, "g"); !errors.Is(err, ErrInelig) {
		t.Fatalf("ineligible claim err=%v want ErrInelig", err)
	}
	if _, err := sys.Claim(ts+1, "nobody"); !errors.Is(err, ErrAbsent) {
		t.Fatalf("never-registered err=%v want ErrAbsent", err)
	}
}

func TestRankOneException(t *testing.T) {
	cases := []struct {
		name    string
		players int
		tied    int
	}{
		{"N=1", 1, 1},
		{"N=2 two tied first", 2, 2},
		{"N=3", 3, 1},
		{"N=5 first tier unreachable by p", 5, 1},
		{"N=1000", 1000, 1},
		{"N=100001", 100001, 1},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			sys := newSpecSystem(t, 1)
			var games []game
			for i := 0; i < c.players; i++ {
				name := fmt.Sprintf("p%06d", i)
				if i < c.tied {
					wins(&games, "top_"+name, name, 2)
				} else {
					wins(&games, "mid_"+name, name, 1)
				}
			}
			play(t, sys, games)
			ts := int64(len(games)) + 10
			if err := sys.Settle(ts); err != nil {
				t.Fatal(err)
			}
			for i := 0; i < c.tied; i++ {
				name := fmt.Sprintf("p%06d", i)
				e, _ := sys.Result(name)
				if e.Rank != 1 || e.Tier != 1 {
					t.Fatalf("%s entry=%+v want rank1 tier1", name, e)
				}
			}
			if c.players > c.tied && c.players < 10 {
				e, _ := sys.Result(fmt.Sprintf("p%06d", c.tied))
				if e.Rank == 1 || e.Tier == 1 {
					t.Fatalf("second player entry=%+v N=%d", e, c.players)
				}
			}
		})
	}
}

func TestSettleN0(t *testing.T) {
	sys := newSpecSystem(t, 0)
	if err := sys.Settle(5); err != nil {
		t.Fatal(err)
	}
	if _, err := sys.Claim(6, "ghost"); !errors.Is(err, ErrAbsent) {
		t.Fatalf("N=0 claim err=%v want ErrAbsent", err)
	}
	if _, err := sys.Result("ghost"); !errors.Is(err, ErrAbsent) {
		t.Fatalf("N=0 result err=%v want ErrAbsent", err)
	}
}

func TestClaimWindowBoundaries(t *testing.T) {
	sys := newSpecSystem(t, 1)
	play(t, sys, []game{{"a", "z1"}, {"b", "z2"}})
	const ts int64 = 10
	if err := sys.Settle(ts); err != nil {
		t.Fatal(err)
	}
	if got, err := sys.Claim(ts, "a"); err != nil || got != 1500 {
		t.Fatalf("claim at ts got %d,%v want 1500", got, err)
	}
	if got, err := sys.Claim(ts+999, "b"); err != nil || got != 1500 {
		t.Fatalf("claim ts+999 got %d,%v want 1500", got, err)
	}
	if _, err := sys.Claim(ts+1000, "a"); !errors.Is(err, ErrExpired) {
		t.Fatalf("ts+Wc err=%v want ErrExpired", err)
	}
	if _, err := sys.Claim(ts+1001, "b"); !errors.Is(err, ErrExpired) {
		t.Fatalf("unclaimed after window err=%v want ErrExpired", err)
	}
}

func TestClaimWithinWindowAfterStart(t *testing.T) {
	sys := newSpecSystem(t, 1)
	play(t, sys, []game{{"a", "z"}})
	const ts int64 = 10
	if err := sys.Settle(ts); err != nil {
		t.Fatal(err)
	}
	if err := sys.Start(ts + 5); err != nil {
		t.Fatal(err)
	}
	got, err := sys.Claim(ts+999, "a")
	if err != nil || got != 1500 {
		t.Fatalf("claim after Start got %d,%v want 1500", got, err)
	}
}

func TestSettleOverwriteVoidsOldSettlement(t *testing.T) {
	sys := newSpecSystem(t, 1)
	play(t, sys, []game{{"a", "z"}})
	if err := sys.Settle(10); err != nil {
		t.Fatal(err)
	}
	if got, err := sys.Claim(11, "a"); err != nil || got != 1500 {
		t.Fatalf("first claim=%d,%v", got, err)
	}
	if err := sys.Start(20); err != nil {
		t.Fatal(err)
	}
	if err := sys.Report(21, "a", "z2"); err != nil {
		t.Fatal(err)
	}
	if err := sys.Settle(30); err != nil {
		t.Fatal(err)
	}
	// 旧结算的已领状态随覆盖作废，a 按新榜可再领。
	if got, err := sys.Claim(31, "a"); err != nil || got != 1500 {
		t.Fatalf("claim after overwrite=%d,%v want 1500", got, err)
	}
	// z 第一次结算合格但未领，新结算中 0 局不合格，旧奖作废。
	if _, err := sys.Claim(32, "z"); !errors.Is(err, ErrInelig) {
		t.Fatalf("old unclaimed award must be void: err=%v want ErrInelig", err)
	}
	e, _ := sys.Result("z")
	if e.Eligible || e.Amount != 0 {
		t.Fatalf("z new entry=%+v want ineligible zero", e)
	}
}

func TestRejectionOrder(t *testing.T) {
	sys := newSpecSystem(t, 1)
	// 1) 参数非法优先。
	if _, err := sys.Claim(5, ""); !errors.Is(err, ErrInvalid) {
		t.Fatalf("empty name err=%v want ErrInvalid", err)
	}
	if err := sys.Report(5, "", "b"); !errors.Is(err, ErrInvalid) {
		t.Fatalf("report empty winner err=%v want ErrInvalid", err)
	}
	if err := sys.Report(5, "a", "a"); !errors.Is(err, ErrInvalid) {
		t.Fatalf("report same name err=%v want ErrInvalid", err)
	}
	if _, err := sys.Claim(-1, "x"); !errors.Is(err, ErrInvalid) {
		t.Fatalf("negative now err=%v want ErrInvalid", err)
	}
	if _, err := sys.Claim(1_000_000_000_001, "x"); !errors.Is(err, ErrInvalid) {
		t.Fatalf("huge now err=%v want ErrInvalid", err)
	}
	// 2) 尚无结算。
	play(t, sys, []game{{"a", "z"}})
	if _, err := sys.Claim(50, "nobody"); !errors.Is(err, ErrNoSettle) {
		t.Fatalf("no settle err=%v want ErrNoSettle", err)
	}
	if err := sys.Settle(100); err != nil {
		t.Fatal(err)
	}
	// 3) 时钟回退优先于缺席与合格判定。
	if _, err := sys.Claim(99, "nobody"); !errors.Is(err, ErrClock) {
		t.Fatalf("rollback absent err=%v want ErrClock", err)
	}
	if _, err := sys.Claim(99, "a"); !errors.Is(err, ErrClock) {
		t.Fatalf("rollback eligible err=%v want ErrClock", err)
	}
	// 被拒绝的操作不推进时钟：now=100 仍然合法。
	if _, err := sys.Claim(100, "a"); err != nil {
		t.Fatalf("equal now after rejected rollback err=%v", err)
	}
	// 4) 缺席优先于窗口已过。
	if _, err := sys.Claim(1_000_000, "nobody"); !errors.Is(err, ErrAbsent) {
		t.Fatalf("absent after window err=%v want ErrAbsent", err)
	}
	// 5) 窗口已过优先于不合格；6) 不合格重复领取仍报不合格。
	sys2 := newSpecSystem(t, 5)
	play(t, sys2, []game{{"a", "q"}})
	if err := sys2.Settle(100); err != nil {
		t.Fatal(err)
	}
	if _, err := sys2.Claim(1_000_000, "a"); !errors.Is(err, ErrExpired) {
		t.Fatalf("expired beats ineligible: err=%v want ErrExpired", err)
	}
	if _, err := sys2.Claim(101, "a"); !errors.Is(err, ErrInelig) {
		t.Fatalf("ineligible err=%v want ErrInelig", err)
	}
	if _, err := sys2.Claim(102, "a"); !errors.Is(err, ErrInelig) {
		t.Fatalf("ineligible repeat err=%v want ErrInelig", err)
	}
}

func TestFrozenReportRejectedAndStateUnchanged(t *testing.T) {
	sys := newSpecSystem(t, 1)
	play(t, sys, []game{{"a", "z"}})
	if err := sys.Settle(10); err != nil {
		t.Fatal(err)
	}
	before, _ := sys.Result("a")
	if err := sys.Report(11, "a", "z"); !errors.Is(err, ErrFrozen) {
		t.Fatalf("report while frozen err=%v want ErrFrozen", err)
	}
	after, _ := sys.Result("a")
	if after != before {
		t.Fatalf("settlement entry changed by rejected report: before=%+v after=%+v", before, after)
	}
	// 仍处于冻结：Start 后再截榜，被拒对局不应出现在新榜（全员 0 局）。
	if !sys.Ladder().Frozen() {
		t.Fatal("ladder should remain frozen")
	}
	if err := sys.Start(12); err != nil {
		t.Fatal(err)
	}
	if err := sys.Settle(13); err != nil {
		t.Fatal(err)
	}
	e, _ := sys.Result("a")
	if e.Eligible {
		t.Fatalf("rejected report must not count games: entry=%+v", e)
	}
}

func TestConcurrentClaimsAndReports(t *testing.T) {
	sys := newSpecSystem(t, 1)
	var games []game
	for i := 0; i < 200; i++ {
		wins(&games, fmt.Sprintf("p%d", i), fmt.Sprintf("p%d", i), 1)
	}
	play(t, sys, games)
	ts := int64(len(games) + 10)
	if err := sys.Settle(ts); err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	success := make(chan string, 200)
	errs := make(chan error, 400)
	for i := 0; i < 200; i++ {
		p := fmt.Sprintf("p%d", i)
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err1 := sys.Claim(ts+1, p)
			_, err2 := sys.Claim(ts+1, p)
			switch {
			case err1 == nil && errors.Is(err2, ErrClaimed):
				success <- p
			case errors.Is(err1, ErrClaimed) && err2 == nil:
				success <- p
			default:
				errs <- fmt.Errorf("%s claims err1=%v err2=%v", p, err1, err2)
			}
		}()
	}
	wg.Wait()
	close(success)
	close(errs)
	n := 0
	for range success {
		n++
	}
	if n != 200 {
		t.Fatalf("successful claims=%d want 200", n)
	}
	for err := range errs {
		t.Fatal(err)
	}
}
