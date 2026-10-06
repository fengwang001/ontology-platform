package duty

import (
	"bufio"
	"fmt"
	"math/rand"
	"os"
	"testing"
)

// fuzzConfig 给出规模很小但规则完整的配置，使朴素逐窗枚举保持快速。
func fuzzConfig(r *rand.Rand) Config {
	return Config{
		DayLen: 200, DayBoundary1: 60, DayBoundary2: 140,
		BaseLimit: [3]int{
			40 + r.Intn(40), 30 + r.Intn(40), 50 + r.Intn(40),
		},
		PerLegCut:    5 + r.Intn(10),
		MinLimit:     15 + r.Intn(20),
		MinRest:      20 + r.Intn(60),
		Window7:      120 + r.Intn(120),
		Window28:     300 + r.Intn(200),
		Limit7:       120 + r.Intn(200),
		Limit28:      300 + r.Intn(400),
		MaxExtension: 10 + r.Intn(40),
	}
}

func sameResult(a, b Result) bool {
	if a.Reject != b.Reject {
		return false
	}
	if a.Reject == RejectRolling7 || a.Reject == RejectRolling28 {
		return a.WindowStart == b.WindowStart
	}
	return true
}

func snapshotsEq(a, b []Duty) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		x, y := a[i], b[i]
		if x != y {
			return false
		}
	}
	return true
}

// naiveEarliest 用逐分钟扫描独立计算最早可报到时刻（朴素基准）。
func naiveEarliest(n *naiveSystem, pid, at, legs, ac int, horizon int) (int, bool) {
	p, ok := n.people[pid]
	if !ok {
		return 0, false
	}
	for s := at; s <= horizon; s++ {
		en := s + n.cfg.SingleLimit(s, legs)
		cand := &Duty{Start: s, End: en, Legs: legs, Aircraft: ac}
		if n.check(p, cand, false, 0).OK() {
			return s, true
		}
	}
	return 0, false
}

// TestDifferentialRandom 与朴素模型比对大量随机操作序列；逐步打印输入、输出、依据。
func TestDifferentialRandom(t *testing.T) {
	logFile, err := os.Create("fuzz_trace.log")
	if err != nil {
		t.Fatal(err)
	}
	defer logFile.Close()
	w := bufio.NewWriter(logFile)
	defer w.Flush()
	logf := func(format string, args ...interface{}) {
		fmt.Fprintf(w, format+"\n", args...)
	}

	const people = 3
	for iter := 0; iter < 300; iter++ {
		r := rand.New(rand.NewSource(int64(1000 + iter)))
		cfg := fuzzConfig(r)
		logf("=== iter %d cfg=%+v", iter, cfg)
		s := NewSystem(cfg)
		n := newNaive(cfg)
		now := 0

		for pid := 0; pid < people; pid++ {
			s.AddPerson(0, pid)
			n.addPerson(0, pid)
			for _, ac := range []int{0, 1} {
				ex := 500 + r.Intn(8000)
				s.UpdateQualification(0, pid, ac, ex)
				n.updateQual(0, pid, ac, ex)
			}
		}

		// 每人保留最近登记信息以便延长/撤销。
		type rec struct{ pid, st, id, nid int }
		var recs []rec

		for step := 0; step < 140; step++ {
			pid := r.Intn(people)
			op := r.Intn(10)
			switch {
			case op < 6: // 登记
				now += r.Intn(6)
				st := r.Intn(5000)
				legs := r.Intn(9)
				ac := r.Intn(2)
				en := st + 1 + r.Intn(cfg.BaseLimit[cfg.Period(st)%3]+cfg.MaxExtension+10)
				rs, acc := s.Register(now, pid, st, en, legs, ac)
				rn, nid := n.register(now, pid, st, en, legs, ac)
				logf("step %d REG now=%d pid=%d [%d,%d) legs=%d ac=%d -> %s | naive %s",
					step, now, pid, st, en, legs, ac, rs, rn)
				if !sameResult(rs, rn) {
					t.Fatalf("iter %d step %d REG mismatch: %+v vs %+v", iter, step, rs, rn)
				}
				if rs.OK() {
					if acc.DutyID != nid {
						t.Fatalf("iter %d step %d id mismatch %d vs %d", iter, step, acc.DutyID, nid)
					}
					recs = append(recs, rec{pid, st, acc.DutyID, nid})
				}
			case op == 6 && len(recs) > 0: // 延长
				k := r.Intn(len(recs))
				rc := recs[k]
				ds := s.Duties(rc.pid)
				var end int
				for _, d := range ds {
					if d.ID == rc.id {
						end = d.End
					}
				}
				nd := 1 + r.Intn(cfg.MaxExtension+20)
				rs := s.Extend(now, rc.pid, rc.id, end+nd)
				rn := n.extend(now, rc.pid, rc.nid, end+nd)
				logf("step %d EXT now=%d pid=%d id=%d newEnd=%d -> %s | naive %s",
					step, now, rc.pid, rc.id, end+nd, rs, rn)
				if !sameResult(rs, rn) {
					t.Fatalf("iter %d step %d EXT mismatch: %+v vs %+v", iter, step, rs, rn)
				}
			case op == 7 && len(recs) > 0: // 撤销
				k := r.Intn(len(recs))
				rc := recs[k]
				recs = append(recs[:k], recs[k+1:]...)
				rs := s.Cancel(now, rc.pid, rc.id)
				rn := n.cancel(now, rc.pid, rc.nid)
				logf("step %d CAN now=%d pid=%d id=%d -> %s | naive %s",
					step, now, rc.pid, rc.id, rs, rn)
				if !sameResult(rs, rn) {
					t.Fatalf("iter %d step %d CAN mismatch: %+v vs %+v", iter, step, rs, rn)
				}
			case op == 8: // 资质更新/吊销
				ac := r.Intn(2)
				if r.Intn(4) == 0 {
					rs := s.RevokeQualification(now, pid, ac)
					rn := n.revokeQual(now, pid, ac)
					logf("step %d REVOKE now=%d pid=%d ac=%d -> %s | naive %s",
						step, now, pid, ac, rs, rn)
					if !sameResult(rs, rn) {
						t.Fatalf("iter %d REVOKE mismatch", iter)
					}
				} else {
					ex := r.Intn(9000)
					rs := s.UpdateQualification(now, pid, ac, ex)
					rn := n.updateQual(now, pid, ac, ex)
					logf("step %d QUAL now=%d pid=%d ac=%d ex=%d -> %s | naive %s",
						step, now, pid, ac, ex, rs, rn)
					if !sameResult(rs, rn) {
						t.Fatalf("iter %d QUAL mismatch", iter)
					}
				}
			default: // 查询最早可报到时刻
				at := r.Intn(3000)
				legs := r.Intn(9)
				ac := r.Intn(2)
				got, ok1 := s.EarliestReport(pid, at, legs, ac)
				want, ok2 := naiveEarliest(n, pid, at, legs, ac, 8000)
				logf("step %d QUERY pid=%d at=%d legs=%d ac=%d -> %d(%v) | naive %d(%v)",
					step, pid, at, legs, ac, got, ok1, want, ok2)
				if ok1 != ok2 || got != want {
					t.Fatalf("iter %d step %d EarliestReport mismatch: got %d(%v) want %d(%v)",
						iter, step, got, ok1, want, ok2)
				}
			}

			// 每步后值勤期快照一致。
			for pid := 0; pid < people; pid++ {
				gd := s.Duties(pid)
				nd := n.people[pid].duties
				if len(gd) != len(nd) {
					t.Fatalf("iter %d step %d pid %d count mismatch", iter, step, pid)
				}
				for i := range gd {
					if gd[i].Start != nd[i].Start || gd[i].End != nd[i].End ||
						gd[i].Legs != nd[i].Legs || gd[i].Aircraft != nd[i].Aircraft ||
						gd[i].Extended != nd[i].Extended {
						t.Fatalf("iter %d step %d duty mismatch: %+v vs %+v",
							iter, step, gd[i], nd[i])
					}
				}
			}
		}
	}
}

// TestRollingMathAgainstBrute 直接对滚动数学做随机差分：随机区间集 + 候选区间，
// 折点法结果必须等于逐整数窗枚举的最小超限位。
func TestRollingMathAgainstBrute(t *testing.T) {
	r := rand.New(rand.NewSource(42))
	for iter := 0; iter < 2000; iter++ {
		win := 50 + r.Intn(200)
		capLimit := 10 + r.Intn(win)
		k := r.Intn(6)
		ds := make([]*Duty, 0, k)
		base := r.Intn(500)
		for i := 0; i < k; i++ {
			a := base + r.Intn(3*win)
			b := a + 1 + r.Intn(win)
			ds = append(ds, &Duty{Start: a, End: b})
		}
		ns := r.Intn(3 * win)
		ne := ns + 1 + r.Intn(win)
		got := rollingViolation(ds, ns, ne, win, capLimit)
		want := -1
		lo := ns - win
		if lo < 0 {
			lo = 0
		}
		for w := lo; w <= ne; w++ {
			total := overlap(ns, ne, w, w+win) + dutyOverlapSum(ds, w, win)
			if total > capLimit {
				want = w
				break
			}
		}
		if got != want {
			t.Fatalf("iter %d mismatch got %d want %d (win=%d cap=%d cand=[%d,%d) ds=%v)",
				iter, got, want, win, capLimit, ns, ne, ds)
		}
	}
}
