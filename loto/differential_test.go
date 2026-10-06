package loto_test

import (
	"flag"
	"fmt"
	"math/rand"
	"os"
	"sort"
	"strings"
	"testing"

	"ontology/loto"
)

// 差分测试日志：默认同时写 t.Log；-loto-log=path 时额外落盘。
var logPath = flag.String("loto-log", "", "write differential fuzz inputs/outputs/reasons to this file")

type opKind int

const (
	opApply opKind = iota
	opApprove
	opPlaceLock
	opVerify
	opStart
	opEnter
	opLeave
	opComplete
	opRemoveLock
	opBeginTrial
	opEndTrial
	opForceRemove
	opEnergize
)

type fuzzOp struct {
	kind                                  opKind
	id, actor, actor2, point, worker, dev string
	wt                                    string
	devs, workers                         []string
	st, en, at                            int64
	reason                                string
}

func (o fuzzOp) String() string {
	switch o.kind {
	case opApply:
		return fmt.Sprintf("Apply(%s by=%s devs=%v wt=%s [%d,%d) workers=%v)", o.id, o.actor, o.devs, o.wt, o.st, o.en, o.workers)
	case opApprove:
		return fmt.Sprintf("Approve(%s by=%s at=%d)", o.id, o.actor, o.at)
	case opPlaceLock:
		return fmt.Sprintf("PlaceLock(%s w=%s pt=%s at=%d)", o.id, o.worker, o.point, o.at)
	case opVerify:
		return fmt.Sprintf("Verify(%s by=%s at=%d)", o.id, o.actor, o.at)
	case opStart:
		return fmt.Sprintf("StartWork(%s by=%s at=%d)", o.id, o.actor, o.at)
	case opEnter:
		return fmt.Sprintf("Enter(%s w=%s at=%d)", o.id, o.worker, o.at)
	case opLeave:
		return fmt.Sprintf("Leave(%s w=%s at=%d)", o.id, o.worker, o.at)
	case opComplete:
		return fmt.Sprintf("Complete(%s by=%s at=%d)", o.id, o.actor, o.at)
	case opRemoveLock:
		return fmt.Sprintf("RemoveLock(%s w=%s pt=%s at=%d)", o.id, o.worker, o.point, o.at)
	case opBeginTrial:
		return fmt.Sprintf("BeginTrial(%s by=%s at=%d)", o.id, o.actor, o.at)
	case opEndTrial:
		return fmt.Sprintf("EndTrial(%s by=%s at=%d)", o.id, o.actor, o.at)
	case opForceRemove:
		return fmt.Sprintf("ForceRemove(%s pt=%s w=%s sup=%s con=%s reason=%q at=%d)",
			o.id, o.point, o.worker, o.actor, o.actor2, o.reason, o.at)
	case opEnergize:
		return fmt.Sprintf("CanEnergize(%s at=%d)", o.dev, o.at)
	}
	return "?"
}

type fuzzWorld struct {
	people []string
	devs   []string
	points []string
	appBy  map[string]string // permit -> applicant
	devsOf map[string][]string
	wkOf   map[string][]string
	ptsOf  map[string][]string
}

func codeOf(err error) string {
	if err == nil {
		return "OK"
	}
	if oe, ok := err.(*loto.OpError); ok {
		return oe.Code.String()
	}
	return err.Error()
}

func nCodeOf(err error) string {
	if err == nil {
		return "OK"
	}
	if ne, ok := err.(*nError); ok {
		return ne.code
	}
	return err.Error()
}

func realStateOf(s *loto.System) map[string]nState {
	out := map[string]nState{}
	// 通过审计/快照枚举票：用 GetPermit 需要 id 列表；从审计拿。
	ids := map[string]bool{}
	for _, e := range s.AuditLog() {
		if e.Permit != "" {
			ids[e.Permit] = true
		}
	}
	// 也覆盖 Apply 失败但 id 存在于差分生成器的情形（失败票 GetPermit 为 false）。
	for id := range ids {
		snap, ok := s.GetPermit(id)
		if !ok {
			continue
		}
		out[id] = nState{
			phase:   string(snap.Phase),
			overdue: snap.Overdue,
			locks:   append([]string{}, snap.PhysicalLocks...),
			inside:  append([]string{}, snap.Inside...),
			approve: len(snap.Approvers),
		}
	}
	return out
}

func naiveStateOf(n *naive) map[string]nState {
	out := map[string]nState{}
	for id, p := range n.permits {
		var lks []string
		for _, lk := range p.locks {
			if !lk.removed && !lk.trialRemoved {
				lks = append(lks, lk.worker+"@"+lk.point)
			}
		}
		sort.Strings(lks)
		var ins []string
		for w := range p.inside {
			ins = append(ins, w)
		}
		sort.Strings(ins)
		out[id] = nState{phase: p.phase, overdue: p.overdue, locks: lks, inside: ins, approve: len(p.approvers)}
	}
	return out
}

func statesEqual(a, b map[string]nState) (string, bool) {
	if len(a) != len(b) {
		return fmt.Sprintf("permit set differs: %d vs %d", len(a), len(b)), false
	}
	ids := make([]string, 0, len(a))
	for id := range a {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	for _, id := range ids {
		x, y := a[id], b[id]
		if x.phase != y.phase || x.overdue != y.overdue || x.approve != y.approve ||
			!sliceEq(x.locks, y.locks) || !sliceEq(x.inside, y.inside) {
			return fmt.Sprintf("permit %s differs: real=%+v naive=%+v", id, x, y), false
		}
	}
	return "", true
}

func sliceEq(a, b []string) bool {
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

func runReal(s *loto.System, o fuzzOp) (string, bool, string) {
	switch o.kind {
	case opApply:
		return codeOf(s.Apply(o.id, o.actor, o.devs, loto.WorkType(o.wt), o.st, o.en, o.workers)), false, ""
	case opApprove:
		return codeOf(s.Approve(o.id, o.actor, o.at)), false, ""
	case opPlaceLock:
		return codeOf(s.PlaceLock(o.id, o.worker, o.point, o.at)), false, ""
	case opVerify:
		return codeOf(s.Verify(o.id, o.actor, o.at)), false, ""
	case opStart:
		return codeOf(s.StartWork(o.id, o.actor, o.at)), false, ""
	case opEnter:
		return codeOf(s.Enter(o.id, o.worker, o.at)), false, ""
	case opLeave:
		return codeOf(s.Leave(o.id, o.worker, o.at)), false, ""
	case opComplete:
		return codeOf(s.Complete(o.id, o.actor, o.at)), false, ""
	case opRemoveLock:
		return codeOf(s.RemoveLock(o.id, o.worker, o.point, o.at)), false, ""
	case opBeginTrial:
		return codeOf(s.BeginTrial(o.id, o.actor, o.at)), false, ""
	case opEndTrial:
		return codeOf(s.EndTrial(o.id, o.actor, o.at)), false, ""
	case opForceRemove:
		return codeOf(s.ForceRemoveLock(o.id, o.point, o.worker, o.actor, o.actor2, o.reason, o.at)), false, ""
	case opEnergize:
		ok, rep, err := s.CanEnergize(o.dev, o.at)
		reason := ""
		if rep != nil {
			reason = strings.Join(rep.Reasons, " | ")
		}
		return codeOf(err), ok, reason
	}
	return "?", false, ""
}

func runNaive(n *naive, o fuzzOp) (string, bool, string) {
	switch o.kind {
	case opApply:
		return nCodeOf(n.apply(o.id, o.actor, o.devs, o.wt, o.st, o.en, o.workers)), false, ""
	case opApprove:
		return nCodeOf(n.approve(o.id, o.actor, o.at)), false, ""
	case opPlaceLock:
		return nCodeOf(n.placeLock(o.id, o.worker, o.point, o.at)), false, ""
	case opVerify:
		return nCodeOf(n.verify(o.id, o.actor, o.at)), false, ""
	case opStart:
		return nCodeOf(n.startWork(o.id, o.actor, o.at)), false, ""
	case opEnter:
		return nCodeOf(n.enter(o.id, o.worker, o.at)), false, ""
	case opLeave:
		return nCodeOf(n.leave(o.id, o.worker, o.at)), false, ""
	case opComplete:
		return nCodeOf(n.complete(o.id, o.actor, o.at)), false, ""
	case opRemoveLock:
		return nCodeOf(n.removeLock(o.id, o.worker, o.point, o.at)), false, ""
	case opBeginTrial:
		return nCodeOf(n.beginTrial(o.id, o.actor, o.at)), false, ""
	case opEndTrial:
		return nCodeOf(n.endTrial(o.id, o.actor, o.at)), false, ""
	case opForceRemove:
		return nCodeOf(n.forceRemoveLock(o.id, o.point, o.worker, o.actor, o.actor2, o.reason, o.at)), false, ""
	case opEnergize:
		ok, err := n.canEnergize(o.dev, o.at)
		return nCodeOf(err), ok, ""
	}
	return "?", false, ""
}

func pickStr(rng *rand.Rand, xs []string) string {
	if len(xs) == 0 {
		return ""
	}
	return xs[rng.Intn(len(xs))]
}

func fuzzPersons() []string {
	return []string{"appA", "appB", "apvA", "apvB", "wA", "wB", "wC", "supA", "supB", "verifier", "x1", "x2"}
}

func initWorld(s *loto.System, n *naive) {
	type spec struct {
		id     string
		roles  []loto.Role
		nroles []string
	}
	role := func(rs ...string) ([]loto.Role, []string) {
		var lr []loto.Role
		m := map[string]loto.Role{
			"applicant": loto.RoleApplicant, "approver": loto.RoleApprover,
			"worker": loto.RoleWorker, "supervisor": loto.RoleSupervisor,
		}
		for _, r := range rs {
			lr = append(lr, m[r])
		}
		return lr, rs
	}
	mk := func(id string, rs ...string) spec {
		lr, nr := role(rs...)
		return spec{id: id, roles: lr, nroles: nr}
	}
	specs := []spec{
		mk("appA", "applicant"), mk("appB", "applicant", "worker"),
		mk("apvA", "approver"), mk("apvB", "approver"),
		mk("wA", "worker"), mk("wB", "worker"), mk("wC", "worker"),
		mk("supA", "supervisor"), mk("supB", "supervisor"),
		mk("x1", "applicant", "approver", "worker", "supervisor"),
		mk("x2", "approver", "worker"),
		{id: "verifier"},
	}
	for _, sp := range specs {
		_ = s.RegisterPerson(sp.id, sp.roles...)
		_ = n.registerPerson(sp.id, sp.nroles...)
	}
	devPts := map[string][]string{
		"devA": {"p1", "p2"},
		"devB": {"p2", "p3"},
		"devC": {"p3"},
		"devD": {"p1", "p4"},
	}
	ids := make([]string, 0, len(devPts))
	for d := range devPts {
		ids = append(ids, d)
	}
	sort.Strings(ids)
	for _, d := range ids {
		_ = s.RegisterDevice(d, devPts[d])
		_ = n.registerDevice(d, devPts[d])
	}
}

type genCtx struct {
	rng     *rand.Rand
	clock   int64
	permits []string
	pApp    map[string]string
	pWork   map[string][]string
	pPts    map[string][]string
	pDev    map[string][]string
}

func (g *genCtx) adv() int64 {
	// 多数推进、少数回退、偶尔重复时刻。
	switch g.rng.Intn(10) {
	case 0:
		return g.clock // 同一时刻的连续操作（合法）
	case 1:
		if g.clock > 0 {
			return g.clock - 1 // 回退（应被两边一致拒绝）
		}
	}
	g.clock += int64(g.rng.Intn(4))
	return g.clock
}

func allPoints(devs []string) []string {
	mp := map[string]bool{}
	dp := map[string][]string{
		"devA": {"p1", "p2"}, "devB": {"p2", "p3"}, "devC": {"p3"}, "devD": {"p1", "p4"},
	}
	for _, d := range devs {
		for _, p := range dp[d] {
			mp[p] = true
		}
	}
	out := make([]string, 0, len(mp))
	for p := range mp {
		out = append(out, p)
	}
	sort.Strings(out)
	return out
}

func genSequence(seed int64, nTickets, nOps int) []fuzzOp {
	rng := rand.New(rand.NewSource(seed))
	g := &genCtx{
		rng: rng, pApp: map[string]string{}, pWork: map[string][]string{},
		pPts: map[string][]string{}, pDev: map[string][]string{},
	}
	devsAll := []string{"devA", "devB", "devC", "devD"}
	wpool := []string{"wA", "wB", "wC", "appB", "x1", "x2"}
	apps := []string{"appA", "appB", "x1"}
	wts := []string{"normal", "normal", "normal", "high_risk", "observation"}
	var ops []fuzzOp
	for i := 0; i < nTickets; i++ {
		id := fmt.Sprintf("tk%d", i)
		ap := apps[rng.Intn(len(apps))]
		k := 1 + rng.Intn(3)
		dset := map[string]bool{}
		for len(dset) < k {
			dset[devsAll[rng.Intn(len(devsAll))]] = true
		}
		dl := make([]string, 0, k)
		for d := range dset {
			dl = append(dl, d)
		}
		sort.Strings(dl)
		wk := 1 + rng.Intn(3)
		ws := map[string]bool{}
		for len(ws) < wk {
			ws[wpool[rng.Intn(len(wpool))]] = true
		}
		wl := make([]string, 0, wk)
		for w := range ws {
			wl = append(wl, w)
		}
		sort.Strings(wl)
		st := int64(rng.Intn(60))
		en := st + int64(1+rng.Intn(60))
		wt := wts[rng.Intn(len(wts))]
		ops = append(ops, fuzzOp{kind: opApply, id: id, actor: ap, devs: dl, wt: wt, st: st, en: en, workers: wl})
		g.permits = append(g.permits, id)
		g.pApp[id] = ap
		g.pWork[id] = wl
		g.pDev[id] = dl
		g.pPts[id] = allPoints(dl)
	}
	approvers := []string{"apvA", "apvB", "x1", "x2"}
	sups := []string{"supA", "supB", "x1"}
	for i := 0; i < nOps; i++ {
		id := g.permits[rng.Intn(len(g.permits))]
		// 5% 引用不存在的票/人/点，制造 NotFound。
		bogus := rng.Intn(20) == 0
		ws, pts := g.pWork[id], g.pPts[id]
		switch rng.Intn(14) {
		case 0:
			who := approvers[rng.Intn(len(approvers))]
			if bogus {
				who = "ghost"
			}
			ops = append(ops, fuzzOp{kind: opApprove, id: id, actor: who, at: g.adv()})
		case 1:
			w, p := pickStr(rng, ws), pickStr(rng, pts)
			if bogus {
				p = "p999"
			}
			ops = append(ops, fuzzOp{kind: opPlaceLock, id: id, worker: w, point: p, at: g.adv()})
		case 2:
			v := "verifier"
			if rng.Intn(4) == 0 {
				v = pickStr(rng, ws)
			}
			ops = append(ops, fuzzOp{kind: opVerify, id: id, actor: v, at: g.adv()})
		case 3:
			ops = append(ops, fuzzOp{kind: opStart, id: id, actor: g.pApp[id], at: g.adv()})
		case 4:
			ops = append(ops, fuzzOp{kind: opEnter, id: id, worker: pickStr(rng, ws), at: g.adv()})
		case 5:
			ops = append(ops, fuzzOp{kind: opLeave, id: id, worker: pickStr(rng, ws), at: g.adv()})
		case 6:
			ops = append(ops, fuzzOp{kind: opComplete, id: id, actor: g.pApp[id], at: g.adv()})
		case 7:
			ops = append(ops, fuzzOp{kind: opRemoveLock, id: id, worker: pickStr(rng, ws), point: pickStr(rng, pts), at: g.adv()})
		case 8:
			ops = append(ops, fuzzOp{kind: opBeginTrial, id: id, actor: g.pApp[id], at: g.adv()})
		case 9:
			ops = append(ops, fuzzOp{kind: opEndTrial, id: id, actor: g.pApp[id], at: g.adv()})
		case 10:
			s1 := sups[rng.Intn(len(sups))]
			s2 := sups[rng.Intn(len(sups))]
			reason := "overdue emergency"
			if rng.Intn(10) == 0 {
				reason = ""
			}
			ops = append(ops, fuzzOp{kind: opForceRemove, id: id, point: pickStr(rng, pts),
				worker: pickStr(rng, ws), actor: s1, actor2: s2, reason: reason, at: g.adv()})
		case 11:
			dev := devsAll[rng.Intn(len(devsAll))]
			if bogus {
				dev = "ghostdev"
			}
			ops = append(ops, fuzzOp{kind: opEnergize, dev: dev, at: g.clock})
		case 12:
			// 非法空串参数
			ops = append(ops, fuzzOp{kind: opApprove, id: "", actor: "", at: g.adv()})
		default:
			ops = append(ops, fuzzOp{kind: opEnter, id: "ghost-ticket", worker: "wA", at: g.adv()})
		}
	}
	return ops
}

func TestDifferentialRandom(t *testing.T) {
	var logFile *os.File
	if *logPath != "" {
		f, err := os.Create(*logPath)
		if err != nil {
			t.Fatalf("open log: %v", err)
		}
		logFile = f
		defer f.Close()
	}
	emit := func(format string, args ...any) {
		line := fmt.Sprintf(format, args...)
		t.Logf("%s", line)
		if logFile != nil {
			fmt.Fprintln(logFile, line)
		}
	}

	iterations := 400
	if testing.Short() {
		iterations = 40
	}
	for iter := 0; iter < iterations; iter++ {
		seed := int64(1000 + iter*7)
		ops := genSequence(seed, 2+(iter%5), 120)
		s := loto.New()
		n := newNaive()
		initWorld(s, n)
		emit("==== seed=%d tickets/ops generated=%d ====", seed, len(ops))
		for step, o := range ops {
			rc, ren, rreason := runReal(s, o)
			nc, nen, _ := runNaive(n, o)
			emit("[%03d] IN  %s", step, o.String())
			ener := ""
			if o.kind == opEnergize {
				ener = fmt.Sprintf(" | energize real=%v naive=%v", ren, nen)
			}
			emit("      OUT real=%s naive=%s%s", rc, nc, ener)
			if o.kind == opEnergize && rreason != "" {
				emit("      WHY %s", rreason)
			}
			if rc != nc {
				t.Fatalf("seed=%d step=%d op=%s code mismatch real=%s naive=%s",
					seed, step, o.String(), rc, nc)
			}
			if o.kind == opEnergize && ren != nen {
				t.Fatalf("seed=%d step=%d energize mismatch real=%v naive=%v op=%s",
					seed, step, ren, nen, o.String())
			}
			if diff, eq := statesEqual(realStateOf(s), naiveStateOf(n)); !eq {
				t.Fatalf("seed=%d step=%d state mismatch after %s -> %s\n%s",
					seed, step, o.String(), rc, diff)
			}
		}
		emit("==== seed=%d converged (%d ops) ====", seed, len(ops))
	}
}
