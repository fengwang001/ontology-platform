package hd

import (
	"fmt"
	"io"
	"math/rand"
	"os"
	"sort"
	"testing"
)

// opKind 为差分测试支持的操作集合。
type opKind int

const (
	opRegBay opKind = iota
	opRegPatient
	opBook
	opApplyPlan
	opCancel
	opCancelPlan
	opFault
	opRecover
	opChange
)

type op struct {
	kind                           opKind
	now                            int
	id, pid                        string
	zone                           Zone
	observed                       bool
	inf                            Infection
	start, dur, faultAt, recoverAt int
	days                           map[int]bool
	dayStart, from, to, changeAt   int
}

type snap struct {
	bays     map[string]string // id -> zone/observed 编码
	patients map[string]Infection
	treats   map[string]string // id -> bay|start|end|inf|plan
	faults   map[string]string
	lastNow  int
}

func snapshotSystem(s *System) snap {
	s.mu.Lock()
	defer s.mu.Unlock()
	sp := snap{
		bays:     map[string]string{},
		patients: map[string]Infection{},
		treats:   map[string]string{},
		faults:   map[string]string{},
		lastNow:  s.clk.last,
	}
	for id, b := range s.bays {
		sp.bays[id] = fmt.Sprintf("%d/%v/%v", b.Zone, b.Observed, b.Available)
	}
	for id, p := range s.patients {
		sp.patients[id] = p.Infection
	}
	for id, t := range s.treatments {
		sp.treats[id] = fmt.Sprintf("%s|%d|%d|%d|%s", t.BayID, t.Start, t.End, t.Duration, t.Infection)
	}
	for id, f := range s.faults {
		sp.faults[id] = fmt.Sprintf("%d/%v/%d", f.Start, f.Recovered, f.RecoverAt)
	}
	return sp
}

func snapshotNaive(m *naiveModel) snap {
	sp := snap{
		bays:     map[string]string{},
		patients: map[string]Infection{},
		treats:   map[string]string{},
		faults:   map[string]string{},
		lastNow:  m.lastNow,
	}
	for id, b := range m.bays {
		avail := true
		if f := m.faults[id]; f != nil && !f.recovered {
			avail = false
		}
		sp.bays[id] = fmt.Sprintf("%d/%v/%v", b.zone, b.observed, avail)
	}
	for id, inf := range m.patients {
		sp.patients[id] = inf
	}
	for id, t := range m.treats {
		sp.treats[id] = fmt.Sprintf("%s|%d|%d|%d|%s", t.bay, t.start, t.end, (t.end - t.start), t.inf)
	}
	for id, f := range m.faults {
		sp.faults[id] = fmt.Sprintf("%d/%v/%d", f.start, f.recovered, f.recoverAt)
	}
	return sp
}

func equalSnap(a, b snap) (bool, string) {
	if len(a.bays) != len(b.bays) {
		return false, "bays len"
	}
	for k, v := range a.bays {
		if b.bays[k] != v {
			return false, "bay " + k + ": " + v + " vs " + b.bays[k]
		}
	}
	if len(a.patients) != len(b.patients) {
		return false, "patients len"
	}
	for k, v := range a.patients {
		if b.patients[k] != v {
			return false, "patient " + k
		}
	}
	if len(a.treats) != len(b.treats) {
		ka := keysTreat(a.treats)
		kb := keysTreat(b.treats)
		return false, fmt.Sprintf("treats len %d vs %d; onlyA=%v onlyB=%v", len(a.treats), len(b.treats),
			subtract(ka, kb), subtract(kb, ka))
	}
	for k, v := range a.treats {
		if b.treats[k] != v {
			return false, "treatment " + k + ": " + v + " vs " + b.treats[k]
		}
	}
	for k, v := range a.faults {
		if b.faults[k] != v {
			return false, "fault " + k + ": " + v + " vs " + b.faults[k]
		}
	}
	if a.lastNow != b.lastNow {
		return false, "clock"
	}
	return true, ""
}

func keysTreat(m map[string]string) []string {
	var out []string
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func subtract(a, b []string) []string {
	set := map[string]bool{}
	for _, x := range b {
		set[x] = true
	}
	var out []string
	for _, x := range a {
		if !set[x] {
			out = append(out, x)
		}
	}
	return out
}

// runProd / runNaive 执行一条操作并返回结果字符串与错误码。
func runProd(s *System, o op) (string, ErrorCode) {
	switch o.kind {
	case opRegBay:
		return codeOr(s.RegisterBay(o.now, o.id, o.zone, o.observed, true))
	case opRegPatient:
		return codeOrErr(s.RegisterPatient(o.now, o.id, o.inf))
	case opBook:
		b, err := s.BookTreatment(o.now, o.id, o.pid, o.start, o.dur)
		if err != nil {
			return "", code(err)
		}
		return "bay=" + b, -1
	case opApplyPlan:
		b, err := s.ApplyPlan(o.now, o.id, o.pid, o.days, o.dayStart, o.dur, o.from, o.to)
		if err != nil {
			return "", code(err)
		}
		return fmt.Sprintf("bays=%v", b), -1
	case opCancel:
		return codeOrErr(s.CancelTreatment(o.now, o.id))
	case opCancelPlan:
		return codeOrErr(s.CancelPlan(o.now, o.id))
	case opFault:
		return codeOrErr(s.ReportFault(o.now, o.id, o.faultAt))
	case opRecover:
		return codeOrErr(s.RecoverBay(o.now, o.id, o.recoverAt))
	case opChange:
		return codeOrErr(s.ChangeInfection(o.now, o.pid, o.inf, o.changeAt))
	}
	return "", -999
}

func codeOr(err error) (string, ErrorCode) {
	if err == nil {
		return "ok", -1
	}
	if e, ok := err.(*Error); ok && e == nil {
		return "ok", -1
	}
	if e, ok := err.(*Error); ok {
		return "", e.Code
	}
	return "", -998
}

func codeOrErr(err error) (string, ErrorCode) { return codeOr(err) }

func runNaive(m *naiveModel, o op) (string, ErrorCode) {
	switch o.kind {
	case opRegBay:
		return codeOr(m.registerBay(o.now, o.id, o.zone, o.observed))
	case opRegPatient:
		return codeOr(m.registerPatient(o.now, o.id, o.inf))
	case opBook:
		b, e := m.book(o.now, o.id, o.pid, o.start, o.dur)
		if e != nil {
			return "", e.Code
		}
		return "bay=" + b, -1
	case opApplyPlan:
		b, e := m.applyPlan(o.now, o.id, o.pid, o.days, o.dayStart, o.dur, o.from, o.to)
		if e != nil {
			return "", e.Code
		}
		return fmt.Sprintf("bays=%v", b), -1
	case opCancel:
		return codeOr(m.cancel(o.now, o.id))
	case opCancelPlan:
		return codeOr(m.cancelPlan(o.now, o.id))
	case opFault:
		return codeOr(m.reportFault(o.now, o.id, o.faultAt))
	case opRecover:
		return codeOr(m.recoverBay(o.now, o.id, o.recoverAt))
	case opChange:
		return codeOr(m.changeInfection(o.now, o.pid, o.inf, o.changeAt))
	}
	return "", -999
}

func opString(o op) string {
	switch o.kind {
	case opRegBay:
		return fmt.Sprintf("RegisterBay(now=%d id=%s zone=%s observed=%v)", o.now, o.id, o.zone, o.observed)
	case opRegPatient:
		return fmt.Sprintf("RegisterPatient(now=%d id=%s inf=%s)", o.now, o.id, o.inf)
	case opBook:
		return fmt.Sprintf("BookTreatment(now=%d id=%s pid=%s start=%d dur=%d)", o.now, o.id, o.pid, o.start, o.dur)
	case opApplyPlan:
		return fmt.Sprintf("ApplyPlan(now=%d id=%s pid=%s days=%v dayStart=%d dur=%d from=%d to=%d)",
			o.now, o.id, o.pid, o.days, o.dayStart, o.dur, o.from, o.to)
	case opCancel:
		return fmt.Sprintf("CancelTreatment(now=%d id=%s)", o.now, o.id)
	case opCancelPlan:
		return fmt.Sprintf("CancelPlan(now=%d id=%s)", o.now, o.id)
	case opFault:
		return fmt.Sprintf("ReportFault(now=%d bay=%s at=%d)", o.now, o.id, o.faultAt)
	case opRecover:
		return fmt.Sprintf("RecoverBay(now=%d bay=%s at=%d)", o.now, o.id, o.recoverAt)
	default:
		return fmt.Sprintf("ChangeInfection(now=%d pid=%s inf=%s at=%d)", o.now, o.pid, o.inf, o.changeAt)
	}
}

func TestDifferentialNaive(t *testing.T) {
	logPath := os.Getenv("DIFF_LOG")
	if logPath == "" {
		logPath = "diff_runs.log"
	}
	lf, err := os.Create(logPath)
	if err != nil {
		t.Fatalf("open log: %v", err)
	}
	defer lf.Close()
	logger := io.MultiWriter(os.Stdout, lf)

	const sequences = 1500
	for seq := 0; seq < sequences; seq++ {
		seed := int64(seq*7919 + 101)
		ops := generateOps(rand.New(rand.NewSource(seed)))
		cfg := Config{CleanNegative: 30, CleanHBV: 40, CleanHCV: 50, DeepClean: 120, MinRecovery: 60}
		sys, _ := New(cfg)
		nv := newNaive(cfg)
		fmt.Fprintf(logger, "===== sequence %d seed=%d ops=%d =====\n", seq, seed, len(ops))
		for i, o := range ops {
			rs, cs := runProd(sys, o)
			rn, cn := runNaive(nv, o)
			reason := "accepted"
			if cs != -1 {
				reason = "rejected: " + cs.String()
			}
			fmt.Fprintf(logger, "[%03d] %-9s %s => prod(%s,%s) naive(%s,%s) basis=%s\n",
				i, kindName(o.kind), opString(o), rs, codeName(cs), rn, codeName(cn), reason)
			if cs != cn || rs != rn {
				t.Fatalf("seq %d step %d divergence:\n%s\nprod=(%s,%s) naive=(%s,%s)",
					seq, i, opString(o), rs, codeName(cs), rn, codeName(cn))
			}
			ps, ns := snapshotSystem(sys), snapshotNaive(nv)
			if ok, where := equalSnap(ps, ns); !ok {
				t.Fatalf("seq %d step %d state divergence at %s:\n%s", seq, i, where, opString(o))
			}
		}
	}
	fmt.Fprintf(logger, "ALL %d SEQUENCES MATCH\n", sequences)
}

func codeName(c ErrorCode) string {
	if c == -1 {
		return "OK"
	}
	if c == -999 {
		return "UNHANDLED"
	}
	return c.String()
}

func kindName(k opKind) string {
	return [...]string{"RegBay", "RegPatient", "Book", "ApplyPlan", "Cancel",
		"CancelPlan", "Fault", "Recover", "Change"}[k]
}
