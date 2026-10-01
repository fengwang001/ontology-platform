package swiss

import (
	"errors"
	"reflect"
	"testing"
)

func mustRegister(t *testing.T, tr *Tournament, names ...string) {
	t.Helper()
	for _, n := range names {
		if _, err := tr.Register(n); err != nil {
			t.Fatalf("Register(%q): %v", n, err)
		}
	}
}

func mustPair(t *testing.T, tr *Tournament, want []Pair, wantBye int) {
	t.Helper()
	pairs, bye, err := tr.Pair()
	if err != nil {
		t.Fatalf("Pair: %v", err)
	}
	if bye != wantBye {
		t.Fatalf("bye = %d, want %d", bye, wantBye)
	}
	if !reflect.DeepEqual(pairs, want) {
		t.Fatalf("pairs = %v, want %v", pairs, want)
	}
}

func mustReport(t *testing.T, tr *Tournament, a, b, r int) {
	t.Helper()
	if err := tr.Report(a, b, r); err != nil {
		t.Fatalf("Report(%d,%d,%d): %v", a, b, r, err)
	}
}

func TestNewRejectsBadRounds(t *testing.T) {
	for _, r := range []int{0, -1, 21, 100} {
		if _, err := New(r); err == nil {
			t.Fatalf("New(%d) expected error", r)
		}
	}
	if _, err := New(1); err != nil {
		t.Fatalf("New(1): %v", err)
	}
	if _, err := New(20); err != nil {
		t.Fatalf("New(20): %v", err)
	}
}

func TestRegisterRejectionOrder(t *testing.T) {
	tr, _ := New(3)
	mustRegister(t, tr, "a", "b")

	if _, err := tr.Register(""); !errors.Is(err, ErrEmptyName) {
		t.Fatalf("empty name: got %v, want ErrEmptyName", err)
	}
	if _, err := tr.Register("a"); !errors.Is(err, ErrDuplicate) {
		t.Fatalf("duplicate: got %v, want ErrDuplicate", err)
	}

	mustPair(t, tr, []Pair{{1, 2}}, 0)
	if _, err := tr.Register(""); !errors.Is(err, ErrStarted) {
		t.Fatalf("after start empty: got %v, want ErrStarted", err)
	}
	if _, err := tr.Register("c"); !errors.Is(err, ErrStarted) {
		t.Fatalf("after start: got %v, want ErrStarted", err)
	}
}

// 三人比赛：第一轮全员 0 分，种子号最大者 3 轮空；
// 1 胜 2 后第二轮 2 号 0 分最低轮空，1、3 配对；第三轮只剩 1 号未轮空。
func TestOddByeSelectionAndByeScore(t *testing.T) {
	tr, _ := New(4)
	mustRegister(t, tr, "p1", "p2", "p3")

	mustPair(t, tr, []Pair{{1, 2}}, 3)
	if got := tr.Standings(); !reflect.DeepEqual(got, []Row{
		{Seed: 3, Score: 2, Buch: 0},
		{Seed: 1, Score: 0, Buch: 0},
		{Seed: 2, Score: 0, Buch: 0},
	}) {
		t.Fatalf("standings after bye = %v", got)
	}

	mustReport(t, tr, 1, 2, 2)
	mustPair(t, tr, []Pair{{1, 3}}, 2)
	mustReport(t, tr, 1, 3, 2)

	// 2 号在第二轮轮空已记 2 分，故终局积分为 1=4, 2=2, 3=2。
	// 1 的真实对手为 2(2)、3(2)，对手分 4；2、3 的真实对手只有 1(4)，对手分 4。
	if got := tr.Standings(); !reflect.DeepEqual(got, []Row{
		{Seed: 1, Score: 4, Buch: 4},
		{Seed: 2, Score: 2, Buch: 4},
		{Seed: 3, Score: 2, Buch: 4},
	}) {
		t.Fatalf("final standings = %v", got)
	}

	// 第三轮：只剩 1 号未轮空，轮空 1；2、3 尚未交手，正常配对。
	mustPair(t, tr, []Pair{{2, 3}}, 1)
	mustReport(t, tr, 2, 3, 2)

	// 第四轮：全员都轮空过且人数仍为奇数（且两两均已交手），无可轮空者。
	pairs, bye, err := tr.Pair()
	if !errors.Is(err, ErrNoPairing) || pairs != nil || bye != 0 {
		t.Fatalf("third Pair = (%v,%d,%v)", pairs, bye, err)
	}
}

// 六人首轮全部打平后第二轮排序为 1..6。
// 1 与相邻的 2 已交手，取其后第一个未交手者 3（而非上下半区对折配 4）；
// 不回溯：(1,3)、(2,4) 后 5 只剩已交手的 6 -> 整体失败，
// 尽管存在 (1,4)、(2,5)、(3,6) 这组合法配对。
func TestGreedyNextUnplayedAndNoBacktracking(t *testing.T) {
	tr, _ := New(3)
	mustRegister(t, tr, "p1", "p2", "p3", "p4", "p5", "p6")
	mustPair(t, tr, []Pair{{1, 2}, {3, 4}, {5, 6}}, 0)
	for _, p := range []Pair{{1, 2}, {3, 4}, {5, 6}} {
		mustReport(t, tr, p.X, p.Y, 1)
	}

	before := tr.Standings()
	pairs, bye, err := tr.Pair()
	if !errors.Is(err, ErrNoPairing) {
		t.Fatalf("Pair = (%v,%d,%v), want ErrNoPairing", pairs, bye, err)
	}
	if tr.inRound || tr.roundsPlayed != 1 {
		t.Fatalf("failed Pair mutated round state: inRound=%v rounds=%d", tr.inRound, tr.roundsPlayed)
	}
	if !reflect.DeepEqual(tr.Standings(), before) {
		t.Fatalf("standings changed after failed Pair: %v vs %v", tr.Standings(), before)
	}
	for i, b := range tr.hadBye {
		if b {
			t.Fatalf("player %d marked as having a bye after failed Pair", i+1)
		}
	}

	// 证明存在别的合法完美匹配。
	played := map[[2]int]bool{{1, 2}: true, {3, 4}: true, {5, 6}: true}
	alt := []Pair{{1, 4}, {2, 5}, {3, 6}}
	seen := map[int]bool{}
	for _, p := range alt {
		a, b := p.X, p.Y
		if a > b {
			a, b = b, a
		}
		if played[[2]int{a, b}] {
			t.Fatalf("alternative pair %v already played", p)
		}
		if seen[p.X] || seen[p.Y] {
			t.Fatalf("alternative matching is not a partition")
		}
		seen[p.X], seen[p.Y] = true, true
	}
}

// 四人三轮后两两均交手过；R=4 时第四轮报 ErrNoPairing。
func TestFourPlayersRoundRobinAndFourthRound(t *testing.T) {
	tr, _ := New(4)
	mustRegister(t, tr, "p1", "p2", "p3", "p4")

	mustPair(t, tr, []Pair{{1, 2}, {3, 4}}, 0)
	mustReport(t, tr, 1, 2, 2)
	mustReport(t, tr, 3, 4, 2)

	mustPair(t, tr, []Pair{{1, 3}, {2, 4}}, 0)
	mustReport(t, tr, 1, 3, 2)
	mustReport(t, tr, 2, 4, 2)

	// 积分 1=4,2=2,3=2,4=0 -> 序列 1,2,3,4；1 与 2、3 都已交手，先配 (1,4)；再 (2,3)。
	mustPair(t, tr, []Pair{{1, 4}, {2, 3}}, 0)
	mustReport(t, tr, 1, 4, 1)
	mustReport(t, tr, 2, 3, 1)

	for i := 0; i < 4; i++ {
		if len(tr.oppList[i]) != 3 {
			t.Fatalf("player %d opponents = %v", i+1, tr.oppList[i])
		}
	}
	for i := 0; i < 4; i++ {
		for j := i + 1; j < 4; j++ {
			if !tr.played[i][j] {
				t.Fatalf("players %d,%d never played", i+1, j+1)
			}
		}
	}

	if _, _, err := tr.Pair(); !errors.Is(err, ErrNoPairing) {
		t.Fatalf("fourth round: got %v, want ErrNoPairing", err)
	}
}

func TestPairRejectionOrder(t *testing.T) {
	// R=1：首轮进行中再 Pair 报 ErrPending（先于轮数上限）。
	tr, _ := New(1)
	mustRegister(t, tr, "a", "b")
	mustPair(t, tr, []Pair{{1, 2}}, 0)
	if _, _, err := tr.Pair(); !errors.Is(err, ErrPending) {
		t.Fatalf("Pair while pending: got %v, want ErrPending", err)
	}
	mustReport(t, tr, 1, 2, 2)
	if _, _, err := tr.Pair(); !errors.Is(err, ErrMaxRounds) {
		t.Fatalf("Pair after R rounds: got %v, want ErrMaxRounds", err)
	}

	tr2, _ := New(3)
	if _, _, err := tr2.Pair(); !errors.Is(err, ErrTooFew) {
		t.Fatalf("Pair with 0 players: got %v, want ErrTooFew", err)
	}
	mustRegister(t, tr2, "solo")
	if _, _, err := tr2.Pair(); !errors.Is(err, ErrTooFew) {
		t.Fatalf("Pair with 1 player: got %v, want ErrTooFew", err)
	}
}

func TestReportRejectionOrder(t *testing.T) {
	tr, _ := New(3)
	mustRegister(t, tr, "a", "b", "c", "d")

	// 没有进行中的轮：即使 r 非法也只报 ErrNoRound。
	if err := tr.Report(1, 2, 9); !errors.Is(err, ErrNoRound) {
		t.Fatalf("Report without round: got %v, want ErrNoRound", err)
	}

	mustPair(t, tr, []Pair{{1, 2}, {3, 4}}, 0)
	if err := tr.Report(1, 2, 9); !errors.Is(err, ErrBadResult) {
		t.Fatalf("bad r: got %v, want ErrBadResult", err)
	}
	// a==b、未知种子、轮空者均不属于本轮的一盘。
	if err := tr.Report(1, 1, 2); !errors.Is(err, ErrUnknownPair) {
		t.Fatalf("a==b: got %v, want ErrUnknownPair", err)
	}
	if err := tr.Report(1, 9, 2); !errors.Is(err, ErrUnknownPair) {
		t.Fatalf("unknown seed: got %v, want ErrUnknownPair", err)
	}
	// 不是本轮对阵的两个已知种子（1、3 本轮不相遇）也按未知对阵拒绝。
	if err := tr.Report(1, 3, 2); !errors.Is(err, ErrUnknownPair) {
		t.Fatalf("non-pair: got %v, want ErrUnknownPair", err)
	}
	mustReport(t, tr, 2, 1, 2) // 反向调用：2 胜 1
	if err := tr.Report(1, 2, 0); !errors.Is(err, ErrAlreadyScore) {
		t.Fatalf("duplicate report: got %v, want ErrAlreadyScore", err)
	}
	mustReport(t, tr, 3, 4, 1) // 本轮最后一盘登记后本轮结束
	// 本轮结束后再报：没有进行中的轮。
	if err := tr.Report(1, 2, 0); !errors.Is(err, ErrNoRound) {
		t.Fatalf("report after round end: got %v, want ErrNoRound", err)
	}
}

// 轮空者不属于本轮任何一盘，对其登记一律按未知对阵拒绝。
func TestReportRejectsByePlayer(t *testing.T) {
	tr, _ := New(3)
	mustRegister(t, tr, "a", "b", "c")
	mustPair(t, tr, []Pair{{1, 2}}, 3)
	if err := tr.Report(1, 3, 2); !errors.Is(err, ErrUnknownPair) {
		t.Fatalf("bye player: got %v, want ErrUnknownPair", err)
	}
}

// 对手分按对手当前积分计；同一轮内各盘登记先后不影响榜单。
func TestBuchholzCurrentScoresAndReportOrder(t *testing.T) {
	run := func(results [][3]int) []Row {
		tr, _ := New(3)
		mustRegister(t, tr, "p1", "p2", "p3", "p4")
		mustPair(t, tr, []Pair{{1, 2}, {3, 4}}, 0)
		mustReport(t, tr, 1, 2, 2)
		mustReport(t, tr, 3, 4, 2)
		mustPair(t, tr, []Pair{{1, 3}, {2, 4}}, 0)
		for _, q := range results {
			mustReport(t, tr, q[0], q[1], q[2])
		}
		return tr.Standings()
	}

	// 第二轮 1 胜 3、2 胜 4：积分 1=4,2=2,3=2,4=0；四人对手分均为 4。
	want := []Row{
		{Seed: 1, Score: 4, Buch: 4},
		{Seed: 2, Score: 2, Buch: 4},
		{Seed: 3, Score: 2, Buch: 4},
		{Seed: 4, Score: 0, Buch: 4},
	}
	orderA := [][3]int{{1, 3, 2}, {2, 4, 2}}
	orderB := [][3]int{{4, 2, 0}, {3, 1, 0}}
	if got := run(orderA); !reflect.DeepEqual(got, want) {
		t.Fatalf("order A = %v, want %v", got, want)
	}
	if got := run(orderB); !reflect.DeepEqual(got, want) {
		t.Fatalf("order B = %v, want %v", got, want)
	}
}

// 总积分不变量：全体积分之和 = 2*已登记盘数 + 2*轮空次数。
func TestTotalScoreInvariant(t *testing.T) {
	tr, _ := New(5)
	mustRegister(t, tr, "p1", "p2", "p3", "p4", "p5")

	check := func(games, byes int) {
		t.Helper()
		total := 0
		for _, r := range tr.Standings() {
			total += r.Score
		}
		if want := 2*games + 2*byes; total != want {
			t.Fatalf("total score = %d, want %d (games=%d byes=%d)", total, want, games, byes)
		}
	}
	check(0, 0)
	_, bye, _ := tr.Pair() // 五人：5 号轮空，对阵 (1,2)、(3,4)
	check(0, 1)            // 轮空分在开轮时即记入
	mustReport(t, tr, 1, 2, 1)
	mustReport(t, tr, 3, 4, 1)
	check(2, 1)
	if bye == 0 {
		t.Fatalf("odd field should produce a bye")
	}

	// 1..4 各 1 分、5 号 2 分；第二轮 4 号（未轮空且最低分中种子最大）轮空，
	// 序列 5,1,2,3 -> (5,1)、(2,3)。
	_, bye2, _ := tr.Pair()
	if bye2 != 4 {
		t.Fatalf("second bye = %d, want 4", bye2)
	}
	mustReport(t, tr, 1, 5, 2)
	mustReport(t, tr, 2, 3, 1)
	check(4, 2)
	if bye2 == bye {
		t.Fatalf("same player got bye twice: %d", bye)
	}
}
