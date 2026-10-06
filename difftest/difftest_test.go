// Package difftest 随机对照测试：
// 对同一随机操作序列，分别驱动高性能实现（loto）与独立朴素模型（naive），
// 逐步比较输出（含错误类别），并在日志中打印输入、输出与判定依据。
package difftest

import (
	"fmt"
	"math/rand"
	"testing"

	"ontology/loto"
	"ontology/naive"
)

// api 两个实现共同的操作接口。
type api interface {
	Apply(t int64, applicant string, devices []string, wt loto.WorkType, start, end int64) (int, error)
	Approve(t int64, approver string, permitID int) error
	AddWorker(t int64, actor string, permitID int, worker string) error
	Lock(t int64, permitID int, person, point string) error
	Verify(t int64, permitID int, verifier string) error
	Start(t int64, permitID int, actor string) error
	Enter(t int64, permitID int, person string) error
	Leave(t int64, permitID int, person string) error
	Complete(t int64, permitID int, actor string) error
	Unlock(t int64, permitID int, person, point string) error
	TrialBegin(t int64, permitID int, actor string) error
	TrialEnd(t int64, permitID int, actor string) error
	ForceUnlock(t int64, permitID int, actor, confirmer, person, point, reason string) error
	EnergizableAt(device string, t int64) (loto.Decision, error)
	PermitState(id int) (loto.State, error)
}

func errStr(err error) string {
	if err == nil {
		return "OK"
	}
	if k, ok := loto.KindOf(err); ok {
		return k.String()
	}
	return "非系统错误:" + err.Error()
}

func mustSameKind(t *testing.T, step int, op string, e1, e2 error) {
	t.Helper()
	k1, ok1 := loto.KindOf(e1)
	k2, ok2 := loto.KindOf(e2)
	if (e1 == nil) != (e2 == nil) || (e1 != nil && (ok1 != ok2 || k1 != k2)) {
		t.Fatalf("步骤 %d %s 结果不一致: loto=%v naive=%v", step, op, errStr(e1), errStr(e2))
	}
}

type env struct {
	devices []string
	points  []string
	people  []string
}

func buildEnv(r *rand.Rand) (env, *loto.Config) {
	points := []string{"P1", "P2", "P3", "P4", "P5"}
	cfg := &loto.Config{DevicePoints: map[string][]string{}}
	devices := []string{"D1", "D2", "D3", "D4"}
	for _, d := range devices {
		n := 1 + r.Intn(3)
		perm := r.Perm(len(points))
		pts := []string{}
		for i := 0; i < n; i++ {
			pts = append(pts, points[perm[i]])
		}
		cfg.DevicePoints[d] = pts
	}
	return env{devices: devices, points: points, people: []string{"u0", "u1", "u2", "u3", "u4", "u5"}}, cfg
}

// newPair 创建一对配置相同的系统（高性能实现 + 朴素模型），并登记相同人员。
func newPair(t *testing.T, r *rand.Rand) (api, api, env) {
	t.Helper()
	e, cfg := buildEnv(r)
	ls, err := loto.NewSystem(cfg)
	if err != nil {
		t.Fatalf("NewSystem: %v", err)
	}
	ns := naive.New(cfg)
	roles := []loto.Role{loto.RoleApplicant, loto.RoleApprover, loto.RoleWorker, loto.RoleSupervisor}
	for _, p := range e.people {
		var rs []loto.Role
		for _, role := range roles {
			if r.Intn(2) == 0 {
				rs = append(rs, role)
			}
		}
		if len(rs) == 0 {
			rs = append(rs, roles[r.Intn(len(roles))])
		}
		if err := ls.AddPerson(p, rs...); err != nil {
			t.Fatalf("AddPerson: %v", err)
		}
		ns.AddPerson(p, rs...)
	}
	return ls, ns, e
}

// op 是一条随机生成的操作，可对任一实现执行并记录日志。
type op struct {
	desc string
	run  func(s api) error
}

func genOps(r *rand.Rand, e env, n int) []op {
	var ops []op
	var clock int64
	nextT := func() int64 {
		if r.Intn(100) < 80 {
			clock += int64(r.Intn(4))
		} else if clock > 0 {
			clock = int64(r.Intn(int(clock) + 2)) // 可能回退
		}
		return clock
	}
	person := func() string { return e.people[r.Intn(len(e.people))] }
	device := func() string { return e.devices[r.Intn(len(e.devices))] }
	point := func() string {
		if r.Intn(100) < 5 {
			return "PX" // 不存在的隔离点
		}
		return e.points[r.Intn(len(e.points))]
	}
	permitID := func() int { return 1 + r.Intn(30) }
	for i := 0; i < n; i++ {
		tm := nextT()
		switch r.Intn(13) {
		case 0:
			ap := person()
			var devs []string
			for _, d := range e.devices {
				if r.Intn(2) == 0 {
					devs = append(devs, d)
				}
			}
			if len(devs) == 0 {
				devs = []string{device()}
			}
			wt := loto.WorkType(r.Intn(3))
			start := tm + int64(r.Intn(20))
			end := start + 1 + int64(r.Intn(40))
			ops = append(ops, op{
				desc: fmt.Sprintf("Apply(t=%d, %s, %v, %s, [%d,%d))", tm, ap, devs, wt, start, end),
				run: func(s api) error {
					_, err := s.Apply(tm, ap, devs, wt, start, end)
					return err
				},
			})
		case 1:
			ap, pid := person(), permitID()
			ops = append(ops, op{
				desc: fmt.Sprintf("Approve(t=%d, %s, #%d)", tm, ap, pid),
				run:  func(s api) error { return s.Approve(tm, ap, pid) },
			})
		case 2:
			actor, w, pid := person(), person(), permitID()
			ops = append(ops, op{
				desc: fmt.Sprintf("AddWorker(t=%d, %s, #%d, %s)", tm, actor, pid, w),
				run:  func(s api) error { return s.AddWorker(tm, actor, pid, w) },
			})
		case 3:
			pid, ps, pt := permitID(), person(), point()
			ops = append(ops, op{
				desc: fmt.Sprintf("Lock(t=%d, #%d, %s, %s)", tm, pid, ps, pt),
				run:  func(s api) error { return s.Lock(tm, pid, ps, pt) },
			})
		case 4:
			pid, v := permitID(), person()
			ops = append(ops, op{
				desc: fmt.Sprintf("Verify(t=%d, #%d, %s)", tm, pid, v),
				run:  func(s api) error { return s.Verify(tm, pid, v) },
			})
		case 5:
			pid, actor := permitID(), person()
			ops = append(ops, op{
				desc: fmt.Sprintf("Start(t=%d, #%d, %s)", tm, pid, actor),
				run:  func(s api) error { return s.Start(tm, pid, actor) },
			})
		case 6:
			pid, ps := permitID(), person()
			ops = append(ops, op{
				desc: fmt.Sprintf("Enter(t=%d, #%d, %s)", tm, pid, ps),
				run:  func(s api) error { return s.Enter(tm, pid, ps) },
			})
		case 7:
			pid, ps := permitID(), person()
			ops = append(ops, op{
				desc: fmt.Sprintf("Leave(t=%d, #%d, %s)", tm, pid, ps),
				run:  func(s api) error { return s.Leave(tm, pid, ps) },
			})
		case 8:
			pid, actor := permitID(), person()
			ops = append(ops, op{
				desc: fmt.Sprintf("Complete(t=%d, #%d, %s)", tm, pid, actor),
				run:  func(s api) error { return s.Complete(tm, pid, actor) },
			})
		case 9:
			pid, ps, pt := permitID(), person(), point()
			ops = append(ops, op{
				desc: fmt.Sprintf("Unlock(t=%d, #%d, %s, %s)", tm, pid, ps, pt),
				run:  func(s api) error { return s.Unlock(tm, pid, ps, pt) },
			})
		case 10:
			pid, actor := permitID(), person()
			ops = append(ops, op{
				desc: fmt.Sprintf("TrialBegin(t=%d, #%d, %s)", tm, pid, actor),
				run:  func(s api) error { return s.TrialBegin(tm, pid, actor) },
			})
		case 11:
			pid, actor := permitID(), person()
			ops = append(ops, op{
				desc: fmt.Sprintf("TrialEnd(t=%d, #%d, %s)", tm, pid, actor),
				run:  func(s api) error { return s.TrialEnd(tm, pid, actor) },
			})
		case 12:
			pid := permitID()
			actor, conf, ps, pt := person(), person(), person(), point()
			reason := "强制清理"
			if r.Intn(20) == 0 {
				reason = ""
			}
			ops = append(ops, op{
				desc: fmt.Sprintf("ForceUnlock(t=%d, #%d, %s, %s, %s, %s, %q)", tm, pid, actor, conf, ps, pt, reason),
				run:  func(s api) error { return s.ForceUnlock(tm, pid, actor, conf, ps, pt, reason) },
			})
		}
	}
	return ops
}

// TestDifferential 对大量随机操作序列逐步对照两个实现。
func TestDifferential(t *testing.T) {
	const sequences = 30
	const opsPerSeq = 400
	for seed := int64(0); seed < sequences; seed++ {
		r := rand.New(rand.NewSource(seed))
		l, n, e := newPair(t, r)
		ops := genOps(r, e, opsPerSeq)
		for i, o := range ops {
			e1 := o.run(l)
			e2 := o.run(n)
			logLine := fmt.Sprintf("seed=%d step=%d 输入=%s 输出(loto)=%s 输出(naive)=%s",
				seed, i, o.desc, errStr(e1), errStr(e2))
			mustSameKind(t, i, o.desc, e1, e2)
			// 判定依据：逐步对照所有票状态与所有设备送电判定
			for pid := 1; pid <= 30; pid++ {
				s1, err1 := l.PermitState(pid)
				s2, err2 := n.PermitState(pid)
				mustSameKind(t, i, o.desc+" PermitState", err1, err2)
				if err1 == nil && s1 != s2 {
					t.Fatalf("%s\n票 #%d 状态不一致: loto=%v naive=%v", logLine, pid, s1, s2)
				}
			}
			var basis string
			for _, d := range e.devices {
				d1, err1 := l.EnergizableAt(d, 0) // t 小于时钟时按当前时钟判定
				d2, err2 := n.EnergizableAt(d, 0)
				mustSameKind(t, i, o.desc+" Energizable", err1, err2)
				if err1 == nil && (d1.OK != d2.OK || d1.Locks != d2.Locks || !sameInts(d1.Blocking, d2.Blocking)) {
					t.Fatalf("%s\n设备 %s 送电判定不一致: loto=%+v naive=%+v", logLine, d, d1, d2)
				}
				if err1 == nil && !d1.OK {
					basis += fmt.Sprintf(" 送电依据[%s]=%s", d, d1.Reason)
				}
			}
			t.Logf("%s 判定=一致%s", logLine, basis)
		}
	}
}

func sameInts(a, b []int) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
