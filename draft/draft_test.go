package draft_test

import (
	"errors"
	"fmt"
	"math/rand"
	"sync"
	"testing"

	"ontology/draft"
)

func exampleDraft(t *testing.T) *draft.Draft {
	t.Helper()
	// n=2 b1=1 c=2 b2=1 T=30 Bk=20 H=10 now0=0
	d, err := draft.New(2, 1, 2, 1, 30, 20, 10, 0)
	if err != nil {
		t.Fatal(err)
	}
	return d
}

func stateAt(t *testing.T, d *draft.Draft, now int64) *draft.State {
	t.Helper()
	st, err := d.State(now)
	if err != nil {
		t.Fatalf("State(%d): %v", now, err)
	}
	return st
}

func TestNewValidation(t *testing.T) {
	good := func(name string, n, b1, c, b2 int, tt, bk int64, h int, now0 int64) {
		if _, err := draft.New(n, b1, c, b2, tt, bk, h, now0); err != nil {
			t.Fatalf("%s: unexpected error: %v", name, err)
		}
	}
	bad := func(name string, n, b1, c, b2 int, tt, bk int64, h int, now0 int64) {
		if _, err := draft.New(n, b1, c, b2, tt, bk, h, now0); !errors.Is(err, draft.ErrInvalid) {
			t.Fatalf("%s: got %v want ErrInvalid", name, err)
		}
	}
	good("min", 1, 0, 0, 0, 1, 0, 2, 0)
	good("max", 5, 5, 10, 0, 1_000_000, 1_000_000, 100_000, 1_000_000_000_000)
	good("n5", 5, 1, 6, 2, 30, 20, 16, 0) // H >= 2n+2(b1+b2)=16
	bad("n0", 0, 0, 0, 0, 1, 0, 2, 0)
	bad("n6", 6, 0, 0, 0, 1, 0, 12, 0)
	bad("b1neg", 2, -1, 0, 0, 1, 0, 4, 0)
	bad("b2big", 2, 0, 0, 6, 1, 0, 4, 0)
	bad("cneg", 2, 0, -1, 0, 1, 0, 4, 0)
	bad("cbig", 2, 0, 5, 0, 1, 0, 4, 0)
	bad("fullc_with_b2", 2, 0, 4, 1, 1, 0, 6, 0)
	bad("t0", 2, 0, 0, 0, 0, 0, 4, 0)
	bad("tbig", 2, 0, 0, 0, 1_000_001, 0, 4, 0)
	bad("bkneg", 2, 0, 0, 0, 1, -1, 4, 0)
	bad("hsmall", 2, 1, 0, 1, 1, 0, 5, 0) // 需要 >= 4+4=8
	bad("hbig", 2, 0, 0, 0, 1, 0, 100_001, 0)
	bad("now0neg", 2, 0, 0, 0, 1, 0, 4, -1)
	bad("now0big", 2, 0, 0, 0, 1, 0, 4, 1_000_000_000_001)
}

// TestExampleReplay 逐步复现题目给出的完整示例。
func TestExampleReplay(t *testing.T) {
	d := exampleDraft(t)

	if err := d.Hover(5, 0, 5); err != nil {
		t.Fatal(err)
	}
	st := stateAt(t, d, 5)
	if st.Step != 0 || st.Team != 1 || !st.KindIsBan || st.Deadline != 50 {
		t.Fatalf("after hover: %+v", st)
	}
	if err := d.Ban(10, 0, 5); err != nil {
		t.Fatal(err)
	}
	st = stateAt(t, d, 10)
	if st.Step != 1 || st.Team != 2 || st.Start != 10 {
		t.Fatalf("after ban1: %+v", st)
	}

	// Ban(45, p2, h7)：用时 35，扣队 2 备用 5，余 15，下一步起于 45。
	if err := d.Ban(45, 2, 7); err != nil {
		t.Fatal(err)
	}
	st = stateAt(t, d, 45)
	if st.Step != 2 || st.Team != 1 || st.Start != 45 || st.Deadline != 95 || st.Reserve[2] != 15 {
		t.Fatalf("after ban2: %+v", st)
	}

	// Pick(139,p3,h3)：步 3 已在 95 超时，玩家 0 预选 5 已被禁，自动选 1。
	if err := d.Pick(139, 3, 3); err != nil {
		t.Fatal(err)
	}
	st = stateAt(t, d, 139)
	if st.Step != 4 || st.Start != 139 || st.Deadline != 170 {
		t.Fatalf("after pick: %+v", st)
	}
	if st.Picks[0] != 1 || st.Picks[3] != 3 {
		t.Fatalf("picks=%v want [1 -1 -1 3]", st.Picks)
	}
	if st.Reserve[1] != 0 || st.Reserve[2] != 1 {
		t.Fatalf("reserve=%v want team1=0 team2=1", st.Reserve)
	}

	// Advance(259)：步 5、步 6 空禁；步 7 在 230 由玩家 2 自动选 2；步 8 X=260 仍等待。
	if err := d.Advance(259); err != nil {
		t.Fatal(err)
	}
	st = stateAt(t, d, 259)
	if st.Step != 7 || st.Team != 1 || st.Start != 230 || st.Deadline != 260 {
		t.Fatalf("advance259: %+v", st)
	}
	if st.Picks[2] != 2 {
		t.Fatalf("player2 auto pick=%d want 2; picks=%v", st.Picks[2], st.Picks)
	}

	// Advance(260)：步 8 取等超时，玩家 1 自动选 4，全部结束。
	if err := d.Advance(260); err != nil {
		t.Fatal(err)
	}
	st = stateAt(t, d, 260)
	if st.Step != st.TotalStep || st.Team != 0 {
		t.Fatalf("advance260: %+v", st)
	}
	wantPicks := []int{1, 4, 2, 3}
	for i, w := range wantPicks {
		if st.Picks[i] != w {
			t.Fatalf("final picks=%v want %v", st.Picks, wantPicks)
		}
	}
	wantBans := [2][]int{{5}, {7}}
	for tm := 1; tm <= 2; tm++ {
		if len(st.Bans[tm-1]) != len(wantBans[tm-1]) {
			t.Fatalf("bans team%d=%v want %v", tm, st.Bans[tm-1], wantBans[tm-1])
		}
		for i := range wantBans[tm-1] {
			if st.Bans[tm-1][i] != wantBans[tm-1][i] {
				t.Fatalf("bans team%d=%v want %v", tm, st.Bans[tm-1], wantBans[tm-1])
			}
		}
	}
}

// TestTimeoutEquality 取等算超时：Pick(140) 时步 4 已自动处理，报步类型不符。
func TestTimeoutEquality(t *testing.T) {
	d := exampleDraft(t)
	_ = d.Hover(5, 0, 5)
	_ = d.Ban(10, 0, 5)
	_ = d.Ban(45, 2, 7)
	err := d.Pick(140, 3, 3)
	if !errors.Is(err, draft.ErrWrongKind) {
		t.Fatalf("got %v want ErrWrongKind", err)
	}
	// 被拒不推进时钟：以同 now 的 Advance 重做入口处理。
	if err := d.Advance(140); err != nil {
		t.Fatal(err)
	}
	st := stateAt(t, d, 140)
	if st.Step != 4 || st.Team != 2 || !st.KindIsBan {
		t.Fatalf("after advance140: %+v", st)
	}
	if st.Picks[0] != 1 || st.Picks[2] != 2 {
		t.Fatalf("auto picks=%v want p0=1 p2=2", st.Picks)
	}
	if st.Reserve[1] != 0 || st.Reserve[2] != 0 {
		t.Fatalf("reserve=%v want both 0", st.Reserve)
	}
}

// TestElapsedExactlyT 用时恰等于 T 不扣备用；备用为 0 时 X=s+T，取等超时。
func TestElapsedExactlyT(t *testing.T) {
	d, err := draft.New(1, 0, 2, 0, 30, 20, 4, 0)
	if err != nil {
		t.Fatal(err)
	}
	if err := d.Pick(30, 0, 1); err != nil { // now-s=30=T，不扣
		t.Fatal(err)
	}
	st := stateAt(t, d, 30)
	if st.Reserve[1] != 20 || st.Reserve[2] != 20 {
		t.Fatalf("reserve=%v want 20/20", st.Reserve)
	}
	// 队 2 的 X=30+30+20=80；79 手动，扣 79-30-30=19，余 1（未取等）。
	if err := d.Pick(79, 1, 2); err != nil {
		t.Fatal(err)
	}
	st = stateAt(t, d, 79)
	if st.Reserve[2] != 1 {
		t.Fatalf("reserve2=%d want 1 (79<80)", st.Reserve[2])
	}

	// 备用为 0：X=s+T，取等超时自动选，备用仍为 0。
	d2, err := draft.New(1, 0, 2, 0, 30, 0, 4, 100)
	if err != nil {
		t.Fatal(err)
	}
	st = stateAt(t, d2, 100)
	if st.Deadline != 130 {
		t.Fatalf("deadline=%d want 130", st.Deadline)
	}
	if err := d2.Advance(130); err != nil {
		t.Fatal(err)
	}
	st = stateAt(t, d2, 130)
	if st.Step != 1 || st.Start != 130 || st.Picks[0] != 1 || st.Reserve[1] != 0 {
		t.Fatalf("zero-reserve timeout: step=%d start=%d picks=%v reserve=%v",
			st.Step, st.Start, st.Picks, st.Reserve)
	}
}

// TestAdvanceMultipleSteps 一次 Advance 追赶多步，各步起点恰为逻辑 X。
func TestAdvanceMultipleSteps(t *testing.T) {
	d, err := draft.New(1, 2, 0, 0, 10, 0, 6, 0) // 4 禁 + 2 选；禁用 X=10,20,30,40
	if err != nil {
		t.Fatal(err)
	}
	if err := d.Advance(35); err != nil {
		t.Fatal(err)
	}
	st := stateAt(t, d, 35)
	if st.Step != 3 || st.Start != 30 || st.Deadline != 40 {
		t.Fatalf("got step=%d start=%d X=%d want 3/30/40", st.Step, st.Start, st.Deadline)
	}
	// 空禁不占英雄：4 步全部超时空禁后，英雄 1..6 仍全部可用。
	if err := d.Advance(40); err != nil {
		t.Fatal(err)
	}
	st = stateAt(t, d, 40)
	if st.Step != 4 || st.KindIsBan {
		t.Fatalf("after 4 bans step=%d ban=%v want step4 pick", st.Step, st.KindIsBan)
	}
	if len(st.Bans[0]) != 0 || len(st.Bans[1]) != 0 {
		t.Fatalf("auto bans should be empty, got %v", st.Bans)
	}

	// 紧接着的选人步自动选应拿到最小英雄 1（空禁没有占用英雄）。
	d2, err := draft.New(1, 0, 2, 0, 10, 0, 2, 0)
	if err != nil {
		t.Fatal(err)
	}
	if err := d2.Advance(25); err != nil {
		t.Fatal(err)
	}
	st = stateAt(t, d2, 25)
	if st.Step != st.TotalStep || st.Picks[0] != 1 || st.Picks[1] != 2 {
		t.Fatalf("picks=%v want [1 2]", st.Picks)
	}
}

// TestAutoPickFallback 预选被禁或被选后回退到最小可用。
func TestAutoPickFallback(t *testing.T) {
	// 预选被禁：玩家 0 预选 3，对手先禁掉 3。
	d, err := draft.New(1, 1, 2, 0, 10, 0, 5, 0)
	if err != nil {
		t.Fatal(err)
	}
	if err := d.Hover(0, 0, 3); err != nil {
		t.Fatal(err)
	}
	// 步序：t1 禁、t2 禁、t1 选、t2 选。队 2（玩家1）禁 3。
	if err := d.Advance(10); err != nil { // 队 1 空禁
		t.Fatal(err)
	}
	if err := d.Ban(15, 1, 3); err != nil {
		t.Fatal(err)
	}
	if err := d.Advance(100); err != nil {
		t.Fatal(err)
	}
	st := stateAt(t, d, 100)
	if st.Picks[0] != 1 { // 3 已被禁，最小可用为 1
		t.Fatalf("fallback after ban picks=%v want p0=1", st.Picks)
	}

	// 预选被选：c=0 时队 1 先选；玩家 1 预选 2，而玩家 0 先选走 2。
	d2, err := draft.New(1, 0, 2, 0, 100, 1000, 5, 0)
	if err != nil {
		t.Fatal(err)
	}
	if err := d2.Hover(0, 1, 2); err != nil {
		t.Fatal(err)
	}
	if err := d2.Pick(10, 0, 2); err != nil {
		t.Fatal(err)
	}
	if err := d2.Advance(10_000); err != nil {
		t.Fatal(err)
	}
	st = stateAt(t, d2, 10_000)
	if st.Picks[1] != 1 { // 预选 2 已被选走，回退最小可用 1
		t.Fatalf("fallback after pick picks=%v want p1=1", st.Picks)
	}
}

// TestZeroPhases b1/b2 为 0、c 为 0 与 2n 的步序与归属。
func TestZeroPhases(t *testing.T) {
	// b1=b2=0：只有选人。
	d, err := draft.New(2, 0, 2, 0, 10, 0, 4, 0)
	if err != nil {
		t.Fatal(err)
	}
	st := stateAt(t, d, 0)
	if st.TotalStep != 4 || st.KindIsBan {
		t.Fatalf("total=%d ban=%v want 4 picks", st.TotalStep, st.KindIsBan)
	}

	// c=0：二阶段先禁方是第 0 手所属队（队 1）；先 4 禁后 2 选。
	d2, err := draft.New(1, 1, 0, 1, 10, 0, 6, 0)
	if err != nil {
		t.Fatal(err)
	}
	st = stateAt(t, d2, 0)
	if st.TotalStep != 6 {
		t.Fatalf("total=%d want 6", st.TotalStep)
	}
	if err := d2.Advance(35); err != nil { // 4 个禁用步 X=10,20,30,40：停在第 4 步前
		t.Fatal(err)
	}
	st = stateAt(t, d2, 35)
	if st.Step != 3 || !st.KindIsBan {
		t.Fatalf("at 35 step=%d ban=%v want step3 ban", st.Step, st.KindIsBan)
	}
	if err := d2.Advance(40); err != nil { // 取等处理最后一个禁用步
		t.Fatal(err)
	}
	st = stateAt(t, d2, 40)
	if st.Step != 4 || st.KindIsBan {
		t.Fatalf("after 4 bans step=%d ban=%v want step4 pick", st.Step, st.KindIsBan)
	}

	// c=2n 时 b2 必须为 0：直接全选人，New(b2>0) 已在参数测试中拒绝。
	d3, err := draft.New(2, 1, 4, 0, 10, 0, 6, 0)
	if err != nil {
		t.Fatal(err)
	}
	if err := d3.Advance(100); err != nil {
		t.Fatal(err)
	}
	st = stateAt(t, d3, 100)
	if st.Step != st.TotalStep {
		t.Fatalf("c=2n not finished: step=%d total=%d", st.Step, st.TotalStep)
	}
}

// TestSecondPhaseStarter 二阶段先禁方等于第 c 手所属队（n=5 c=6 -> 队 2）。
func TestSecondPhaseStarter(t *testing.T) {
	d, err := draft.New(5, 0, 6, 1, 10, 0, 14, 0)
	if err != nil {
		t.Fatal(err)
	}
	// 前 6 个选人步 X=10,...,60；60 取等处理完，第 7 步（二阶段首禁）X=70 仍等待。
	if err := d.Advance(60); err != nil {
		t.Fatal(err)
	}
	st := stateAt(t, d, 60)
	if st.Step != 6 || st.Team != 2 || !st.KindIsBan {
		t.Fatalf("step=%d team=%d ban=%v want step6 team2 ban", st.Step, st.Team, st.KindIsBan)
	}
}

// snapshot 保存用于“被拒不改状态”比对的关键字段。
type snapshot struct {
	step    int
	team    int
	start   int64
	dead    int64
	reserve [3]int64
	picks   string
	bans    string
	hover   string
}

func snap(st *draft.State) snapshot {
	return snapshot{
		step: st.Step, team: st.Team, start: st.Start, dead: st.Deadline,
		reserve: st.Reserve,
		picks:   fmt.Sprint(st.Picks),
		bans:    fmt.Sprint(st.Bans),
		hover:   fmt.Sprint(st.Hover),
	}
}

// TestRejectionOrder 严格校验拒绝次序及“被拒不改状态、不推进时钟”。
func TestRejectionOrder(t *testing.T) {
	d := exampleDraft(t)

	// 参数非法最先：坏玩家 + 时钟回退 + 已结束等同时存在。
	cases := []struct {
		name string
		fn   func() error
		want error
	}{
		{"ban bad hero", func() error { return d.Ban(5, 0, 0) }, draft.ErrInvalid},
		{"ban bad player", func() error { return d.Ban(5, 99, 1) }, draft.ErrInvalid},
		{"ban bad now", func() error { return d.Ban(-1, 0, 1) }, draft.ErrInvalid},
		{"pick bad hero", func() error { return d.Pick(5, 0, 11) }, draft.ErrInvalid},
		{"hover bad now", func() error { return d.Hover(11, 0, 0) }, draft.ErrInvalid},
		{"advance bad now", func() error { return d.Advance(1_000_000_000_001) }, draft.ErrInvalid},
		{"clock rewind ban", func() error { return d.Ban(4, 0, 1) }, draft.ErrClockRewind},
		{"clock rewind pick", func() error { return d.Pick(4, 0, 1) }, draft.ErrClockRewind},
		{"clock rewind hover", func() error { return d.Hover(4, 0, 1) }, draft.ErrClockRewind},
		{"clock rewind advance", func() error { return d.Advance(4) }, draft.ErrClockRewind},
		{"pick on ban step", func() error { return d.Pick(5, 0, 1) }, draft.ErrWrongKind},
		{"ban wrong team", func() error { return d.Ban(5, 2, 1) }, draft.ErrWrongTeam},
		{"pick wrong team player", func() error {
			// 无禁用：步 0 即队 1 的选人步；玩家 2 属队 2，报归属不符。
			d2, err := draft.New(2, 0, 4, 0, 100, 1000, 10, 0)
			if err != nil {
				t.Fatal(err)
			}
			return d2.Pick(0, 2, 1)
		}, draft.ErrWrongTeam},
		{"hero unavailable ban", func() error {
			d2 := exampleDraft(t)
			_ = d2.Ban(10, 0, 5)
			_ = d2.Advance(40) // 队 2 禁用步等待中，5 已被禁
			return d2.Ban(40, 2, 5)
		}, draft.ErrHeroBusy},
		{"pick already picked", func() error {
			d2, err := draft.New(1, 0, 2, 0, 100, 1000, 4, 0)
			if err != nil {
				t.Fatal(err)
			}
			_ = d2.Pick(10, 0, 1)
			_ = d2.Pick(20, 1, 2) // 全部选完
			return d2.Pick(20, 0, 3)
		}, draft.ErrFinished},
		{"hover after finished", func() error {
			d2, err := draft.New(1, 0, 2, 0, 10, 0, 2, 0)
			if err != nil {
				t.Fatal(err)
			}
			_ = d2.Advance(100)
			return d2.Hover(100, 0, 1)
		}, draft.ErrFinished},
		{"ban after finished", func() error {
			d2, err := draft.New(1, 0, 2, 0, 10, 0, 2, 0)
			if err != nil {
				t.Fatal(err)
			}
			_ = d2.Advance(100) // 两个选人步全部超时结束
			return d2.Ban(100, 0, 1)
		}, draft.ErrFinished},
	}

	before := snap(stateAt(t, d, 5))
	for _, tc := range cases {
		err := tc.fn()
		if !errors.Is(err, tc.want) {
			t.Fatalf("%s: got %v want %v", tc.name, err, tc.want)
		}
	}
	// 主 d 的所有被拒操作不得改变状态（用 now=5 观察）。
	after := snap(stateAt(t, d, 5))
	if before != after {
		t.Fatalf("rejected ops changed state:\nbefore=%+v\nafter =%+v", before, after)
	}

	// 已选校验在英雄可用性之前：已选玩家给一个可用英雄仍报 AlreadyPick。
	d2, err := draft.New(1, 0, 2, 0, 100, 1000, 4, 0)
	if err != nil {
		t.Fatal(err)
	}
	if err := d2.Pick(10, 0, 1); err != nil {
		t.Fatal(err)
	}
	if err := d2.Hover(20, 0, 2); !errors.Is(err, draft.ErrAlreadyPick) {
		t.Fatalf("hover by picked player: got %v want ErrAlreadyPick", err)
	}
	// Pick 拒绝次序：已选 > 英雄不可用。让玩家 0 在属于队 1 的第二手（第 3 手，X=300）
	// 已处于选完状态不可行，故改为：玩家 0 已选后，队 2 选人步上玩家 1 已选场景——
	// 这里直接让玩家 0 在他自己选完后再尝试（此时步属队 2，故先调整到队 1 的选人步）。
	d3, err := draft.New(2, 0, 4, 0, 1000, 1000, 10, 0)
	if err != nil {
		t.Fatal(err)
	}
	if err := d3.Pick(0, 0, 1); err != nil { // 第 0 手队 1，玩家 0 选 1
		t.Fatal(err)
	}
	// 第 1、2 手属于队 2，跳过：队 2 两人各选一个（推进至第 3 手队 1）。
	if err := d3.Pick(0, 2, 2); err != nil {
		t.Fatal(err)
	}
	if err := d3.Pick(0, 3, 3); err != nil {
		t.Fatal(err)
	}
	if err := d3.Pick(0, 0, 1); !errors.Is(err, draft.ErrAlreadyPick) {
		t.Fatalf("picked player with busy hero: got %v want ErrAlreadyPick", err)
	}
}

// randParams 生成一组合法随机参数。
func randParams(rng *rand.Rand) (n, b1, c, b2 int, tt, bk int64, h int, now0 int64) {
	n = 1 + rng.Intn(5)
	b1 = rng.Intn(4)
	b2 = rng.Intn(4)
	c = rng.Intn(2*n + 1)
	if c == 2*n {
		b2 = 0
	}
	minH := 2*n + 2*(b1+b2)
	h = minH + rng.Intn(40)
	tt = int64(1 + rng.Intn(40))
	bk = int64(rng.Intn(60))
	now0 = int64(rng.Intn(5))
	return
}

func sameStates(a, b *draft.State) bool {
	if a.Step != b.Step || a.Team != b.Team || a.KindIsBan != b.KindIsBan ||
		a.Start != b.Start || a.Deadline != b.Deadline || a.Reserve != b.Reserve {
		return false
	}
	for i := range a.Picks {
		if a.Picks[i] != b.Picks[i] || a.Hover[i] != b.Hover[i] {
			return false
		}
	}
	for tm := 0; tm < 2; tm++ {
		if len(a.Bans[tm]) != len(b.Bans[tm]) {
			return false
		}
		for i := range a.Bans[tm] {
			if a.Bans[tm][i] != b.Bans[tm][i] {
				return false
			}
		}
	}
	return true
}

// TestRandomAgainstNaive 1500 组随机操作序列与逐毫秒推进、逐个扫描英雄的朴素模拟
// 逐项对照（错误类型与全量状态），失败时打印参数、输入序列、输出与判定依据。
func TestRandomAgainstNaive(t *testing.T) {
	if testing.Short() {
		t.Skip("short mode")
	}
	rng := rand.New(rand.NewSource(20261004))
	const groups = 1500
	for g := 0; g < groups; g++ {
		n, b1, c, b2, tt, bk, h, now0 := randParams(rng)
		d, err := draft.New(n, b1, c, b2, tt, bk, h, now0)
		if err != nil {
			t.Fatalf("group %d New: %v", g, err)
		}
		nd := newNaive(n, b1, c, b2, tt, bk, h, now0)

		now := now0
		var log []string
		ops := 3 + rng.Intn(12)
		for k := 0; k < ops; k++ {
			if rng.Intn(4) != 0 {
				now += int64(rng.Intn(int(tt + bk + 10)))
			}
			if rng.Intn(20) == 0 && now > now0 {
				now-- // 偶发时钟回退
			}
			player := rng.Intn(2 * n)
			hero := 1 + rng.Intn(h)
			if rng.Intn(15) == 0 {
				hero = 1 + h // 偶发非法英雄
			}
			kind := []string{"ban", "pick", "hover", "advance", "state"}[rng.Intn(5)]

			var gotErr, wantErr error
			switch kind {
			case "ban":
				gotErr = d.Ban(now, player, hero)
				wantErr = nd.manual(now, player, hero, false)
			case "pick":
				gotErr = d.Pick(now, player, hero)
				wantErr = nd.manual(now, player, hero, true)
			case "hover":
				gotErr = d.Hover(now, player, hero)
				wantErr = nd.hoverOp(now, player, hero)
			case "advance":
				gotErr = d.Advance(now)
				wantErr = nd.advance(now)
			case "state":
				var gotSt *draft.State
				gotSt, gotErr = d.State(now)
				var wantSt *draft.State
				wantSt, wantErr = nd.peek(now)
				if gotErr == nil && !sameStates(gotSt, wantSt) {
					t.Fatalf("group %d state mismatch:\n got=%+v\nwant=%+v", g, gotSt, wantSt)
				}
			}
			verdict := "accepted"
			if gotErr != nil {
				verdict = "rejected:" + errName(gotErr)
			}
			log = append(log, fmt.Sprintf("%s(now=%d player=%d hero=%d) -> %s", kind, now, player, hero, verdict))

			if !eqErr(gotErr, wantErr) {
				t.Fatalf("group %d PARAMS n=%d b1=%d c=%d b2=%d T=%d Bk=%d H=%d now0=%d\n"+
					"INPUT op#%d %s(now=%d player=%d hero=%d)\nOUTPUT got=%v want=%v\nJUDGE error mismatch\nLOG:\n%s",
					g, n, b1, c, b2, tt, bk, h, now0,
					k, kind, now, player, hero, gotErr, wantErr, joinLog(log))
			}

			if kind != "state" {
				// 用同 now 的 State 观察双方；被拒操作已回滚，故这只反映“入口处理”后的世界。
				gotSt, gErr := d.State(now)
				wantSt, wErr := nd.peek(now)
				if (gErr == nil) != (wErr == nil) || (gErr != nil && !eqErr(gErr, wErr)) {
					t.Fatalf("group %d observe errors: got=%v want=%v", g, gErr, wErr)
				}
				if gErr != nil {
					continue // 两侧一致拒绝（如时钟回退），无可观察状态
				}
				if !sameStates(gotSt, wantSt) {
					t.Fatalf("group %d PARAMS n=%d b1=%d c=%d b2=%d T=%d Bk=%d H=%d now0=%d\n"+
						"INPUT op#%d %s(now=%d player=%d hero=%d)\nOUTPUT got=%+v\nwant   =%+v\nJUDGE state mismatch\nLOG:\n%s",
						g, n, b1, c, b2, tt, bk, h, now0,
						k, kind, now, player, hero, gotSt, wantSt, joinLog(log))
				}
			}
		}
	}
}

func joinLog(log []string) string {
	out := ""
	for i, line := range log {
		out += fmt.Sprintf("  %d: %s\n", i, line)
	}
	return out
}

// TestLoggedScenario 打印一组固定随机操作序列的输入、输出与判定依据，
// 便于人工核对（go test -v 可见）。
func TestLoggedScenario(t *testing.T) {
	rng := rand.New(rand.NewSource(42))
	n, b1, c, b2, tt, bk, h, now0 := randParams(rng)
	t.Logf("PARAMS n=%d b1=%d c=%d b2=%d T=%d Bk=%d H=%d now0=%d",
		n, b1, c, b2, tt, bk, h, now0)
	d, err := draft.New(n, b1, c, b2, tt, bk, h, now0)
	if err != nil {
		t.Fatal(err)
	}
	nd := newNaive(n, b1, c, b2, tt, bk, h, now0)
	now := now0
	for k := 0; k < 10; k++ {
		now += int64(rng.Intn(int(tt + bk + 10)))
		player := rng.Intn(2 * n)
		hero := 1 + rng.Intn(h)
		kind := []string{"ban", "pick", "hover", "advance", "state"}[rng.Intn(5)]

		var gotErr error
		var gotSt *draft.State
		switch kind {
		case "ban":
			gotErr = d.Ban(now, player, hero)
			_ = nd.manual(now, player, hero, false)
		case "pick":
			gotErr = d.Pick(now, player, hero)
			_ = nd.manual(now, player, hero, true)
		case "hover":
			gotErr = d.Hover(now, player, hero)
			_ = nd.hoverOp(now, player, hero)
		case "advance":
			gotErr = d.Advance(now)
			_ = nd.advance(now)
		case "state":
			gotSt, gotErr = d.State(now)
			_, _ = nd.peek(now)
		}
		verdict := "accepted"
		if gotErr != nil {
			verdict = "rejected:" + errName(gotErr)
		}
		t.Logf("INPUT  %s(now=%d player=%d hero=%d)", kind, now, player, hero)
		t.Logf("OUTPUT %s", verdict)
		if gotSt != nil {
			t.Logf("STATE  step=%d/%d team=%d ban=%v start=%d X=%d reserve=[%d,%d] picks=%v bans=%v",
				gotSt.Step, gotSt.TotalStep, gotSt.Team, gotSt.KindIsBan,
				gotSt.Start, gotSt.Deadline, gotSt.Reserve[1], gotSt.Reserve[2],
				gotSt.Picks, gotSt.Bans)
		}
		t.Logf("JUDGE  %s 依据：参数>时钟>结束>类型>归属>已选>可用 的首条不满足项", verdict)
	}
}

// TestFinalInvariants 与 TestConcurrency：结束态不变量与并发等价串行。
func TestFinalInvariants(t *testing.T) {
	rng := rand.New(rand.NewSource(7))
	for g := 0; g < 100; g++ {
		n, b1, c, b2, tt, bk, h, now0 := randParams(rng)
		d, err := draft.New(n, b1, c, b2, tt, bk, h, now0)
		if err != nil {
			t.Fatal(err)
		}
		st, err := d.State(now0 + 10_000_000)
		if err != nil {
			t.Fatal(err)
		}
		if st.Step != st.TotalStep {
			t.Fatalf("case %d: not finished", g)
		}
		used := map[int]bool{}
		players := 0
		for _, hero := range st.Picks {
			if hero < 1 || used[hero] {
				t.Fatalf("case %d: bad picks %v", g, st.Picks)
			}
			used[hero] = true
			players++
		}
		if players != 2*n {
			t.Fatalf("case %d: picked players=%d want %d", g, players, 2*n)
		}
		for _, bs := range st.Bans {
			for _, hero := range bs {
				if used[hero] {
					t.Fatalf("case %d: banned %d also picked", g, hero)
				}
				used[hero] = true
			}
		}
		if st.Reserve[1] < 0 || st.Reserve[2] < 0 {
			t.Fatalf("case %d: negative reserve %v", g, st.Reserve)
		}
	}
}

// TestConcurrency 并发调用不产生数据竞争（配合 -race），且最终状态确定。
func TestConcurrency(t *testing.T) {
	d, err := draft.New(3, 2, 3, 1, 50, 100, 30, 0)
	if err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	for i := 0; i < 16; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			now := int64(10 + (i%6)*40)
			_ = d.Hover(now, i%6, 1+i%20)
			_ = d.Advance(now)
			_, _ = d.State(now)
		}(i)
	}
	wg.Wait()
	st := stateAt(t, d, 10_000)
	if st.Step != st.TotalStep {
		t.Fatalf("concurrent run not finished: %d/%d", st.Step, st.TotalStep)
	}
	used := map[int]bool{}
	for _, hero := range st.Picks {
		if hero < 1 || used[hero] {
			t.Fatalf("bad picks after concurrent ops: %v", st.Picks)
		}
		used[hero] = true
	}
}
