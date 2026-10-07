package pharmacy_test

import (
	"bufio"
	"fmt"
	"math/rand"
	"os"
	"reflect"
	"strconv"
	"sync"
	"sync/atomic"
	"testing"

	. "ontology/pharmacy"
	"ontology/pharmacy/naive"
)

// ---- 操作表示 ----

type fopKind int

const (
	fRegister fopKind = iota
	fInbound
	fAccept
	fDispense
	fCancel
	fQueryRx
	fQueryDrug
)

type fop struct {
	kind       fopKind
	now        int
	drugID     string
	box        int
	splittable bool
	qty        int
	rxID       string
	rxIn       RxInput
}

func (op fop) desc() string {
	switch op.kind {
	case fRegister:
		return fmt.Sprintf("register(drug=%s box=%d splittable=%v)", op.drugID, op.box, op.splittable)
	case fInbound:
		return fmt.Sprintf("inbound(drug=%s qty=%d)", op.drugID, op.qty)
	case fAccept:
		return fmt.Sprintf("accept(rx=%s patient=%s issue=%d whole=%v lines=%v)", op.rxIn.ID, op.rxIn.Patient, op.rxIn.IssueTime, op.rxIn.WholeOrder, op.rxIn.Lines)
	case fDispense:
		return fmt.Sprintf("dispense(rx=%s)", op.rxID)
	case fCancel:
		return fmt.Sprintf("cancel(rx=%s)", op.rxID)
	case fQueryRx:
		return fmt.Sprintf("queryRx(rx=%s)", op.rxID)
	case fQueryDrug:
		return fmt.Sprintf("queryDrug(drug=%s)", op.drugID)
	}
	return "?"
}

func applyEngine(e *Engine, op fop) (any, error) {
	switch op.kind {
	case fRegister:
		return nil, e.RegisterDrug(op.now, op.drugID, op.box, op.splittable)
	case fInbound:
		return nil, e.Inbound(op.now, op.drugID, op.qty)
	case fAccept:
		return nil, e.AcceptPrescription(op.now, op.rxIn)
	case fDispense:
		return nil, e.Dispense(op.now, op.rxID)
	case fCancel:
		return nil, e.CancelPrescription(op.now, op.rxID)
	case fQueryRx:
		return e.QueryPrescription(op.now, op.rxID)
	case fQueryDrug:
		return e.QueryDrug(op.now, op.drugID)
	}
	return nil, nil
}

func applyNaive(n *naive.Engine, op fop) (any, error) {
	switch op.kind {
	case fRegister:
		return nil, n.RegisterDrug(op.now, op.drugID, op.box, op.splittable)
	case fInbound:
		return nil, n.Inbound(op.now, op.drugID, op.qty)
	case fAccept:
		return nil, n.AcceptPrescription(op.now, op.rxIn)
	case fDispense:
		return nil, n.Dispense(op.now, op.rxID)
	case fCancel:
		return nil, n.CancelPrescription(op.now, op.rxID)
	case fQueryRx:
		return n.QueryPrescription(op.now, op.rxID)
	case fQueryDrug:
		return n.QueryDrug(op.now, op.drugID)
	}
	return nil, nil
}

// ---- 随机序列生成 ----

type gen struct {
	rng   *rand.Rand
	now   int
	drugs []string
	rxs   []string
	live  []string // 已成功受理的处方
	drugN int
	rxN   int
}

func (g *gen) pickDrug() string {
	if len(g.drugs) == 0 || g.rng.Intn(100) < 8 {
		return "ghost" // 不存在的药品
	}
	return g.drugs[g.rng.Intn(len(g.drugs))]
}

func (g *gen) pickRx() string {
	if len(g.live) > 0 && g.rng.Intn(100) < 70 {
		return g.live[g.rng.Intn(len(g.live))]
	}
	if len(g.rxs) == 0 || g.rng.Intn(100) < 8 {
		return "ghost"
	}
	return g.rxs[g.rng.Intn(len(g.rxs))]
}

func (g *gen) removeLive(id string) {
	for i, x := range g.live {
		if x == id {
			g.live = append(g.live[:i], g.live[i+1:]...)
			return
		}
	}
}

func (g *gen) advance() {
	g.now += g.rng.Intn(40)
	if g.rng.Intn(100) < 3 { // 时钟回退尝试
		g.now -= g.rng.Intn(100)
		if g.now < 0 {
			g.now = 0
		}
	}
	if g.rng.Intn(100) < 2 { // 大跳变，跨有效期
		g.now += g.rng.Intn(5000)
	}
	if g.now > MaxTime {
		g.now = MaxTime
	}
}

func (g *gen) next() fop {
	g.advance()
	roll := g.rng.Intn(100)
	switch {
	case roll < 12: // 登记药品
		id := fmt.Sprintf("D%d", g.drugN)
		g.drugN++
		if len(g.drugs) > 0 && g.rng.Intn(100) < 15 {
			id = g.drugs[g.rng.Intn(len(g.drugs))] // 重复登记
		} else {
			g.drugs = append(g.drugs, id)
		}
		op := fop{kind: fRegister, now: g.now, drugID: id, box: 1 + g.rng.Intn(8), splittable: g.rng.Intn(2) == 0}
		if g.rng.Intn(100) < 5 { // 参数非法
			op.box = 0
		}
		return op
	case roll < 27: // 入库
		op := fop{kind: fInbound, now: g.now, drugID: g.pickDrug(), qty: 1 + g.rng.Intn(60)}
		if g.rng.Intn(100) < 5 {
			op.qty = 0 // 参数非法
		}
		return op
	case roll < 57: // 受理处方
		id := fmt.Sprintf("R%d", g.rxN)
		g.rxN++
		nLines := 1 + g.rng.Intn(3)
		if g.rng.Intn(100) < 4 {
			nLines = 9 // 参数非法：行数超限
		}
		lines := make([]LineInput, 0, nLines)
		used := map[string]bool{}
		for i := 0; i < nLines; i++ {
			d := g.pickDrug()
			if used[d] && len(g.drugs) > 0 {
				d = g.drugs[g.rng.Intn(len(g.drugs))]
			}
			if g.rng.Intn(100) < 3 && len(lines) > 0 { // 参数非法：重复药品行
				d = lines[0].DrugID
			}
			used[d] = true
			q := 1 + g.rng.Intn(20)
			if g.rng.Intn(100) < 3 {
				q = MaxQty + 1 // 参数非法
			}
			lines = append(lines, LineInput{DrugID: d, Qty: q})
		}
		issue := g.now
		if g.rng.Intn(100) < 60 {
			issue = g.now - g.rng.Intn(4500) // 覆盖临近过期与已过期
			if issue < 0 {
				issue = 0
			}
		}
		if g.rng.Intn(100) < 3 {
			issue = g.now + 1 // 参数非法：开具时刻晚于 now
		}
		in := RxInput{
			ID: id, Patient: "P" + strconv.Itoa(g.rxN), IssueTime: issue,
			WholeOrder: g.rng.Intn(100) < 30, Lines: lines,
		}
		if g.rng.Intn(100) < 10 && len(g.rxs) > 0 {
			in.ID = g.rxs[g.rng.Intn(len(g.rxs))] // 重复处方号
		} else {
			g.rxs = append(g.rxs, id)
		}
		return fop{kind: fAccept, now: g.now, rxIn: in}
	case roll < 72: // 取药
		return fop{kind: fDispense, now: g.now, rxID: g.pickRx()}
	case roll < 80: // 取消
		return fop{kind: fCancel, now: g.now, rxID: g.pickRx()}
	case roll < 90: // 查询处方
		return fop{kind: fQueryRx, now: g.now, rxID: g.pickRx()}
	default: // 查询药品
		return fop{kind: fQueryDrug, now: g.now, drugID: g.pickDrug()}
	}
}

// ---- 差分测试 ----

func envInt(name string, def int) int {
	if s := os.Getenv(name); s != "" {
		if v, err := strconv.Atoi(s); err == nil {
			return v
		}
	}
	return def
}

func fmtVal(v any) string {
	switch x := v.(type) {
	case nil:
		return "-"
	case RxState:
		return fmt.Sprintf("%+v", x)
	case DrugState:
		return fmt.Sprintf("%+v", x)
	}
	return fmt.Sprintf("%+v", v)
}

// TestDifferentialFuzz 以独立朴素模型为参照，重放随机操作序列并逐步比对。
// 日志打印每步输入、输出与判定依据。可用环境变量覆盖：
// PHARMACY_FUZZ_SEQS（默认 1500）、PHARMACY_FUZZ_SEED、PHARMACY_FUZZ_LOG。
func TestDifferentialFuzz(t *testing.T) {
	seqs := envInt("PHARMACY_FUZZ_SEQS", 1500)
	seed := int64(envInt("PHARMACY_FUZZ_SEED", 20261007))
	logPath := os.Getenv("PHARMACY_FUZZ_LOG")
	if logPath == "" {
		f, err := os.CreateTemp("", "pharmacy-fuzz-*.log")
		if err != nil {
			t.Fatalf("创建日志文件: %v", err)
		}
		logPath = f.Name()
		f.Close()
	}
	lf, err := os.Create(logPath)
	if err != nil {
		t.Fatalf("打开日志文件: %v", err)
	}
	defer lf.Close()
	log := bufio.NewWriter(lf)
	defer log.Flush()

	rng := rand.New(rand.NewSource(seed))
	totalOps := 0
	for s := 0; s < seqs; s++ {
		r := rng.Intn(25)
		eng, err := NewEngine(Config{R: r})
		if err != nil {
			t.Fatalf("NewEngine: %v", err)
		}
		nav := naive.New(r)
		g := &gen{rng: rng}
		steps := 30 + rng.Intn(40)
		fmt.Fprintf(log, "=== 序列 %d R=%d 步数=%d ===\n", s, r, steps)
		for step := 0; step < steps; step++ {
			op := g.next()
			v1, err1 := applyEngine(eng, op)
			v2, err2 := applyNaive(nav, op)
			if op.kind == fAccept && err1 == nil {
				g.live = append(g.live, op.rxIn.ID)
			}
			if op.kind == fCancel && err1 == nil {
				g.removeLive(op.rxID)
			}
			if op.kind == fDispense && err1 == nil && g.rng.Intn(2) == 0 {
				g.removeLive(op.rxID) // 已取过的处方降权，欠药行仍可能被再次取药
			}
			c1, c2 := CodeOf(err1), CodeOf(err2)
			verdict := "一致"
			basis := ""
			switch {
			case c1 != c2:
				verdict = "不一致"
				basis = fmt.Sprintf("错误码不同: 引擎=%s 模型=%s", c1, c2)
			case (op.kind == fQueryRx || op.kind == fQueryDrug) && c1 == ErrNone && !reflect.DeepEqual(v1, v2):
				verdict = "不一致"
				basis = fmt.Sprintf("查询结果不同: 引擎=%s 模型=%s", fmtVal(v1), fmtVal(v2))
			default:
				d1, p1 := eng.Snapshot()
				d2, p2 := nav.Snapshot()
				if !reflect.DeepEqual(d1, d2) || !reflect.DeepEqual(p1, p2) {
					verdict = "不一致"
					basis = fmt.Sprintf("状态快照不同: 引擎药=%v 模型药=%v 引擎方=%v 模型方=%v", d1, d2, p1, p2)
				} else {
					basis = fmt.Sprintf("错误码一致(%s) 输出一致 状态快照一致 不变量通过", c1)
				}
			}
			fmt.Fprintf(log, "seq=%d step=%d now=%d op=%s | 引擎: err=%s out=%s | 模型: err=%s out=%s | 判定: %s | 依据: %s\n",
				s, step, op.now, op.desc(), c1, fmtVal(v1), c2, fmtVal(v2), verdict, basis)
			if verdict != "一致" {
				log.Flush()
				t.Fatalf("差分失败（日志 %s）: seq=%d step=%d op=%s: %s", logPath, s, step, op.desc(), basis)
			}
			if verr := eng.CheckInvariants(); verr != nil {
				log.Flush()
				t.Fatalf("不变量违反（日志 %s）: seq=%d step=%d op=%s: %v", logPath, s, step, op.desc(), verr)
			}
			totalOps++
		}
	}
	log.Flush()
	t.Logf("差分测试完成: %d 序列, %d 操作, 日志: %s", seqs, totalOps, logPath)
}

// TestReplayDeterminism 相同操作序列在两个引擎实例上重放，结果完全相同。
func TestReplayDeterminism(t *testing.T) {
	rng := rand.New(rand.NewSource(777))
	for s := 0; s < 50; s++ {
		r := rng.Intn(25)
		g := &gen{rng: rng}
		steps := 40 + rng.Intn(30)
		ops := make([]fop, steps)
		for i := range ops {
			ops[i] = g.next()
		}
		e1, _ := NewEngine(Config{R: r})
		e2, _ := NewEngine(Config{R: r})
		for i, op := range ops {
			v1, err1 := applyEngine(e1, op)
			v2, err2 := applyEngine(e2, op)
			if CodeOf(err1) != CodeOf(err2) || !reflect.DeepEqual(v1, v2) {
				t.Fatalf("重放结果不同: seq=%d step=%d op=%s", s, i, op.desc())
			}
		}
		d1, p1 := e1.Snapshot()
		d2, p2 := e2.Snapshot()
		if !reflect.DeepEqual(d1, d2) || !reflect.DeepEqual(p1, p2) {
			t.Fatalf("重放终态不同: seq=%d", s)
		}
	}
}

// TestConcurrentSafety 并发调用等价于某串行顺序：竞态检测 + 不变量保持。
func TestConcurrentSafety(t *testing.T) {
	e, err := NewEngine(Config{R: 5})
	if err != nil {
		t.Fatalf("NewEngine: %v", err)
	}
	for i := 0; i < 4; i++ {
		id := fmt.Sprintf("D%d", i)
		if err := e.RegisterDrug(0, id, 1+i%3, i%2 == 0); err != nil {
			t.Fatalf("RegisterDrug: %v", err)
		}
		if err := e.Inbound(0, id, 500); err != nil {
			t.Fatalf("Inbound: %v", err)
		}
	}
	var wg sync.WaitGroup
	var clock atomic.Int64
	for g := 0; g < 8; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			rng := rand.New(rand.NewSource(int64(g*1000 + 1)))
			for i := 0; i < 300; i++ {
				now := int(clock.Add(1) % 200)
				drug := fmt.Sprintf("D%d", rng.Intn(4))
				rx := fmt.Sprintf("g%d-r%d", g, i)
				switch rng.Intn(6) {
				case 0:
					e.Inbound(now, drug, 1+rng.Intn(20))
				case 1, 2:
					e.AcceptPrescription(now, RxInput{
						ID: rx, Patient: "p", IssueTime: 0,
						WholeOrder: rng.Intn(2) == 0,
						Lines:      []LineInput{{DrugID: drug, Qty: 1 + rng.Intn(15)}},
					})
				case 3:
					e.Dispense(now, fmt.Sprintf("g%d-r%d", g, rng.Intn(i+1)))
				case 4:
					e.CancelPrescription(now, fmt.Sprintf("g%d-r%d", g, rng.Intn(i+1)))
				default:
					e.QueryDrug(now, drug)
				}
			}
		}(g)
	}
	wg.Wait()
	if err := e.CheckInvariants(); err != nil {
		t.Fatalf("并发后不变量违反: %v", err)
	}
}
