package ontology_test

import (
	"errors"
	"fmt"
	"math/rand"
	"strings"
	"sync"
	"testing"

	"ontology/adjust"
	"ontology/authz"
	"ontology/count"
)

func newSysT(t *testing.T, tabs, tpct, lim int64) *adjust.System {
	t.Helper()
	s, err := adjust.New(tabs, tpct, lim)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return s
}

// TestRejectionOrder 验证只报第一个错误：
// 非法 > 不存在 > 状态 > 冲突 > 无Approve > 换人 > 缺Senior > 库存不足。
func TestRejectionOrder(t *testing.T) {
	s := newSysT(t, 2, 5, 500)
	mustOK(t, "add", s.AddLoc("L", 100, 10))
	mustOK(t, "g", authz.Grant(s, "a", true, false))

	type rc struct {
		name string
		fn   func() error
		want error
	}
	rows := []rc{
		{"invalid beats notfound: bad id", func() error { return s.Move("!!", 0) }, adjust.ErrInvalid},
		{"Move delta zero", func() error { return s.Move("L", 0) }, adjust.ErrInvalid},
		{"Move unknown loc", func() error { return s.Move("ZZ", 1) }, adjust.ErrNotFound},
		{"Move understock", func() error { return s.Move("L", -101) }, adjust.ErrUnderstock},
		{"Move over 1e9 invalid", func() error { return s.Move("L", 1_000_000_001) }, adjust.ErrInvalid},
		{"AddLoc duplicate conflict", func() error { return s.AddLoc("L", 1, 1) }, adjust.ErrConflict},
		{"Open empty list", func() error { return count.Open(s, "E", nil) }, adjust.ErrInvalid},
		{"Open dup loc in list", func() error { return count.Open(s, "E", []adjust.ID{"L", "L"}) }, adjust.ErrInvalid},
		{"Open unknown loc notfound", func() error { return count.Open(s, "E", []adjust.ID{"NOPE"}) }, adjust.ErrNotFound},
		{"Submit unknown task", func() error { return count.Submit(s, "NOPE", "L", 1, "u") }, adjust.ErrNotFound},
		{"Approve unknown task", func() error { return authz.Approve(s, "NOPE", "L", "a") }, adjust.ErrNotFound},
		{"Reject unknown task", func() error { return authz.Reject(s, "NOPE", "L", "a") }, adjust.ErrNotFound},
	}
	for _, r := range rows {
		if got := r.fn(); !isErr(r.want, got) {
			t.Errorf("%s: want %v got %v", r.name, r.want, got)
		}
	}

	// 建任务：L 进入 First。
	mustOK(t, "open", count.Open(s, "T", []adjust.ID{"L"}))
	rows2 := []rc{
		{"Open again conflict (loc busy)", func() error { return count.Open(s, "T2", []adjust.ID{"L"}) }, adjust.ErrConflict},
		{"Submit loc not in task -> notfound", func() error {
			mustOK(t, "add2", s.AddLoc("L2", 5, 1))
			return count.Submit(s, "T", "L2", 1, "u")
		}, adjust.ErrNotFound},
		{"Approve in First -> state", func() error { return authz.Approve(s, "T", "L", "a") }, adjust.ErrState},
		{"Reject in First -> state", func() error { return authz.Reject(s, "T", "L", "a") }, adjust.ErrState},
	}
	for _, r := range rows2 {
		if got := r.fn(); !isErr(r.want, got) {
			t.Errorf("%s: want %v got %v", r.name, r.want, got)
		}
	}

	// 制造 Pending：u1 初盘 90（超 tol5），u2 复盘 90（净移动为0，一致）。
	mustOK(t, "first", count.Submit(s, "T", "L", 90, "u1"))
	mustOK(t, "second", count.Submit(s, "T", "L", 90, "u2"))
	v, err := s.View("L")
	mustOK(t, "view", err)
	if v.Phase != adjust.PhasePending {
		t.Fatalf("phase=%d want Pending", v.Phase)
	}
	// 权限次序：无 Approve 的人先报 ErrNoApprove（即便也是盘点人/金额超 Lim）。
	if got := authz.Approve(s, "T", "L", "nobody"); !isErr(adjust.ErrNoApprove, got) {
		t.Errorf("no-perm: want ErrNoApprove got %v", got)
	}
	// 有 Approve 的盘点人报换人（优先于 Senior）。
	mustOK(t, "g1", authz.Grant(s, "u1", true, false))
	if got := authz.Approve(s, "T", "L", "u1"); !isErr(adjust.ErrMustSwitch, got) {
		t.Errorf("counter approver: want ErrMustSwitch got %v", got)
	}
	// 金额 |-10|*10=100 <=500，a 可批。
	mustOK(t, "approve", authz.Approve(s, "T", "L", "a"))

	// Close 次序：未关闭任务全部 Done 后关闭；重复 Close 报状态。
	mustOK(t, "close", count.Close(s, "T"))
	if got := count.Close(s, "T"); !isErr(adjust.ErrState, got) {
		t.Errorf("reclose: want ErrState got %v", got)
	}
	exists, closed := s.TaskClosed("T")
	if !exists || !closed {
		t.Errorf("task should exist and be closed")
	}
}

// TestConcurrency 在高并发混合操作下验证：book>=0、不变量成立、阶段合法、无数据竞争。
func TestConcurrency(t *testing.T) {
	s := newSysT(t, 3, 5, 100000)
	for i := 0; i < 6; i++ {
		loc := adjust.ID(fmt.Sprintf("cL%d", i))
		mustOK(t, "add", s.AddLoc(loc, 1000, 10))
		mustOK(t, "grant", authz.Grant(s, adjust.ID(fmt.Sprintf("cu%d", i)), true, true))
	}
	var wg sync.WaitGroup
	for w := 0; w < 16; w++ {
		wg.Add(1)
		go func(seed int64) {
			defer wg.Done()
			r := rand.New(rand.NewSource(seed))
			for k := 0; k < 400; k++ {
				i := r.Intn(6)
				loc := adjust.ID(fmt.Sprintf("cL%d", i))
				u := adjust.ID(fmt.Sprintf("cu%d", r.Intn(6)))
				switch r.Intn(6) {
				case 0:
					_ = s.Move(loc, int64(r.Intn(5)+1))
				case 1:
					_ = s.Move(loc, -int64(r.Intn(5)+1))
				case 2:
					_ = count.Open(s, adjust.ID(fmt.Sprintf("ct%d_%d", seed, k)), []adjust.ID{loc})
				case 3:
					v, _ := s.View(loc)
					c := v.Book + int64(r.Intn(7)-3)
					if c < 0 {
						c = 0
					}
					_ = count.Submit(s, v.Task, loc, c, u)
				case 4:
					_ = authz.Approve(s, s.TaskOf(loc), loc, u)
				case 5:
					_ = authz.Reject(s, s.TaskOf(loc), loc, u)
				}
			}
		}(int64(w + 1))
	}
	wg.Wait()

	for i := 0; i < 6; i++ {
		loc := adjust.ID(fmt.Sprintf("cL%d", i))
		v, err := s.View(loc)
		mustOK(t, "view", err)
		if v.Book < 0 {
			t.Fatalf("book negative: %d", v.Book)
		}
		if v.Book != 1000+v.Mv+v.Adj {
			t.Fatalf("invariant broken loc %s: %d != 1000+%d+%d", loc, v.Book, v.Mv, v.Adj)
		}
	}
}

func errLabel(e error) string {
	if e == nil {
		return "nil"
	}
	return e.Error()
}

func phaseName(p adjust.Phase) string {
	switch p {
	case adjust.PhaseIdle:
		return "Idle"
	case adjust.PhaseFirst:
		return "First"
	case adjust.PhaseSecond:
		return "Second"
	case adjust.PhaseThird:
		return "Third"
	case adjust.PhasePending:
		return "Pending"
	case adjust.PhaseDone:
		return "Done"
	}
	return "?"
}

// TestRandomAgainstNaive 用 1500 组随机操作序列对照"保存全部流水逐条求和"的朴素模型，
// 逐步核对错误、book、mv、调整量、阶段、待批差值；-v 日志含输入、输出与判定依据。
func TestRandomAgainstNaive(t *testing.T) {
	const sequences = 1500
	const ops = 120
	var rng = rand.New(rand.NewSource(20261004))

	for seq := 0; seq < sequences; seq++ {
		tabs := int64(rng.Intn(6))
		tpct := int64(rng.Intn(11))
		lim := int64(rng.Intn(600))
		sys, err := adjust.New(tabs, tpct, lim)
		mustOK(t, "new", err)
		model := newNaive(tabs, tpct, lim)

		var log strings.Builder
		fmt.Fprintf(&log, "seq %d tabs=%d tpct=%d lim=%d\n", seq, tabs, tpct, lim)

		nLocs := 3 + rng.Intn(4)
		var locs []adjust.ID
		for i := 0; i < nLocs; i++ {
			loc := adjust.ID(fmt.Sprintf("L%d", i))
			book := int64(rng.Intn(400))
			price := int64(rng.Intn(20))
			mustOK(t, "sys add", sys.AddLoc(loc, book, price))
			mustOK(t, "naive add", model.addLoc(loc, book, price))
			fmt.Fprintf(&log, "setup AddLoc(%s book=%d price=%d)\n", loc, book, price)
			locs = append(locs, loc)
		}
		users := []adjust.ID{"u1", "u2", "u3", "u4", "u5", "u6"}
		for _, u := range users {
			a := rng.Intn(2) == 1
			sr := a && rng.Intn(2) == 1
			mustOK(t, "grant", authz.Grant(sys, u, a, sr))
			model.grant(u, a, sr)
		}

		openCount := 0
		assertState := func(stage string) {
			t.Helper()
			for _, loc := range locs {
				v, verr := sys.View(loc)
				if verr != nil {
					t.Fatalf("seq %d %s view %s: %v", seq, stage, loc, verr)
				}
				nl := model.locs[loc]
				nb := nl.book()
				if v.Book != nb || v.Mv != nl.mv() || v.Phase != nl.phase ||
					v.Pending != nl.pending || v.Task != nl.task ||
					v.C1 != nl.c1 || v.C2 != nl.c2 ||
					v.P1 != nl.p1 || v.P2 != nl.p2 || v.P3 != nl.p3 ||
					v.M1 != nl.m1 || v.M2 != nl.m2 {
					t.Fatalf("seq %d %s loc %s diverge sys={b=%d mv=%d ph=%s pend=%d task=%q c1=%d c2=%d p1=%q p2=%q p3=%q m1=%d m2=%d} naive={b=%d mv=%d ph=%s pend=%d task=%q c1=%d c2=%d p1=%q p2=%q p3=%q m1=%d m2=%d}\n%s",
						seq, stage, loc,
						v.Book, v.Mv, phaseName(v.Phase), v.Pending, v.Task, v.C1, v.C2, v.P1, v.P2, v.P3, v.M1, v.M2,
						nb, nl.mv(), phaseName(nl.phase), nl.pending, nl.task, nl.c1, nl.c2, nl.p1, nl.p2, nl.p3, nl.m1, nl.m2,
						log.String())
				}
				if v.Book < 0 {
					t.Fatalf("seq %d %s book negative: %d", seq, stage, v.Book)
				}
				if v.Book != nl.init+v.Mv+v.Adj {
					t.Fatalf("seq %d invariant broken loc %s: book=%d init=%d mv=%d adj=%d",
						seq, loc, v.Book, nl.init, v.Mv, v.Adj)
				}
			}
			for tk, nt := range model.tasks {
				exists, closed := sys.TaskClosed(tk)
				if !exists || closed != nt.closed {
					t.Fatalf("seq %d task %s diverge sys(exists=%v closed=%v) naive(closed=%v)",
						seq, tk, exists, closed, nt.closed)
				}
			}
		}

		for i := 0; i < ops; i++ {
			roll := rng.Intn(100)
			var desc, basis string
			var e1, e2 error
			switch {
			case roll < 35: // Move
				loc := locs[rng.Intn(len(locs))]
				d := int64(rng.Intn(60) + 1)
				if rng.Intn(2) == 0 {
					d = -d
				}
				desc = fmt.Sprintf("Move(%s,%d)", loc, d)
				e1 = sys.Move(loc, d)
				e2 = model.move(loc, d)
				basis = "net move accumulates mv iff accepted"
			case roll < 50: // Open
				var free []adjust.ID
				for _, loc := range locs {
					if model.locs[loc].task == "" {
						free = append(free, loc)
					}
				}
				if len(free) == 0 {
					i--
					continue
				}
				k := 1 + rng.Intn(len(free))
				if k > 3 {
					k = 3
				}
				rng.Shuffle(len(free), func(a, b int) { free[a], free[b] = free[b], free[a] })
				pick := free[:k]
				openCount++
				tk := adjust.ID(fmt.Sprintf("t%d", openCount))
				desc = fmt.Sprintf("Open(%s,%v)", tk, pick)
				e1 = count.Open(sys, tk, pick)
				e2 = model.open(tk, pick)
				basis = "all locs free -> First; else atomic reject"
			case roll < 75: // Submit
				var cand []adjust.ID
				for _, loc := range locs {
					ph := model.locs[loc].phase
					if ph == adjust.PhaseFirst || ph == adjust.PhaseSecond || ph == adjust.PhaseThird {
						cand = append(cand, loc)
					}
				}
				if len(cand) == 0 {
					i--
					continue
				}
				loc := cand[rng.Intn(len(cand))]
				nl := model.locs[loc]
				noise := int64(rng.Intn(41)) - 20 // -20..20
				cnt := nl.book() + noise
				if cnt < 0 {
					cnt = 0
				}
				if cnt > 1e9 {
					cnt = 1e9
				}
				p := users[rng.Intn(len(users))]
				tk := nl.task
				b0 := nl.book()
				diff := cnt - b0
				basis = fmt.Sprintf("ph=%s diff=%d tol=%d mv=%d m1=%d c1=%d",
					phaseName(nl.phase), diff, model.tolAt(b0), nl.mv(), nl.m1, nl.c1)
				desc = fmt.Sprintf("Submit(%s,%s,%d,%s)", tk, loc, cnt, p)
				before := nl.phase
				e1 = count.Submit(sys, tk, loc, cnt, p)
				e2 = model.submit(tk, loc, cnt, p)
				if e2 == nil && nl.phase != before {
					basis += " -> " + phaseName(nl.phase)
				}
			case roll < 87: // Approve
				var pend []adjust.ID
				for _, loc := range locs {
					if model.locs[loc].phase == adjust.PhasePending {
						pend = append(pend, loc)
					}
				}
				if len(pend) == 0 {
					i--
					continue
				}
				loc := pend[rng.Intn(len(pend))]
				nl := model.locs[loc]
				p := users[rng.Intn(len(users))]
				desc = fmt.Sprintf("Approve(%s,%s,%s)", nl.task, loc, p)
				basis = fmt.Sprintf("pending=%d amount=%d lim=%d book=%d",
					nl.pending, abs64(nl.pending)*nl.price, model.lim, nl.book())
				e1 = authz.Approve(sys, nl.task, loc, p)
				e2 = model.approve(nl.task, loc, p)
			case roll < 95: // Reject
				var pend []adjust.ID
				for _, loc := range locs {
					if model.locs[loc].phase == adjust.PhasePending {
						pend = append(pend, loc)
					}
				}
				if len(pend) == 0 {
					i--
					continue
				}
				loc := pend[rng.Intn(len(pend))]
				nl := model.locs[loc]
				p := users[rng.Intn(len(users))]
				desc = fmt.Sprintf("Reject(%s,%s,%s)", nl.task, loc, p)
				basis = "reject needs same checks, no book change"
				e1 = authz.Reject(sys, nl.task, loc, p)
				e2 = model.reject(nl.task, loc, p)
			default: // Close
				var open []adjust.ID
				for tk, nt := range model.tasks {
					if !nt.closed {
						open = append(open, tk)
					}
				}
				if len(open) == 0 {
					i--
					continue
				}
				tk := open[rng.Intn(len(open))]
				desc = fmt.Sprintf("Close(%s)", tk)
				basis = "requires every loc Done"
				e1 = count.Close(sys, tk)
				e2 = model.close(tk)
			}

			if errLabel(e1) != errLabel(e2) {
				t.Fatalf("seq %d op %d %s: sys err=%v naive err=%v\n%s",
					seq, i, desc, e1, e2, log.String())
			}
			fmt.Fprintf(&log, "op %3d %-44s -> %-28s [%s]\n", i, desc, errLabel(e1), basis)
			assertState("after " + desc)
		}
		t.Logf("%s", log.String())
	}
}

// TestScannedConstant 证明 Second 一致性判定读取流水 0 条，
// Submit 触碰记录数为常数，与期间 Move 笔数（10 与 10000）无关。
func TestScannedConstant(t *testing.T) {
	measure := func(t *testing.T, moves int) adjust.Counters {
		t.Helper()
		s := newSysT(t, 2, 5, adjust.MaxLim)
		mustOK(t, "add", s.AddLoc("L", 500_000_000, 10))
		mustOK(t, "open", count.Open(s, "T", []adjust.ID{"L"}))
		mustOK(t, "first", count.Submit(s, "T", "L", 400_000_000, "u1")) // 超容差 -> Second
		before, _ := s.Counters("L")
		for i := 0; i < moves; i++ {
			mustOK(t, "move+", s.Move("L", 1))
			mustOK(t, "move-", s.Move("L", -1)) // 净移动为 0，流水 2*moves 条
		}
		// 复盘数与初盘相同：counted-c1=0 == mv-m1=0，一致，Pending。
		mustOK(t, "second", count.Submit(s, "T", "L", 400_000_000, "u2"))
		after, _ := s.Counters("L")
		return adjust.Counters{Scanned: after.Scanned - before.Scanned, Touched: after.Touched - before.Touched}
	}

	c10 := measure(t, 10)
	c10k := measure(t, 10000)
	if c10.Scanned != 0 || c10k.Scanned != 0 {
		t.Errorf("consistency check must scan 0 move records: 10=%d 10000=%d", c10.Scanned, c10k.Scanned)
	}
	if c10.Touched != c10k.Touched {
		t.Errorf("Submit touches must be constant: 10=%d 10000=%d", c10.Touched, c10k.Touched)
	}
	t.Logf("moves=10 -> %+v; moves=10000 -> %+v", c10, c10k)
}

func isErr(want, got error) bool {
	if want == nil {
		return got == nil
	}
	return errors.Is(got, want)
}

func mustOK(t *testing.T, ctx string, err error) {
	t.Helper()
	if err != nil {
		t.Fatalf("%s: unexpected error %v", ctx, err)
	}
}

// TestTableDriven 表驱动覆盖题面全部边界与两例。
func TestTableDriven(t *testing.T) {
	type step struct {
		name string
		fn   func(s *adjust.System) error
		want error
	}
	type tc struct {
		name  string
		tabs  int64
		tpct  int64
		lim   int64
		setup func(t *testing.T, s *adjust.System)
		steps []step
		check func(t *testing.T, s *adjust.System)
	}

	move := func(loc adjust.ID, d int64) func(*adjust.System) error {
		return func(s *adjust.System) error { return s.Move(loc, d) }
	}
	open := func(task adjust.ID, locs ...adjust.ID) func(*adjust.System) error {
		return func(s *adjust.System) error { return count.Open(s, task, locs) }
	}
	sub := func(task, loc adjust.ID, c int64, p adjust.ID) func(*adjust.System) error {
		return func(s *adjust.System) error { return count.Submit(s, task, loc, c, p) }
	}
	appr := func(task, loc, p adjust.ID) func(*adjust.System) error {
		return func(s *adjust.System) error { return authz.Approve(s, task, loc, p) }
	}
	rej := func(task, loc, p adjust.ID) func(*adjust.System) error {
		return func(s *adjust.System) error { return authz.Reject(s, task, loc, p) }
	}
	closeT := func(task adjust.ID) func(*adjust.System) error {
		return func(s *adjust.System) error { return count.Close(s, task) }
	}
	bookEq := func(loc adjust.ID, want int64) func(*testing.T, *adjust.System) {
		return func(t *testing.T, s *adjust.System) {
			v, err := s.View(loc)
			mustOK(t, "View", err)
			if v.Book != want {
				t.Errorf("book(%s)=%d want %d", loc, v.Book, want)
			}
		}
	}
	phaseEq := func(loc adjust.ID, want adjust.Phase) func(*testing.T, *adjust.System) {
		return func(t *testing.T, s *adjust.System) {
			v, err := s.View(loc)
			mustOK(t, "View", err)
			if v.Phase != want {
				t.Errorf("phase(%s)=%d want %d", loc, v.Phase, want)
			}
		}
	}

	cases := []tc{
		{
			name: "example1-X-97-within", tabs: 2, tpct: 5, lim: 500,
			setup: func(t *testing.T, s *adjust.System) { mustOK(t, "add", s.AddLoc("X", 100, 10)) },
			steps: []step{
				{"open", open("T1", "X"), nil},
				{"submit97 diff=-3 tol=5 adopt", sub("T1", "X", 97, "u1"), nil},
			},
			check: func(t *testing.T, s *adjust.System) {
				bookEq("X", 97)(t, s)
				phaseEq("X", adjust.PhaseDone)(t, s)
			},
		},
		{
			name: "diff-equal-tol-adopt", tabs: 2, tpct: 5, lim: 500,
			setup: func(t *testing.T, s *adjust.System) { mustOK(t, "add", s.AddLoc("X", 100, 10)) },
			steps: []step{
				{"open", open("T1", "X"), nil},
				{"submit95 diff=-5 ==tol adopt", sub("T1", "X", 95, "u1"), nil},
			},
			check: bookEq("X", 95),
		},
		{
			name: "X-94-to-second", tabs: 2, tpct: 5, lim: 500,
			setup: func(t *testing.T, s *adjust.System) { mustOK(t, "add", s.AddLoc("X", 100, 10)) },
			steps: []step{
				{"open", open("T1", "X"), nil},
				{"submit94 exceeds tol5", sub("T1", "X", 94, "u1"), nil},
			},
			check: phaseEq("X", adjust.PhaseSecond),
		},
		{
			name: "example2-Y-consistency-approve-diff-not-counted", tabs: 2, tpct: 5, lim: 500,
			setup: func(t *testing.T, s *adjust.System) {
				mustOK(t, "add", s.AddLoc("Y", 200, 10))
				mustOK(t, "grant", authz.Grant(s, "u3", true, false))
			},
			steps: []step{
				{"open", open("T2", "Y"), nil},
				{"u1 180 first exceed tol10", sub("T2", "Y", 180, "u1"), nil},
				{"move -30 book170", move("Y", -30), nil},
				{"u1 repeat must-switch", sub("T2", "Y", 150, "u1"), adjust.ErrMustSwitch},
				{"u2 150 consistent(-30==-30) pending-20", sub("T2", "Y", 150, "u2"), nil},
				{"move +5 book175", move("Y", 5), nil},
				{"u3 approve amount200<=500", appr("T2", "Y", "u3"), nil},
			},
			check: func(t *testing.T, s *adjust.System) {
				bookEq("Y", 155)(t, s) // 175-20，而不是实盘 150
				phaseEq("Y", adjust.PhaseDone)(t, s)
			},
		},
		{
			name: "second-inconsistent-third-direct-counters-cannot-approve", tabs: 2, tpct: 5, lim: 500,
			setup: func(t *testing.T, s *adjust.System) {
				mustOK(t, "add", s.AddLoc("Y", 200, 10))
				mustOK(t, "g1", authz.Grant(s, "u1", true, false))
				mustOK(t, "g2", authz.Grant(s, "u2", true, false))
				mustOK(t, "g3", authz.Grant(s, "u3", true, false))
			},
			steps: []step{
				{"open", open("T3", "Y"), nil},
				{"u1 180", sub("T3", "Y", 180, "u1"), nil},
				{"move -30", move("Y", -30), nil},
				{"u2 152 inconsistent -28!=-30", sub("T3", "Y", 152, "u2"), nil},
				{"u3 151 diff=-19 exceed pending", sub("T3", "Y", 151, "u3"), nil},
				{"u1 cannot approve", appr("T3", "Y", "u1"), adjust.ErrMustSwitch},
				{"u2 cannot approve", appr("T3", "Y", "u2"), adjust.ErrMustSwitch},
				{"u3 cannot approve", appr("T3", "Y", "u3"), adjust.ErrMustSwitch},
			},
			check: phaseEq("Y", adjust.PhasePending),
		},
		{
			name: "second-within-tol-follows-current-book", tabs: 2, tpct: 5, lim: 500,
			setup: func(t *testing.T, s *adjust.System) { mustOK(t, "add", s.AddLoc("Y", 200, 10)) },
			steps: []step{
				{"open", open("T4", "Y"), nil},
				{"u1 180 exceed tol10", sub("T4", "Y", 180, "u1"), nil},
				{"move -30 ->170", move("Y", -30), nil},
				{"u2 165 diff=-5 within tol8 adopt", sub("T4", "Y", 165, "u2"), nil},
			},
			check: bookEq("Y", 165),
		},
		{
			name: "amount-equal-lim-no-senior-needed", tabs: 0, tpct: 0, lim: 500,
			setup: func(t *testing.T, s *adjust.System) {
				mustOK(t, "add", s.AddLoc("Z", 100, 10))
				mustOK(t, "grant", authz.Grant(s, "ap", true, false))
			},
			steps: []step{
				{"open", open("T5", "Z"), nil},
				{"first 50 diff-50", sub("T5", "Z", 50, "c1"), nil},
				{"second 50 pending", sub("T5", "Z", 50, "c2"), nil},
				{"approve 500==lim", appr("T5", "Z", "ap"), nil},
			},
			check: bookEq("Z", 50),
		},
		{
			name: "amount-510-needs-senior", tabs: 0, tpct: 0, lim: 500,
			setup: func(t *testing.T, s *adjust.System) {
				mustOK(t, "add", s.AddLoc("Z", 100, 10))
				mustOK(t, "gj", authz.Grant(s, "ap", true, false))
				mustOK(t, "gs", authz.Grant(s, "sn", true, true))
			},
			steps: []step{
				{"open", open("T6", "Z"), nil},
				{"first 49", sub("T6", "Z", 49, "c1"), nil},
				{"second 49 pending -51", sub("T6", "Z", 49, "c2"), nil},
				{"junior blocked", appr("T6", "Z", "ap"), adjust.ErrNoSenior},
				{"senior approves", appr("T6", "Z", "sn"), nil},
			},
			check: bookEq("Z", 49),
		},
		{
			name: "approve-understock-keeps-pending-then-retry", tabs: 0, tpct: 0, lim: adjust.MaxLim,
			setup: func(t *testing.T, s *adjust.System) {
				mustOK(t, "add", s.AddLoc("W", 100, 10))
				mustOK(t, "grant", authz.Grant(s, "ap", true, false))
			},
			steps: []step{
				{"open", open("T7", "W"), nil},
				{"first 80", sub("T7", "W", 80, "c1"), nil},
				{"second 80 pending -20", sub("T7", "W", 80, "c2"), nil},
				{"move -85 book15", move("W", -85), nil},
				{"approve would go -5", appr("T7", "W", "ap"), adjust.ErrUnderstock},
				{"move +20 book35", move("W", 20), nil},
				{"retry approve book15", appr("T7", "W", "ap"), nil},
			},
			check: func(t *testing.T, s *adjust.System) {
				bookEq("W", 15)(t, s)
				phaseEq("W", adjust.PhaseDone)(t, s)
			},
		},
		{
			name: "reject-leaves-book-unchanged", tabs: 0, tpct: 0, lim: adjust.MaxLim,
			setup: func(t *testing.T, s *adjust.System) {
				mustOK(t, "add", s.AddLoc("R", 100, 10))
				mustOK(t, "grant", authz.Grant(s, "ap", true, false))
			},
			steps: []step{
				{"open", open("T8", "R"), nil},
				{"first 80", sub("T8", "R", 80, "c1"), nil},
				{"second 80 pending", sub("T8", "R", 80, "c2"), nil},
				{"reject", rej("T8", "R", "ap"), nil},
				{"close", closeT("T8"), nil},
			},
			check: bookEq("R", 100),
		},
		{
			name: "close-then-recount-same-loc", tabs: 2, tpct: 5, lim: 500,
			setup: func(t *testing.T, s *adjust.System) { mustOK(t, "add", s.AddLoc("Q", 100, 10)) },
			steps: []step{
				{"open T9", open("T9", "Q"), nil},
				{"submit 100 done", sub("T9", "Q", 100, "u1"), nil},
				{"close T9", closeT("T9"), nil},
				{"reopen T10", open("T10", "Q"), nil},
				{"submit 97 done", sub("T10", "Q", 97, "u2"), nil},
				{"close T10", closeT("T10"), nil},
			},
			check: bookEq("Q", 97),
		},
	}

	for _, c := range cases {
		c := c
		t.Run(c.name, func(t *testing.T) {
			s := newSysT(t, c.tabs, c.tpct, c.lim)
			if c.setup != nil {
				c.setup(t, s)
			}
			for _, st := range c.steps {
				if st.fn == nil {
					continue
				}
				got := st.fn(s)
				if !isErr(st.want, got) {
					t.Fatalf("step %q: want %v, got %v", st.name, st.want, got)
				}
			}
			if c.check != nil {
				c.check(t, s)
			}
		})
	}
}
