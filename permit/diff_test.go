package permit

import (
	"fmt"
	"math/rand"
	"strings"
	"testing"
)

type stepLogger struct{ b strings.Builder }

func (l *stepLogger) Logf(format string, args ...any) {
	l.b.WriteString(fmt.Sprintf(format, args...) + "\n")
}

// randDAG 生成一个至多 n 个环节的随机许可 DAG（边只从小编号指向大编号，保证无环）。
func randDAG(r *rand.Rand, n int, nonWorking []int) *PermitType {
	t := &PermitType{ID: "P"}
	for i := 0; i < n; i++ {
		st := StageDef{
			ID:          fmt.Sprintf("S%d", i),
			Department:  fmt.Sprintf("D%d", i),
			DueWorkdays: 1 + r.Intn(5),
			AutoPass:    r.Intn(2) == 0,
		}
		for j := 0; j < i; j++ {
			if r.Intn(3) == 0 {
				st.Prereqs = append(st.Prereqs, fmt.Sprintf("S%d", j))
			}
		}
		t.Stages = append(t.Stages, st)
	}
	return t
}

func TestRandomDifferential(t *testing.T) {
	const iterations = 5
	const maxOps = 120
	const horizon = 40
	var aggregate strings.Builder

	for iter := 0; iter < iterations; iter++ {
		r := rand.New(rand.NewSource(int64(1000 + iter)))
		nonWorking := randomNonWorking(r, horizon)
		tp := randDAG(r, 1+r.Intn(4), nonWorking)
		suppWD := 1 + r.Intn(4)
		suppLimit := 1 + r.Intn(2)

		var log stepLogger
		svc := NewService(nonWorking, WithSupplement(suppWD, suppLimit), WithLogger(&log))
		if err := svc.RegisterType(tp); err != nil {
			t.Fatalf("register: %v", err)
		}
		acceptDay := 1
		if err := svc.Accept(acceptDay, "c", "P"); err != nil {
			t.Fatal(err)
		}
		cal := NewCalendar(nonWorking)
		nm := NewNaiveModel(cal, tp, acceptDay, suppWD, suppLimit)
		n := len(tp.Stages)

		day := acceptDay
		for op := 0; op < maxOps; op++ {
			// 时间以较大概率前进（偶尔停留模拟同日多操作）。
			if r.Intn(5) != 0 {
				day += r.Intn(3)
			}
			if day > horizon {
				day = horizon
			}
			si := r.Intn(n)
			st := tp.Stages[si]
			kind := r.Intn(10)
			switch {
			case kind < 5: // 部门办理
				dec := []Decision{DecideApprove, DecideReject, DecideSupplement}[r.Intn(3)]
				dept := st.Department
				if r.Intn(6) == 0 {
					dept = "WRONG"
				}
				sErr := svc.Decide(day, "c", st.ID, dept, dec)
				nErr := nm.decide(day, st.ID, dept, dec, suppLimit)
				compareErr(t, iter, op, "decide", day, st.ID, dec, dept, sErr, nErr, log.b.String())
			case kind < 7: // 提交补正
				sErr := svc.SubmitSupplement(day, "c", st.ID)
				nErr := nm.submitSupp(day, st.ID)
				compareErr(t, iter, op, "supp", day, st.ID, 0, "", sErr, nErr, log.b.String())
			case kind < 8: // 撤回
				sErr := svc.Withdraw(day, "c")
				nErr := nm.withdraw(day)
				compareErr(t, iter, op, "withdraw", day, "", 0, "", sErr, nErr, log.b.String())
			default:
				// 纯查询推进观察
				nm.advanceTo(day)
			}

			// 在若干历史时刻对全部环节状态与整件结论做一致性比对。
			for _, probe := range []int{day} {
				if probe < acceptDay {
					continue
				}
				nm.advanceTo(probe)
				cv, qErr := svc.Progress(probe, "c")
				if qErr != nil {
					t.Fatalf("progress day%d: %v", probe, qErr)
				}
				for _, st := range tp.Stages {
					sv, _ := svc.StageAt(probe, "c", st.ID)
					nv := nm.View(st.ID)
					if sv.Status != nv.Status {
						dump(t, iter, probe, nonWorking, tp, st.ID, sv, nv, log.b.String())
					}
					if sv.Overtime != nv.Overtime {
						dump(t, iter, probe, nonWorking, tp, st.ID, sv, nv, log.b.String())
					}
				}
				if cv.Outcome != nm.Outcome() {
					t.Fatalf("iter%d day%d outcome svc=%d naive=%d\n%s", iter, probe, cv.Outcome, nm.Outcome(), log.b.String())
				}
				fd, hf := nm.FinalDay()
				if hf && cv.HasFinal && cv.FinalDay != fd {
					t.Fatalf("iter%d day%d finalday svc=%d naive=%d\n%s", iter, probe, cv.FinalDay, fd, log.b.String())
				}
			}
		}
		// 全程无分歧则只保留少量摘要日志，避免输出爆炸；失败时已打印完整日志。
		aggregate.WriteString(fmt.Sprintf("iter=%d stages=%d nonWorking=%v suppWD=%d suppLimit=%d OK\n",
			iter, n, nonWorking, suppWD, suppLimit))
	}
	t.Log("\n" + aggregate.String())
}

func compareErr(t *testing.T, iter, op int, name string, day int, sid string, dec Decision, dept string, sErr, nErr error, log string) {
	t.Helper()
	if code(sErr) != code(nErr) {
		t.Fatalf("iter%d op%d %s day%d stage=%s dec=%d dept=%s svcErr=%v naiveErr=%v\n%s",
			iter, op, name, day, sid, dec, dept, sErr, nErr, log)
	}
}

func dump(t *testing.T, iter, day int, nw []int, tp *PermitType, sid string, sv, nv StageView, log string) {
	t.Fatalf("iter%d day%d stage=%s MISMATCH\n svc=%+v\nnaive=%+v\nnonWorking=%v tp=%+v\n%s",
		iter, day, sid, sv, nv, nw, tp, log)
}

func randomNonWorking(r *rand.Rand, horizon int) []int {
	var nw []int
	for d := 1; d <= horizon; d++ {
		if r.Intn(7) == 0 {
			nw = append(nw, d)
		}
	}
	return nw
}

func TestDbgIter1(t *testing.T) {
	r := rand.New(rand.NewSource(1001))
	nonWorking := randomNonWorking(r, 40)
	tp := randDAG(r, 1+r.Intn(4), nonWorking)
	suppWD := 1 + r.Intn(4)
	suppLimit := 1 + r.Intn(2)
	svc := NewService(nonWorking, WithSupplement(suppWD, suppLimit))
	svc.RegisterType(tp)
	svc.Accept(1, "c", "P")
	cal := NewCalendar(nonWorking)
	nm := NewNaiveModel(cal, tp, 1, suppWD, suppLimit)
	t.Logf("nw=%v suppWD=%d suppLimit=%d", nonWorking, suppWD, suppLimit)
	for _, st := range tp.Stages {
		t.Logf("def %+v", st)
	}
	_ = nm
}

func TestDbgIter3(t *testing.T) {
	r := rand.New(rand.NewSource(1003))
	nonWorking := randomNonWorking(r, 40)
	tp := randDAG(r, 1+r.Intn(4), nonWorking)
	suppWD := 1 + r.Intn(4)
	suppLimit := 1 + r.Intn(2)
	cal := NewCalendar(nonWorking)
	nm := NewNaiveModel(cal, tp, 1, suppWD, suppLimit)
	t.Logf("nw=%v", nonWorking)
	for _, st := range tp.Stages {
		t.Logf("def %+v", st)
	}
	nm.submitSupp(1, tp.Stages[0].ID)
	nm.submitSupp(2, tp.Stages[0].ID)
	nm.decide(4, tp.Stages[0].ID, tp.Stages[0].Department, DecideSupplement, suppLimit)
	nm.decide(6, tp.Stages[0].ID, tp.Stages[0].Department, DecideApprove, suppLimit)
	s1 := nm.stages["S1"]
	t.Logf("S1 start=%d remain=%d", s1.startDay, s1.remaining)
}
