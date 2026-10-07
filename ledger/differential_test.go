package ledger_test

import (
	"errors"
	"fmt"
	"math/rand"
	"os"
	"reflect"
	"sort"
	"testing"

	"ontology/ledger"
	"ontology/naive"
)

// ---- 随机操作生成 ----

type diffOp struct {
	desc string
	// apply 对两个实现分别施加同一操作。
	applySys   func(s *ledger.System) (any, error)
	applyNaive func(m *naive.Model) (any, error)
	// onAccept 在操作被接受后回调，用于维护生成器的跟踪状态。
	onAccept func()
}

type genState struct {
	rng         *rand.Rand
	now         int64
	drugs       []string
	depts       []string
	persons     []string
	reviewers   []string
	batchSeq    int
	slipSeq     int
	openSlips   map[string]int      // 未结清单据 -> 领出量
	discrepSlip []string            // 差额待处理单据
	batches     map[string][]string // 药品 -> 已接受的批号
	authWin     map[string][2]int64 // 人员 -> 当前授权窗口（生成器跟踪）
}

func newGenState(seed int64) *genState {
	return &genState{
		rng:       rand.New(rand.NewSource(seed)),
		now:       int64(seed % 1000),
		drugs:     []string{"d0", "d1", "d2"},
		depts:     []string{"k0", "k1", "k2"},
		persons:   []string{"p0", "p1", "p2"},
		reviewers: []string{"r0", "r1", "r2", "r3"},
		openSlips: map[string]int{},
		batches:   map[string][]string{},
		authWin:   map[string][2]int64{},
	}
}

func (g *genState) pick(ss []string) string { return ss[g.rng.Intn(len(ss))] }

// advance 大部分时间前进，偶尔原地，偶尔回退（应被拒绝）。
func (g *genState) advance() {
	switch r := g.rng.Intn(100); {
	case r < 5:
		g.now -= int64(g.rng.Intn(200) + 1) // 时钟回退
		if g.now < 0 {
			g.now = 0
		}
	case r < 25:
		// 原地不动
	case r < 45:
		g.now += int64(g.rng.Intn(200000)) // 可能跨过 24 小时期限
	default:
		g.now += int64(g.rng.Intn(600))
	}
	if g.now > 1_000_000_000 {
		g.now = 1_000_000_000
	}
}

// reviewers 生成复核人组合：通常合法，偶尔触发各类复核人错误。
func (g *genState) reviewerPair(applicant string) []string {
	switch g.rng.Intn(20) {
	case 0:
		return []string{g.pick(g.reviewers)} // 人数不足
	case 1:
		r := g.pick(g.reviewers)
		return []string{r, r} // 同一人
	case 2:
		if applicant != "" {
			return []string{applicant, g.pick(g.reviewers)} // 申请人本人
		}
	}
	// 多数情况下优先挑选当前授权有效的复核人
	if g.rng.Intn(4) > 0 {
		valid := make([]string, 0, len(g.reviewers))
		for _, r := range g.reviewers {
			if w, ok := g.authWin[r]; ok && w[0] <= g.now && g.now < w[1] {
				valid = append(valid, r)
			}
		}
		if len(valid) >= 2 {
			g.rng.Shuffle(len(valid), func(i, j int) { valid[i], valid[j] = valid[j], valid[i] })
			return valid[:2]
		}
	}
	rs := append([]string(nil), g.reviewers...)
	g.rng.Shuffle(len(rs), func(i, j int) { rs[i], rs[j] = rs[j], rs[i] })
	return rs[:2]
}

func (g *genState) maybeBadQty(q int) int {
	switch g.rng.Intn(30) {
	case 0:
		return 0
	case 1:
		return 1_000_001
	case 2:
		return -1
	}
	return q
}

func (g *genState) maybeBadName(s string) string {
	if g.rng.Intn(40) == 0 {
		return ""
	}
	return s
}

func (g *genState) genGrant() diffOp {
	from := g.now - int64(g.rng.Intn(3))
	if from < 0 {
		from = 0
	}
	var until int64
	switch g.rng.Intn(6) {
	case 0:
		until = g.now + int64(g.rng.Intn(2000)+1) // 短窗口，考验失效边界
	case 1:
		until = g.now + int64(g.rng.Intn(200000)+1000) // 中等窗口
	default:
		until = 1_000_000_000 // 长期有效
	}
	if g.rng.Intn(20) == 0 {
		until = from // 非法：空窗口
	}
	if g.rng.Intn(4) == 0 {
		// 一次性授权全部复核人
		now, f, u := g.now, from, until
		return diffOp{
			desc: fmt.Sprintf("GrantAuthAll(now=%d from=%d until=%d)", now, f, u),
			applySys: func(s *ledger.System) (any, error) {
				for _, p := range g.reviewers {
					if err := s.GrantAuth(now, p, f, u); err != nil {
						return nil, err
					}
				}
				return nil, nil
			},
			applyNaive: func(m *naive.Model) (any, error) {
				for _, p := range g.reviewers {
					if err := m.GrantAuth(now, p, f, u); err != nil {
						return nil, err
					}
				}
				return nil, nil
			},
			onAccept: func() {
				for _, p := range g.reviewers {
					g.authWin[p] = [2]int64{f, u}
				}
			},
		}
	}
	p := g.pick(g.reviewers)
	now := g.now
	return diffOp{
		desc:       fmt.Sprintf("GrantAuth(now=%d person=%s from=%d until=%d)", now, p, from, until),
		applySys:   func(s *ledger.System) (any, error) { return nil, s.GrantAuth(now, p, from, until) },
		applyNaive: func(m *naive.Model) (any, error) { return nil, m.GrantAuth(now, p, from, until) },
		onAccept:   func() { g.authWin[p] = [2]int64{from, until} },
	}
}

func (g *genState) genRevoke() diffOp {
	p := g.pick(g.reviewers)
	if g.rng.Intn(15) == 0 {
		p = "ghost"
	}
	now := g.now
	return diffOp{
		desc:       fmt.Sprintf("RevokeAuth(now=%d person=%s)", now, p),
		applySys:   func(s *ledger.System) (any, error) { return nil, s.RevokeAuth(now, p) },
		applyNaive: func(m *naive.Model) (any, error) { return nil, m.RevokeAuth(now, p) },
		onAccept: func() {
			if w, ok := g.authWin[p]; ok && now < w[1] {
				g.authWin[p] = [2]int64{w[0], now}
			}
		},
	}
}

func (g *genState) genInbound() diffOp {
	drug := g.maybeBadName(g.pick(g.drugs))
	var batch string
	if g.rng.Intn(10) == 0 && g.batchSeq > 0 {
		batch = fmt.Sprintf("b%d", g.rng.Intn(g.batchSeq)) // 可能重复
	} else {
		batch = fmt.Sprintf("b%d", g.batchSeq)
		g.batchSeq++
	}
	qty := g.maybeBadQty(1 + g.rng.Intn(50))
	var expiry int64
	switch g.rng.Intn(10) {
	case 0:
		expiry = g.now // 恰等于 now：已过期
	case 1, 2:
		expiry = g.now - int64(g.rng.Intn(100)) // 已过期
		if expiry < 0 {
			expiry = 0
		}
	default:
		expiry = g.now + int64(g.rng.Intn(300000)) + 1
	}
	now := g.now
	return diffOp{
		desc:       fmt.Sprintf("Inbound(now=%d drug=%s batch=%s qty=%d expiry=%d)", now, drug, batch, qty, expiry),
		applySys:   func(s *ledger.System) (any, error) { return nil, s.Inbound(now, drug, batch, qty, expiry) },
		applyNaive: func(m *naive.Model) (any, error) { return nil, m.Inbound(now, drug, batch, qty, expiry) },
		onAccept: func() {
			if drug != "" {
				g.batches[drug] = append(g.batches[drug], batch)
			}
		},
	}
}

func (g *genState) genWithdraw() diffOp {
	dept := g.maybeBadName(g.pick(g.depts))
	applicant := g.pick(g.persons)
	drug := g.pick(g.drugs)
	if g.rng.Intn(25) == 0 {
		drug = "nodrug"
	}
	var id string
	if g.rng.Intn(12) == 0 && g.slipSeq > 0 {
		id = fmt.Sprintf("s%d", g.rng.Intn(g.slipSeq)) // 可能重复
	} else {
		id = fmt.Sprintf("s%d", g.slipSeq)
		g.slipSeq++
	}
	qty := 1 + g.rng.Intn(60)
	if g.rng.Intn(10) == 0 {
		qty = 1 + g.rng.Intn(600) // 大数量，考验库存不足
	}
	qty = g.maybeBadQty(qty)
	reviewers := g.reviewerPair(applicant)
	now := g.now
	return diffOp{
		desc: fmt.Sprintf("Withdraw(now=%d slip=%s dept=%s app=%s drug=%s qty=%d rev=%v)",
			now, id, dept, applicant, drug, qty, reviewers),
		applySys:   func(s *ledger.System) (any, error) { return s.Withdraw(now, id, dept, applicant, drug, qty, reviewers) },
		applyNaive: func(m *naive.Model) (any, error) { return m.Withdraw(now, id, dept, applicant, drug, qty, reviewers) },
		onAccept:   func() { g.openSlips[id] = qty },
	}
}

func (g *genState) genSettle() diffOp {
	var id string
	qty := 10
	if len(g.openSlips) > 0 && g.rng.Intn(10) > 0 {
		keys := make([]string, 0, len(g.openSlips))
		for k := range g.openSlips {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		id = keys[g.rng.Intn(len(keys))]
		qty = g.openSlips[id]
	} else {
		id = fmt.Sprintf("s%d", g.rng.Intn(g.slipSeq+3)) // 可能不存在或状态不符
	}
	var used, returned, residual int
	switch g.rng.Intn(5) {
	case 0, 1: // 恰好结清
		used = g.rng.Intn(qty + 1)
		returned = g.rng.Intn(qty - used + 1)
		residual = qty - used - returned
	case 2: // 超出
		used = qty + 1
		returned = g.rng.Intn(3)
		residual = 0
	default: // 可能不足（差额）
		used = g.rng.Intn(qty + 1)
		returned = g.rng.Intn(qty + 1)
		residual = g.rng.Intn(qty + 1)
	}
	now := g.now
	return diffOp{
		desc:       fmt.Sprintf("Settle(now=%d slip=%s used=%d returned=%d residual=%d)", now, id, used, returned, residual),
		applySys:   func(s *ledger.System) (any, error) { return nil, s.Settle(now, id, used, returned, residual) },
		applyNaive: func(m *naive.Model) (any, error) { return nil, m.Settle(now, id, used, returned, residual) },
		onAccept: func() {
			delete(g.openSlips, id)
			if used+returned+residual < qty {
				g.discrepSlip = append(g.discrepSlip, id)
			}
		},
	}
}

func (g *genState) genResolve() diffOp {
	var id string
	if len(g.discrepSlip) > 0 && g.rng.Intn(10) > 0 {
		id = g.discrepSlip[g.rng.Intn(len(g.discrepSlip))]
	} else {
		id = fmt.Sprintf("s%d", g.rng.Intn(g.slipSeq+3))
	}
	reviewers := g.reviewerPair("")
	now := g.now
	return diffOp{
		desc:       fmt.Sprintf("ResolveDiscrepancy(now=%d slip=%s rev=%v)", now, id, reviewers),
		applySys:   func(s *ledger.System) (any, error) { return nil, s.ResolveDiscrepancy(now, id, reviewers) },
		applyNaive: func(m *naive.Model) (any, error) { return nil, m.ResolveDiscrepancy(now, id, reviewers) },
		onAccept: func() {
			for i, v := range g.discrepSlip {
				if v == id {
					g.discrepSlip = append(g.discrepSlip[:i], g.discrepSlip[i+1:]...)
					break
				}
			}
		},
	}
}

func (g *genState) genDestroy() diffOp {
	drug := g.pick(g.drugs)
	batch := "b0"
	if bs := g.batches[drug]; len(bs) > 0 && g.rng.Intn(10) > 0 {
		batch = bs[g.rng.Intn(len(bs))]
	} else if g.rng.Intn(3) == 0 {
		batch = "nobatch"
	}
	qty := g.maybeBadQty(1 + g.rng.Intn(60))
	reviewers := g.reviewerPair("")
	now := g.now
	return diffOp{
		desc:       fmt.Sprintf("Destroy(now=%d drug=%s batch=%s qty=%d rev=%v)", now, drug, batch, qty, reviewers),
		applySys:   func(s *ledger.System) (any, error) { return nil, s.Destroy(now, drug, batch, qty, reviewers) },
		applyNaive: func(m *naive.Model) (any, error) { return nil, m.Destroy(now, drug, batch, qty, reviewers) },
	}
}

func (g *genState) next() diffOp {
	g.advance()
	switch r := g.rng.Intn(100); {
	case r < 14:
		return g.genGrant()
	case r < 17:
		return g.genRevoke()
	case r < 29:
		return g.genInbound()
	case r < 50:
		return g.genWithdraw()
	case r < 70:
		return g.genSettle()
	case r < 78:
		return g.genResolve()
	default:
		return g.genDestroy()
	}
}

// ---- 对照执行 ----

func errKindOf(err error) (ledger.ErrKind, bool) {
	if err == nil {
		return 0, false
	}
	var oe *ledger.OpError
	if errors.As(err, &oe) {
		return oe.Kind, true
	}
	return 0, false
}

func resultString(res any, err error) string {
	if err != nil {
		return fmt.Sprintf("拒绝 %v", err)
	}
	if lines, ok := res.([]ledger.BatchLine); ok {
		return fmt.Sprintf("接受 分出=%v", lines)
	}
	return "接受"
}

// digest 生成关键状态摘要，作为判定依据写入日志。
func digest(snap ledger.Snapshot) string {
	var b []byte
	drugs := make([]string, 0, len(snap.Drugs))
	for name := range snap.Drugs {
		drugs = append(drugs, name)
	}
	sort.Strings(drugs)
	for _, name := range drugs {
		d := snap.Drugs[name]
		b = fmt.Appendf(b, " %s(book=%d,avail=%d)", name, d.BookTotal, d.Available)
	}
	depts := make([]string, 0, len(snap.Depts))
	for name := range snap.Depts {
		depts = append(depts, name)
	}
	sort.Strings(depts)
	for _, name := range depts {
		d := snap.Depts[name]
		b = fmt.Appendf(b, " %s(locked=%v,open=%d,disc=%d)", name, d.Locked, d.OpenCount, d.Discrepancy)
	}
	return string(b)
}

// TestDifferentialRandom 与独立朴素模型对照不少于 1500 组随机操作序列，
// 每步打印输入、输出与判定依据到 testdata/differential.log。
func TestDifferentialRandom(t *testing.T) {
	const sequences = 1500
	const opsPerSeq = 30
	if err := os.MkdirAll("testdata", 0o755); err != nil {
		t.Fatal(err)
	}
	logPath := "testdata/differential.log"
	logFile, err := os.Create(logPath)
	if err != nil {
		t.Fatal(err)
	}
	defer logFile.Close()

	accepted, rejected := 0, 0
	for seq := 0; seq < sequences; seq++ {
		g := newGenState(int64(seq)*7919 + 1)
		sys := ledger.NewSystem()
		model := naive.New()
		fmt.Fprintf(logFile, "=== 序列 %d ===\n", seq)
		step := 0
		runStep := func(op diffOp) {
			resSys, errSys := op.applySys(sys)
			resNaive, errNaive := op.applyNaive(model)

			kindSys, isErrSys := errKindOf(errSys)
			kindNaive, isErrNaive := errKindOf(errNaive)
			consistent := isErrSys == isErrNaive
			if consistent && isErrSys {
				consistent = kindSys == kindNaive
			}
			if consistent && !isErrSys {
				consistent = reflect.DeepEqual(resSys, resNaive)
			}

			if errSys == nil {
				accepted++
				if op.onAccept != nil {
					op.onAccept()
				}
			} else {
				rejected++
			}

			now := sys.Now()
			snapSys := sys.Snapshot(now)
			snapNaive := model.Snapshot(now)
			snapOK := reflect.DeepEqual(snapSys, snapNaive)
			invariantOK := sys.InvariantOK()

			fmt.Fprintf(logFile, "seq=%d step=%d %s\n", seq, step, op.desc)
			fmt.Fprintf(logFile, "  系统输出: %s\n", resultString(resSys, errSys))
			fmt.Fprintf(logFile, "  朴素输出: %s\n", resultString(resNaive, errNaive))
			fmt.Fprintf(logFile, "  判定: 结果一致=%v 快照一致=%v 不变式=%v |%s\n",
				consistent, snapOK, invariantOK, digest(snapSys))

			if !consistent {
				t.Fatalf("序列 %d 步骤 %d 结果不一致\nop=%s\nsys=%v\nnaive=%v", seq, step, op.desc, resultString(resSys, errSys), resultString(resNaive, errNaive))
			}
			if !snapOK {
				t.Fatalf("序列 %d 步骤 %d 快照不一致\nop=%s\nsys=%+v\nnaive=%+v", seq, step, op.desc, snapSys, snapNaive)
			}
			if !invariantOK {
				t.Fatalf("序列 %d 步骤 %d 账面不变式被破坏\nop=%s", seq, step, op.desc)
			}
			step++
		}
		// 建账阶段：授权全部复核人，并为每种药品入两个批次（一长一短效期）。
		setup := []diffOp{
			{
				desc: "GrantAuthAll(now=0 from=0 until=1000000000)",
				applySys: func(s *ledger.System) (any, error) {
					for _, p := range g.reviewers {
						if err := s.GrantAuth(0, p, 0, 1_000_000_000); err != nil {
							return nil, err
						}
					}
					return nil, nil
				},
				applyNaive: func(m *naive.Model) (any, error) {
					for _, p := range g.reviewers {
						if err := m.GrantAuth(0, p, 0, 1_000_000_000); err != nil {
							return nil, err
						}
					}
					return nil, nil
				},
				onAccept: func() {
					for _, p := range g.reviewers {
						g.authWin[p] = [2]int64{0, 1_000_000_000}
					}
				},
			},
		}
		for _, drug := range g.drugs {
			for i, expiry := range []int64{1_000_000_000, 100_000 + int64(g.rng.Intn(200_000))} {
				drug, expiry := drug, expiry
				batch := fmt.Sprintf("seed%d", i)
				setup = append(setup, diffOp{
					desc:       fmt.Sprintf("Inbound(now=0 drug=%s batch=%s qty=200 expiry=%d)", drug, batch, expiry),
					applySys:   func(s *ledger.System) (any, error) { return nil, s.Inbound(0, drug, batch, 200, expiry) },
					applyNaive: func(m *naive.Model) (any, error) { return nil, m.Inbound(0, drug, batch, 200, expiry) },
					onAccept:   func() { g.batches[drug] = append(g.batches[drug], batch) },
				})
			}
		}
		for _, op := range setup {
			runStep(op)
		}
		for step < opsPerSeq {
			runStep(g.next())
		}
	}
	t.Logf("差分对照完成: %d 序列 x %d 操作，接受 %d，拒绝 %d；日志: %s",
		sequences, opsPerSeq, accepted, rejected, logPath)
}
