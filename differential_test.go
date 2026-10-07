package ontology_test

import (
	"fmt"
	"math/rand"
	"os"
	"reflect"
	"sort"
	"strconv"
	"testing"

	"ontology"
	"ontology/naive"
)

// 本测试以独立朴素模型为参照，对随机操作序列做逐步对照：
//   1. 主引擎与朴素引擎逐步结果（含错误类别）必须完全一致；
//   2. 相同操作序列在全新主引擎上重放，结果必须完全相同；
//   3. 把最终登记集合按规范顺序一次性重建（住宿全部追补、病例按
//      登记时刻重登、隔离/改正/撤销收尾），查询结果必须与原引擎一致。
// 每一步的输入、输出与判定依据都写入日志文件（默认 testdata/differential.log）。

// api 是两个引擎共同满足的接口。
type api interface {
	Admit(patient, ward string, at, now int64) error
	Discharge(patient string, at, now int64) error
	BackfillStay(patient, ward string, in, out, now int64) error
	RegisterCase(caseID, patient string, onset, now int64) error
	RegisterIsolation(caseID string, at, now int64) error
	CorrectOnset(caseID string, onset, now int64) error
	RevokeCase(caseID string, now int64) error
	Status(patient string, now int64) (ontology.StatusResult, error)
	CaseContacts(caseID string, now int64) (ontology.ContactsResult, error)
}

type opKind int

const (
	opAdmit opKind = iota
	opDischarge
	opBackfill
	opRegCase
	opIsolate
	opCorrect
	opRevoke
	opStatus
	opContacts
)

// op 一个操作的全部输入。
type op struct {
	kind     opKind
	a, b     string
	x, y, nw int64
}

func (o op) desc() string {
	switch o.kind {
	case opAdmit:
		return fmt.Sprintf("Admit(patient=%s ward=%s at=%d now=%d)", o.a, o.b, o.x, o.nw)
	case opDischarge:
		return fmt.Sprintf("Discharge(patient=%s at=%d now=%d)", o.a, o.x, o.nw)
	case opBackfill:
		return fmt.Sprintf("BackfillStay(patient=%s ward=%s in=%d out=%d now=%d)", o.a, o.b, o.x, o.y, o.nw)
	case opRegCase:
		return fmt.Sprintf("RegisterCase(case=%s patient=%s onset=%d now=%d)", o.a, o.b, o.x, o.nw)
	case opIsolate:
		return fmt.Sprintf("RegisterIsolation(case=%s at=%d now=%d)", o.a, o.x, o.nw)
	case opCorrect:
		return fmt.Sprintf("CorrectOnset(case=%s onset=%d now=%d)", o.a, o.x, o.nw)
	case opRevoke:
		return fmt.Sprintf("RevokeCase(case=%s now=%d)", o.a, o.nw)
	case opStatus:
		return fmt.Sprintf("Status(patient=%s now=%d)", o.a, o.nw)
	default:
		return fmt.Sprintf("CaseContacts(case=%s now=%d)", o.a, o.nw)
	}
}

// outcome 一个操作的输出：错误类别（0 表示成功）与查询结果。
type outcome struct {
	errKind  ontology.Kind
	status   *ontology.StatusResult
	contacts *ontology.ContactsResult
}

func (o op) apply(e api) outcome {
	switch o.kind {
	case opAdmit:
		return outcome{errKind: ontology.KindOf(e.Admit(o.a, o.b, o.x, o.nw))}
	case opDischarge:
		return outcome{errKind: ontology.KindOf(e.Discharge(o.a, o.x, o.nw))}
	case opBackfill:
		return outcome{errKind: ontology.KindOf(e.BackfillStay(o.a, o.b, o.x, o.y, o.nw))}
	case opRegCase:
		return outcome{errKind: ontology.KindOf(e.RegisterCase(o.a, o.b, o.x, o.nw))}
	case opIsolate:
		return outcome{errKind: ontology.KindOf(e.RegisterIsolation(o.a, o.x, o.nw))}
	case opCorrect:
		return outcome{errKind: ontology.KindOf(e.CorrectOnset(o.a, o.x, o.nw))}
	case opRevoke:
		return outcome{errKind: ontology.KindOf(e.RevokeCase(o.a, o.nw))}
	case opStatus:
		st, err := e.Status(o.a, o.nw)
		return outcome{errKind: ontology.KindOf(err), status: &st}
	default:
		ct, err := e.CaseContacts(o.a, o.nw)
		return outcome{errKind: ontology.KindOf(err), contacts: &ct}
	}
}

func (out outcome) String() string {
	switch {
	case out.errKind != 0:
		return "ERR " + out.errKind.String()
	case out.status != nil:
		s := out.status
		r := fmt.Sprintf("%s release=%d 依据=[", s.Status, s.ReleaseAt)
		for _, src := range s.Sources {
			in := "期外"
			if src.InPeriod {
				in = "期内"
			}
			r += fmt.Sprintf("(%s %s 最后接触=%d 解除=%d %s) ",
				src.CaseID, src.Level, src.LastContact, src.ReleaseAt, in)
		}
		return r + "]"
	case out.contacts != nil:
		return fmt.Sprintf("密接=%+v 次密接=%+v", out.contacts.Close, out.contacts.Secondary)
	}
	return "OK"
}

func outcomesEqual(x, y outcome) bool {
	if x.errKind != y.errKind {
		return false
	}
	if (x.status == nil) != (y.status == nil) {
		return false
	}
	if x.status != nil && !reflect.DeepEqual(*x.status, *y.status) {
		return false
	}
	if (x.contacts == nil) != (y.contacts == nil) {
		return false
	}
	if x.contacts != nil && !reflect.DeepEqual(*x.contacts, *y.contacts) {
		return false
	}
	return true
}

// --- 最终状态跟踪（用于规范重建） ---

type stayState struct {
	ward    string
	in, out int64
	open    bool
}

type caseState struct {
	patient      string
	onset        int64
	registeredAt int64
	isolated     bool
	isolatedAt   int64
	revoked      bool
}

type tracker struct {
	stays map[string][]stayState
	cases map[string]*caseState
}

func newTracker() *tracker {
	return &tracker{stays: make(map[string][]stayState), cases: make(map[string]*caseState)}
}

func (tr *tracker) onAccepted(o op) {
	switch o.kind {
	case opAdmit:
		tr.stays[o.a] = append(tr.stays[o.a], stayState{ward: o.b, in: o.x, open: true})
	case opDischarge:
		ss := tr.stays[o.a]
		for i := range ss {
			if ss[i].open {
				ss[i].open = false
				ss[i].out = o.x
			}
		}
	case opBackfill:
		tr.stays[o.a] = append(tr.stays[o.a], stayState{ward: o.b, in: o.x, out: o.y})
	case opRegCase:
		tr.cases[o.a] = &caseState{patient: o.b, onset: o.x, registeredAt: o.nw}
	case opIsolate:
		c := tr.cases[o.a]
		c.isolated = true
		c.isolatedAt = o.x
	case opCorrect:
		tr.cases[o.a].onset = o.x
	case opRevoke:
		tr.cases[o.a].revoked = true
	}
}

// buildCanonical 把最终登记集合按规范顺序一次性重建：
// 病例按确诊登记时刻重登（必要时先以过渡发病时刻登记，隔离后再改正），
// 住宿全部以追补/入住形式给出，最后登记隔离、改正与撤销。
func buildCanonical(t *testing.T, tr *tracker, tFinal int64) *ontology.Engine {
	t.Helper()
	e := ontology.NewEngine()

	ids := make([]string, 0, len(tr.cases))
	for id := range tr.cases {
		ids = append(ids, id)
	}
	sort.Slice(ids, func(i, j int) bool {
		ci, cj := tr.cases[ids[i]], tr.cases[ids[j]]
		if ci.registeredAt != cj.registeredAt {
			return ci.registeredAt < cj.registeredAt
		}
		return ids[i] < ids[j]
	})
	onset0 := make(map[string]int64)
	for _, id := range ids {
		c := tr.cases[id]
		o0 := minI64(c.onset, c.registeredAt)
		if c.isolated && !c.revoked {
			o0 = minI64(o0, c.isolatedAt+ontology.InfectiousLeadMinutes)
		}
		onset0[id] = o0
		if err := e.RegisterCase(id, c.patient, o0, c.registeredAt); err != nil {
			t.Fatalf("canonical RegisterCase(%s): %v", id, err)
		}
	}

	patients := make([]string, 0, len(tr.stays))
	for p := range tr.stays {
		patients = append(patients, p)
	}
	sort.Strings(patients)
	for _, p := range patients {
		ss := append([]stayState(nil), tr.stays[p]...)
		sort.Slice(ss, func(i, j int) bool { return ss[i].in < ss[j].in })
		for _, s := range ss {
			var err error
			if s.open {
				err = e.Admit(p, s.ward, s.in, tFinal)
			} else {
				err = e.BackfillStay(p, s.ward, s.in, s.out, tFinal)
			}
			if err != nil {
				t.Fatalf("canonical stay(%s %+v): %v", p, s, err)
			}
		}
	}

	sort.Strings(ids)
	for _, id := range ids { // 先隔离（此时发病时刻的传染期起点检查仍满足）
		c := tr.cases[id]
		if c.isolated && !c.revoked {
			if err := e.RegisterIsolation(id, c.isolatedAt, tFinal); err != nil {
				t.Fatalf("canonical Isolate(%s): %v", id, err)
			}
		}
	}
	for _, id := range ids { // 再改正发病时刻
		c := tr.cases[id]
		if !c.revoked && c.onset != onset0[id] {
			if err := e.CorrectOnset(id, c.onset, tFinal); err != nil {
				t.Fatalf("canonical Correct(%s): %v", id, err)
			}
		}
	}
	for _, id := range ids { // 最后撤销
		if tr.cases[id].revoked {
			if err := e.RevokeCase(id, tFinal); err != nil {
				t.Fatalf("canonical Revoke(%s): %v", id, err)
			}
		}
	}
	return e
}

func minI64(a, b int64) int64 {
	if a < b {
		return a
	}
	return b
}

func envInt(key string, def int) int {
	if v := os.Getenv(key); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			return n
		}
	}
	return def
}

// genOp 生成一个随机操作。now 为生成器维护的单调时钟，clock 为引擎当前时钟。
// 以小概率注入非法参数、越界时刻与时钟回退，覆盖错误路径。
func genOp(rng *rand.Rand, now, clock int64, patients, wards, cases []string) op {
	pick := func(pool []string) string { return pool[rng.Intn(len(pool))] }
	past := func(span int64) int64 {
		t := now - rng.Int63n(span)
		if t < 0 {
			t = 0
		}
		return t
	}

	var o op
	switch w := rng.Intn(100); {
	case w < 10:
		o = op{kind: opAdmit, a: pick(patients), b: pick(wards), x: past(2000), nw: now}
	case w < 18:
		o = op{kind: opDischarge, a: pick(patients), x: past(1000), nw: now}
	case w < 33:
		in := past(15000)
		out := in + 1 + rng.Int63n(3000)
		if out > now {
			out = now
		}
		if out <= in && in > 0 {
			in = out - 1
		}
		o = op{kind: opBackfill, a: pick(patients), b: pick(wards), x: in, y: out, nw: now}
	case w < 39:
		o = op{kind: opRegCase, a: pick(cases), b: pick(patients), x: past(10000), nw: now}
	case w < 44:
		o = op{kind: opIsolate, a: pick(cases), x: past(5000), nw: now}
	case w < 48:
		o = op{kind: opCorrect, a: pick(cases), x: past(10000), nw: now}
	case w < 50:
		o = op{kind: opRevoke, a: pick(cases), nw: now}
	case w < 72:
		o = op{kind: opStatus, a: pick(patients), nw: now}
	default:
		o = op{kind: opContacts, a: pick(cases), nw: now}
	}

	// 注入非法操作（不改变生成器时钟）
	switch r := rng.Intn(100); {
	case r < 2: // 空标识
		o.a = ""
	case r < 4: // 未来时刻 / 越界时刻
		o.x = now + 100
		o.y = now + 200
	case r < 6: // 时钟回退
		if clock > 0 {
			o.nw = clock - 1
		}
	case r < 7: // 负时刻
		o.x = -1
	}
	return o
}

func TestDifferentialAgainstNaive(t *testing.T) {
	sequences := envInt("ONT_DIFF_SEQ", 1500)
	opsPerSeq := envInt("ONT_DIFF_OPS", 60)
	baseSeed := int64(envInt("ONT_DIFF_SEED", 1558))
	logPath := os.Getenv("ONT_DIFF_LOG")
	if logPath == "" {
		logPath = "testdata/differential.log"
	}
	if err := os.MkdirAll("testdata", 0o755); err != nil {
		t.Fatal(err)
	}
	logf, err := os.Create(logPath)
	if err != nil {
		t.Fatal(err)
	}
	defer logf.Close()

	patients := make([]string, 10)
	for i := range patients {
		patients[i] = fmt.Sprintf("P%d", i)
	}
	wards := []string{"W0", "W1", "W2", "W3"}
	caseIDs := make([]string, 6)
	for i := range caseIDs {
		caseIDs[i] = fmt.Sprintf("C%d", i)
	}

	var totalOps, accepted, rejected, queries int
	kindCount := make(map[ontology.Kind]int)

	for seq := 0; seq < sequences; seq++ {
		seed := baseSeed + int64(seq)
		rng := rand.New(rand.NewSource(seed))
		fmt.Fprintf(logf, "==== 序列 %d 种子 %d ====\n", seq, seed)

		eng := ontology.NewEngine()
		ref := naive.NewEngine()
		tr := newTracker()
		history := make([]op, 0, opsPerSeq)
		outcomes := make([]outcome, 0, opsPerSeq)

		now := int64(0)
		for step := 0; step < opsPerSeq; step++ {
			now += rng.Int63n(901)
			o := genOp(rng, now, eng.Clock(), patients, wards, caseIDs)

			gotMain := o.apply(eng)
			gotNaive := o.apply(ref)
			totalOps++
			if gotMain.errKind == 0 {
				accepted++
				tr.onAccepted(o)
			} else {
				rejected++
				kindCount[gotMain.errKind]++
			}
			if o.kind == opStatus || o.kind == opContacts {
				queries++
			}
			fmt.Fprintf(logf, "seq=%d step=%02d %s => %s\n", seq, step, o.desc(), gotMain)

			if !outcomesEqual(gotMain, gotNaive) {
				t.Fatalf("序列 %d 步 %d 主引擎与朴素模型不一致\nop: %s\n主引擎: %s\n朴素: %s",
					seq, step, o.desc(), gotMain, gotNaive)
			}
			history = append(history, o)
			outcomes = append(outcomes, gotMain)
		}

		// 重放确定性：相同操作序列在全新引擎上必须得到完全相同的结果
		replay := ontology.NewEngine()
		for i, o := range history {
			got := o.apply(replay)
			if !outcomesEqual(got, outcomes[i]) {
				t.Fatalf("序列 %d 步 %d 重放不一致\nop: %s\n首次: %s\n重放: %s",
					seq, i, o.desc(), outcomes[i], got)
			}
		}

		// 规范重建：把全部登记按规范顺序一次性给出，结果必须一致
		tFinal := eng.Clock()
		canon := buildCanonical(t, tr, tFinal)
		for _, p := range patients {
			sMain, errMain := eng.Status(p, tFinal)
			sCanon, errCanon := canon.Status(p, tFinal)
			if ontology.KindOf(errMain) != ontology.KindOf(errCanon) ||
				(errMain == nil && !reflect.DeepEqual(sMain, sCanon)) {
				t.Fatalf("序列 %d 规范重建后 Status(%s) 不一致\n原引擎: %+v %v\n重建: %+v %v",
					seq, p, sMain, errMain, sCanon, errCanon)
			}
		}
		for _, id := range caseIDs {
			cMain, errMain := eng.CaseContacts(id, tFinal)
			cCanon, errCanon := canon.CaseContacts(id, tFinal)
			if ontology.KindOf(errMain) != ontology.KindOf(errCanon) ||
				(errMain == nil && !reflect.DeepEqual(cMain, cCanon)) {
				t.Fatalf("序列 %d 规范重建后 CaseContacts(%s) 不一致\n原引擎: %+v %v\n重建: %+v %v",
					seq, id, cMain, errMain, cCanon, errCanon)
			}
		}
		fmt.Fprintf(logf, "序列 %d 重放与规范重建一致\n", seq)
	}

	fmt.Fprintf(logf, "==== 汇总：序列 %d，总操作 %d，接受 %d，拒绝 %d（%v），查询 %d ====\n",
		sequences, totalOps, accepted, rejected, kindCount, queries)
	t.Logf("序列=%d 总操作=%d 接受=%d 拒绝=%d %v 查询=%d 日志=%s",
		sequences, totalOps, accepted, rejected, kindCount, queries, logPath)
}
