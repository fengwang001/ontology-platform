package notify

import (
	"fmt"
	"math/rand"
	"strings"
	"sync"
	"testing"
)

// 产品侧执行一条操作，投影成与朴素模拟相同的 nOutcome 便于逐字段比对。
func runProduct(m *Manager, o op) nOutcome {
	switch o.kind {
	case kAddTest:
		err := m.AddTest(o.code, o.low, o.high, o.step)
		return nOutcome{err: err, why: "AddTest"}
	case kSetWard:
		return nOutcome{err: m.SetWard(o.patient, o.ward), why: "SetWard"}
	case kGrant:
		return nOutcome{err: m.Grant(o.u1, o.ward, o.role), why: "Grant"}
	case kResult:
		r, err := m.Result(o.now, o.patient, o.code, o.v)
		return nOutcome{
			err: err, normal: r.Normal, eid: r.EventID, created: r.Created,
			upgraded: r.Upgraded, sev: r.Sev, rep: r.Rep, deadline: r.Deadline,
			land: r.LandNow, why: "Result",
		}
	case kNotify:
		r, err := m.Notify(o.now, o.event, o.u1, o.u2)
		return nOutcome{err: err, eid: r.EventID, land: r.LandNow, why: "Notify"}
	case kReadBack:
		r, err := m.ReadBack(o.now, o.event, o.u2, o.v)
		return nOutcome{err: err, eid: r.EventID, mis: r.Mismatch, state: r.State, land: r.LandNow, why: "ReadBack"}
	case kAct:
		r, err := m.Act(o.now, o.event, o.u2)
		return nOutcome{err: err, eid: r.EventID, late: r.Late, land: r.LandNow, why: "Act"}
	}
	return nOutcome{}
}

func idsEq(a, b []int64) bool {
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

func cmpOutcome(a, b nOutcome) string {
	sameErr := errEq(a.err, b.err)
	sameBody := a.normal == b.normal && a.eid == b.eid && a.created == b.created &&
		a.upgraded == b.upgraded && a.mis == b.mis && a.sev == b.sev &&
		a.rep == b.rep && a.deadline == b.deadline && a.state == b.state &&
		a.late == b.late && idsEq(a.land, b.land)
	if !sameErr || (a.err == nil && !sameBody) {
		return fmt.Sprintf("product={err=%v norm=%v eid=%d new=%v up=%v mis=%v sev=%d rep=%d ddl=%d st=%v late=%v land=%v}\n naive={err=%v norm=%v eid=%d new=%v up=%v mis=%v sev=%d rep=%d ddl=%d st=%v late=%v land=%v why=%s}",
			a.err, a.normal, a.eid, a.created, a.upgraded, a.mis, a.sev, a.rep, a.deadline, a.state, a.late, a.land,
			b.err, b.normal, b.eid, b.created, b.upgraded, b.mis, b.sev, b.rep, b.deadline, b.state, b.late, b.land, b.why)
	}
	return ""
}

// 每一步之后逐事件比对最终状态（sev/rep/deadline/state/late/接收人/不符/结果数）。
func cmpState(t *testing.T, m *Manager, n *naive) string {
	if len(m.board.Events()) != len(n.events) {
		return fmt.Sprintf("event count product=%d naive=%d", len(m.board.Events()), len(n.events))
	}
	for i, ne := range n.events {
		pe := m.board.Get(int64(i + 1))
		if pe == nil {
			return fmt.Sprintf("product missing event %d", i+1)
		}
		if pe.Sev != ne.sev || pe.Rep != ne.rep || pe.Deadline != ne.deadline ||
			pe.State != ne.state || pe.Late != ne.late || pe.Receiver != ne.receiver ||
			pe.Mismatch != ne.mismatch || len(pe.Results) != ne.nresults {
			return fmt.Sprintf("event %d differs: product=sev%d rep%d ddl%d st%v late%v recv%s mis%d nres%d; naive=sev%d rep%d ddl%d st%v late%v recv%s mis%d nres%d",
				ne.id, pe.Sev, pe.Rep, pe.Deadline, pe.State, pe.Late, pe.Receiver, pe.Mismatch, len(pe.Results),
				ne.sev, ne.rep, ne.deadline, ne.state, ne.late, ne.receiver, ne.mismatch, ne.nresults)
		}
	}
	if !idsEq(m.Overdue(), n.overdue) {
		return fmt.Sprintf("overdue product=%v naive=%v", m.Overdue(), n.overdue)
	}
	return ""
}

var (
	dPatients = []string{"p1", "p2", "p3"}
	dWards    = []string{"W1", "W2"}
	dUsers    = []string{"n1", "n2", "d1"} // n1 W1护士 n2 W2护士 d1 W1医生
	dCodes    = []string{"K", "Na"}
	// 各项目危急候选值池（覆盖取等、分档边界两侧与正常值）
	dValues = map[string][]int64{
		// low=25 high=65 step=5
		"K": {24, 25, 26, 40, 64, 65, 66, 69, 70, 74, 75, 15, 100},
		// low=100 high=200 step=50
		"Na": {99, 100, 101, 150, 199, 200, 201, 249, 250, 300},
	}
)

func genOps(rng *rand.Rand) []op {
	// 确定性公共基线
	ops := []op{
		{kind: kAddTest, code: "K", low: 25, high: 65, step: 5},
		{kind: kAddTest, code: "Na", low: 100, high: 200, step: 50},
		{kind: kSetWard, patient: "p1", ward: "W1"},
		{kind: kSetWard, patient: "p2", ward: "W1"},
		{kind: kSetWard, patient: "p3", ward: "W2"},
		{kind: kGrant, u1: "n1", ward: "W1", role: Nurse},
		{kind: kGrant, u1: "n2", ward: "W2", role: Nurse},
		{kind: kGrant, u1: "d1", ward: "W1", role: Doctor},
	}
	var now int64
	const nops = 80
	for i := 0; i < nops; i++ {
		now += int64(rng.Intn(4)) // 非递减小步时钟
		r := rng.Float64()
		switch {
		case r < 0.04: // 偶发配置/非法参数
			ops = append(ops, op{kind: kAddTest, code: "K", low: 1, high: 2, step: 1}) // 重复
		case r < 0.42:
			pat := dPatients[rng.Intn(len(dPatients))]
			code := dCodes[rng.Intn(len(dCodes))]
			vals := dValues[code]
			v := vals[rng.Intn(len(vals))]
			if rng.Intn(10) == 0 {
				v = []int64{-2_000_000_000, 0}[rng.Intn(2)] // 偶发越界
			}
			ops = append(ops, op{kind: kResult, now: now, patient: pat, code: code, v: v})
		case r < 0.62:
			e := int64(1 + rng.Intn(4)) // 可能引用不存在事件
			recv := dUsers[rng.Intn(len(dUsers))]
			ops = append(ops, op{kind: kNotify, now: now, event: e, u1: "tech", u2: recv})
		case r < 0.82:
			e := int64(1 + rng.Intn(4))
			recv := dUsers[rng.Intn(len(dUsers))]
			// 回读值：有时用候选池值（含正确 rep 概率高），有时离谱值
			v := int64(rng.Intn(320)) - 60
			if rng.Intn(2) == 0 {
				v = dValues["K"][rng.Intn(len(dValues["K"]))]
			}
			ops = append(ops, op{kind: kReadBack, now: now, event: e, u2: recv, v: v})
		default:
			e := int64(1 + rng.Intn(4))
			doc := []string{"d1", "n1", "ghost"}[rng.Intn(3)]
			ops = append(ops, op{kind: kAct, now: now, event: e, u2: doc})
		}
	}
	return ops
}

func TestDifferentialRandom1500(t *testing.T) {
	const seeds = 1500
	for seed := int64(1); seed <= seeds; seed++ {
		rng := rand.New(rand.NewSource(seed))
		m, err := New(1+rng.Intn(8), 1+rng.Intn(6), 1+rng.Intn(4))
		if err != nil {
			t.Fatal(err)
		}
		n := newNaive(m.t[1], m.t[2], m.t[3])
		ops := genOps(rand.New(rand.NewSource(seed * 7919)))
		var logb []string
		for i, o := range ops {
			po := runProduct(m, o)
			no := n.run(o)
			line := fmt.Sprintf("[seed=%d step=%d] 输入: %s\n  判定依据: %s", seed, i, o.String(), no.why)
			if diff := cmpOutcome(po, no); diff != "" {
				t.Fatalf("输出分歧 @%s\n%s\n%s", line, diff, strings.Join(logb, "\n"))
			}
			if diff := cmpState(t, m, n); diff != "" {
				t.Fatalf("状态分歧 @%s\n%s\n%s", line, diff, strings.Join(logb, "\n"))
			}
			logb = append(logb, line+"\n  输出一致")
			if seed <= 3 {
				t.Log(line + " -> 输出/状态一致")
			}
		}
		if seed%200 == 0 {
			t.Logf("已完成 %d 组随机对照（最近一组 %d 步，逾期清单=%v）", seed, len(ops), n.overdue)
		}
	}
}

// 并发烟测：同一序列在多 goroutine 重放不同管理器不崩溃；
// 单管理器的并发等价性由互斥锁保证（整体串行），此处用 race 检测器跑。
func TestConcurrentSmoke(t *testing.T) {
	ops := genOps(rand.New(rand.NewSource(42)))
	var wg sync.WaitGroup
	for g := 0; g < 8; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			m, _ := New(10, 6, 3)
			for _, o := range ops {
				_ = runProduct(m, o)
			}
			_ = m.Overdue()
		}()
	}
	wg.Wait()
}
