package qc

import (
	"fmt"
	"math/rand"
	"os"
	"reflect"
	"strconv"
	"testing"
)

// 随机操作序列对照测试：同一序列同时作用于 System 与朴素模型，
// 逐步比对输出、错误与全部可观察状态，并打印每步输入、输出与判定依据。

type opKind int

const (
	opRegister opKind = iota
	opRun
	opCalibrate
	opIssue
	opReview
	opQueryState
	opQueryReport
)

type op struct {
	kind                opKind
	instrument, analyte string
	a, b, c, d, e       int64 // 登记参数或测量值
	now                 int64
	reportID            int64
}

func (o op) String() string {
	switch o.kind {
	case opRegister:
		return fmt.Sprintf("Register(%s,%s lowT=%d lowSD=%d highT=%d highSD=%d validity=%d)",
			o.instrument, o.analyte, o.a, o.b, o.c, o.d, o.e)
	case opRun:
		return fmt.Sprintf("Run(%s,%s low=%d high=%d now=%d)",
			o.instrument, o.analyte, o.a, o.b, o.now)
	case opCalibrate:
		return fmt.Sprintf("Calibrate(%s,%s now=%d)", o.instrument, o.analyte, o.now)
	case opIssue:
		return fmt.Sprintf("Issue(%s,%s now=%d)", o.instrument, o.analyte, o.now)
	case opReview:
		return fmt.Sprintf("Review(id=%d now=%d)", o.reportID, o.now)
	case opQueryState:
		return fmt.Sprintf("QueryState(%s,%s)", o.instrument, o.analyte)
	default:
		return fmt.Sprintf("QueryReport(id=%d)", o.reportID)
	}
}

var (
	fuzzInstruments = []string{"I1", "I2", "I3"}
	fuzzAnalytes    = []string{"A", "B"}
)

// genValue 生成围绕靶值的测量值：k 倍标准差叠加 -1/0/+1，
// 以高频命中各规则的恰等边界。
func genValue(rng *rand.Rand, target, sd int64) int64 {
	k := int64(rng.Intn(23) - 11) // -11..11
	j := int64(rng.Intn(3) - 1)   // -1..1
	return target + k*sd + j
}

func genSequence(rng *rand.Rand, nOps int) []op {
	params := map[string][5]int64{}
	key := func(i, a string) string { return i + "/" + a }
	now := int64(0)
	var ops []op
	var maxReportID int64
	for i := 0; i < nOps; i++ {
		inst := fuzzInstruments[rng.Intn(len(fuzzInstruments))]
		ana := fuzzAnalytes[rng.Intn(len(fuzzAnalytes))]
		if rng.Intn(40) == 0 { // 偶发非法标识
			inst = ""
		}
		// 时间：多数前进 0..3 秒，偶发回退。
		if rng.Intn(50) == 0 && now > 0 {
			now--
		} else {
			now += int64(rng.Intn(4))
		}
		switch r := rng.Intn(100); {
		case r < 6: // 登记（含非法参数）
			p := [5]int64{100, 10, 200, 20, []int64{5, 50, 1000}[rng.Intn(3)]}
			if rng.Intn(30) == 0 {
				p[1] = 0 // 非法标准差
			}
			params[key(inst, ana)] = p
			ops = append(ops, op{kind: opRegister, instrument: inst, analyte: ana,
				a: p[0], b: p[1], c: p[2], d: p[3], e: p[4]})
		case r < 46: // 运行
			p, ok := params[key(inst, ana)]
			if !ok {
				p = [5]int64{100, 10, 200, 20, 50}
			}
			lowSD, highSD := p[1], p[3]
			if lowSD <= 0 {
				lowSD = 10
			}
			if highSD <= 0 {
				highSD = 20
			}
			if rng.Intn(4) == 0 {
				// 同侧突发段：连续多次运行同一项目、同一水平同侧偏离，
				// 以覆盖规则二/四/五的长连续触发与零值打断。
				burst := 3 + rng.Intn(10)
				signV := int64(1)
				if rng.Intn(2) == 0 {
					signV = -1
				}
				mag := []int64{1, lowSD + 1, lowSD*2 + 1}[rng.Intn(3)]
				for bi := 0; bi < burst && len(ops) < nOps; bi++ {
					lv := p[0] + signV*mag
					if rng.Intn(12) == 0 {
						lv = p[0] // 偶发零偏离，打断连续
					}
					ops = append(ops, op{kind: opRun, instrument: inst, analyte: ana,
						a: lv, b: genValue(rng, p[2], highSD), now: now})
					now += int64(rng.Intn(2))
				}
			} else {
				ops = append(ops, op{kind: opRun, instrument: inst, analyte: ana,
					a: genValue(rng, p[0], lowSD), b: genValue(rng, p[2], highSD), now: now})
			}
		case r < 54: // 校准
			ops = append(ops, op{kind: opCalibrate, instrument: inst, analyte: ana, now: now})
		case r < 79: // 出具
			ops = append(ops, op{kind: opIssue, instrument: inst, analyte: ana, now: now})
			maxReportID++ // 上界估计，实际单号不超过操作数
		case r < 92: // 复核
			id := int64(0)
			if maxReportID > 0 {
				id = int64(rng.Intn(int(maxReportID) + 2))
			}
			ops = append(ops, op{kind: opReview, reportID: id, now: now})
		case r < 96: // 查询项目状态
			ops = append(ops, op{kind: opQueryState, instrument: inst, analyte: ana})
		default: // 查询报告状态
			id := int64(0)
			if maxReportID > 0 {
				id = int64(rng.Intn(int(maxReportID) + 2))
			}
			ops = append(ops, op{kind: opQueryReport, reportID: id})
		}
	}
	return ops
}

// fuzzStats 统计随机对照过程中关键行为的覆盖次数。
type fuzzStats struct {
	ruleFired   [6]int // 按规则编号 1..5
	rejected    int    // 失控运行数
	warning     int    // 警告运行数
	issueDenied [8]int // 出具被拒（按错误）
	pending     int    // 进入待复核的报告数
	reviewed    int    // 完成复核的报告数
	calibrates  int    // 成功的校准数
}

func applyOne(t *testing.T, sys *System, m *naiveModel, o op, step int, st *fuzzStats) {
	t.Helper()
	desc := func(out string) {
		t.Logf("step %03d | %s => %s", step, o, out)
	}
	switch o.kind {
	case opRegister:
		e1 := sys.RegisterAnalyte(o.instrument, o.analyte, o.a, o.b, o.c, o.d, o.e)
		e2 := m.RegisterAnalyte(o.instrument, o.analyte, o.a, o.b, o.c, o.d, o.e)
		if e1 != e2 {
			t.Fatalf("step %d %s: err %v vs model %v", step, o, e1, e2)
		}
		desc(fmt.Sprintf("err=%v", e1))
	case opRun:
		r1, e1 := sys.Run(o.instrument, o.analyte, o.a, o.b, o.now)
		r2, e2 := m.Run(o.instrument, o.analyte, o.a, o.b, o.now)
		if e1 != e2 || !reflect.DeepEqual(r1, r2) {
			t.Fatalf("step %d %s: (%+v,%v) vs model (%+v,%v)", step, o, r1, e1, r2, e2)
		}
		for _, rl := range r1.Rules {
			st.ruleFired[int(rl)]++
		}
		if r1.Status == RunRejected {
			st.rejected++
		} else if r1.Status == RunWarning {
			st.warning++
		}
		basis := ""
		if np, ok := m.projects[projectKey(o.instrument, o.analyte)]; ok && e1 == nil {
			basis = fmt.Sprintf(" lowDev=%d highDev=%d", o.a-np.low.target, o.b-np.high.target)
		}
		desc(fmt.Sprintf("rules=%v status=%v err=%v%s", r1.Rules, r1.Status, e1, basis))
	case opCalibrate:
		e1 := sys.Calibrate(o.instrument, o.analyte, o.now)
		e2 := m.Calibrate(o.instrument, o.analyte, o.now)
		if e1 != e2 {
			t.Fatalf("step %d %s: err %v vs model %v", step, o, e1, e2)
		}
		if e1 == nil {
			st.calibrates++
		}
		desc(fmt.Sprintf("err=%v", e1))
	case opIssue:
		id1, e1 := sys.IssueReport(o.instrument, o.analyte, o.now)
		id2, e2 := m.IssueReport(o.instrument, o.analyte, o.now)
		if e1 != e2 || id1 != id2 {
			t.Fatalf("step %d %s: (%d,%v) vs model (%d,%v)", step, o, id1, e1, id2, e2)
		}
		if e1 != nil {
			st.issueDenied[0]++
		}
		desc(fmt.Sprintf("id=%d err=%v", id1, e1))
	case opReview:
		e1 := sys.Review(o.reportID, o.now)
		e2 := m.Review(o.reportID, o.now)
		if e1 != e2 {
			t.Fatalf("step %d %s: err %v vs model %v", step, o, e1, e2)
		}
		if e1 == nil {
			st.reviewed++
		}
		desc(fmt.Sprintf("err=%v", e1))
	case opQueryState:
		s1, e1 := sys.ProjectState(o.instrument, o.analyte)
		s2, e2 := m.ProjectState(o.instrument, o.analyte)
		if e1 != e2 || s1 != s2 {
			t.Fatalf("step %d %s: (%v,%v) vs model (%v,%v)", step, o, s1, e1, s2, e2)
		}
		desc(fmt.Sprintf("state=%v err=%v", s1, e1))
	case opQueryReport:
		s1, e1 := sys.ReportStatus(o.reportID)
		s2, e2 := m.ReportStatus(o.reportID)
		if e1 != e2 || s1 != s2 {
			t.Fatalf("step %d %s: (%v,%v) vs model (%v,%v)", step, o, s1, e1, s2, e2)
		}
		desc(fmt.Sprintf("status=%v err=%v", s1, e1))
	}
}

// compareAll 在序列结束后比对全部报告与项目状态。
func compareAll(t *testing.T, sys *System, m *naiveModel, seq int) {
	t.Helper()
	for id := int64(1); id <= int64(len(m.reports))+1; id++ {
		s1, e1 := sys.ReportStatus(id)
		s2, e2 := m.ReportStatus(id)
		if e1 != e2 || s1 != s2 {
			t.Fatalf("seq %d final report %d: (%v,%v) vs model (%v,%v)", seq, id, s1, e1, s2, e2)
		}
	}
	for _, inst := range fuzzInstruments {
		for _, ana := range fuzzAnalytes {
			s1, e1 := sys.ProjectState(inst, ana)
			s2, e2 := m.ProjectState(inst, ana)
			if e1 != e2 || s1 != s2 {
				t.Fatalf("seq %d final state %s/%s: (%v,%v) vs model (%v,%v)", seq, inst, ana, s1, e1, s2, e2)
			}
		}
	}
}

func TestModelComparison(t *testing.T) {
	sequences := 1500
	if v := os.Getenv("QC_FUZZ_SEQS"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			sequences = n
		}
	}
	rng := rand.New(rand.NewSource(20261008))
	var st fuzzStats
	for seq := 0; seq < sequences; seq++ {
		sys := New()
		model := newNaiveModel()
		ops := genSequence(rng, 40)
		for i, o := range ops {
			applyOne(t, sys, model, o, i, &st)
		}
		compareAll(t, sys, model, seq)
		for _, r := range model.reports {
			if r.Status == ReportPendingReview {
				st.pending++
			}
		}
		if t.Failed() {
			t.Fatalf("sequence %d diverged", seq)
		}
	}
	t.Logf("coverage: rules=[r1:%d r2:%d r3:%d r4:%d r5:%d] rejected=%d warning=%d "+
		"issueDenied=%d pending=%d reviewed=%d calibrates=%d",
		st.ruleFired[1], st.ruleFired[2], st.ruleFired[3], st.ruleFired[4], st.ruleFired[5],
		st.rejected, st.warning, st.issueDenied[0], st.pending, st.reviewed, st.calibrates)
	for r := 1; r <= 5; r++ {
		if st.ruleFired[r] == 0 {
			t.Fatalf("rule %d never fired in %d sequences", r, sequences)
		}
	}
	if st.pending == 0 || st.reviewed == 0 || st.calibrates == 0 || st.warning == 0 {
		t.Fatalf("insufficient behavior coverage: %+v", st)
	}
}
