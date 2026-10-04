package grade

import (
	"fmt"
	"math/big"
	"math/rand"
	"strings"
	"testing"

	"ontology/rma"
)

// simLine 是朴素模拟器的订单行状态。
type simLine struct {
	shipped, paid, a, b int64
}

type simAuth struct {
	order string
	exp   int64
	lines map[string]bool
	open  map[string]int64
	valid bool
	owe   int64
}

// simulator 独立于产品代码逐步重放规则：到期直接改 valid，
// 余量每次扫描全部有效授权单重算，128 位除法用 big.Int，保证交叉验证独立。
type simulator struct {
	wd, v, beta, fee int64
	clock            int64
	hasTime          bool
	shipAt           map[string]int64
	lines            map[string]map[string]*simLine
	auths            map[string]*simAuth
}

func newSimulator(wd, v, beta, fee int64) *simulator {
	return &simulator{
		wd: wd, v: v, beta: beta, fee: fee,
		shipAt: map[string]int64{},
		lines:  map[string]map[string]*simLine{},
		auths:  map[string]*simAuth{},
	}
}

// expire 落地所有 exp<=now 的有效单，返回被失效 id（含每单的 open 快照用于回滚）。
func (m *simulator) expire(now int64) []*simAuth {
	var gone []*simAuth
	for {
		var pick *simAuth
		for _, a := range m.auths {
			if !a.valid || a.exp > now {
				continue
			}
			if pick == nil || a.exp < pick.exp {
				pick = a
			}
		}
		if pick == nil {
			return gone
		}
		pick.valid = false
		gone = append(gone, pick)
	}
}

func restore(gone []*simAuth, fee int64) {
	for _, a := range gone {
		a.valid = true
		a.owe = fee
	}
}

func (m *simulator) reserved(order, line string) int64 {
	var r int64
	for _, a := range m.auths {
		if a.valid && a.order == order {
			r += a.open[line]
		}
	}
	return r
}

func (m *simulator) addOrder(id string, shipAt int64, lines map[string]rma.Line, now int64) error {
	if id == "" || shipAt < 0 || len(lines) < 1 || len(lines) > 100 {
		return rma.ErrInvalid
	}
	for _, ln := range lines {
		if ln.Shipped < 1 || ln.Shipped > 1e6 || ln.Paid < 0 || ln.Paid > 1e12 {
			return rma.ErrInvalid
		}
	}
	if m.hasTime && now < m.clock {
		return rma.ErrClockBack
	}
	if _, ok := m.lines[id]; ok {
		return rma.ErrConflict
	}
	m.lines[id] = map[string]*simLine{}
	for name, ln := range lines {
		m.lines[id][name] = &simLine{shipped: ln.Shipped, paid: ln.Paid}
	}
	m.shipAt[id] = shipAt
	m.clock, m.hasTime = now, true
	return nil
}

func (m *simulator) authorize(id, order string, items []rma.Item, now int64) (int64, error) {
	if id == "" || len(items) < 1 || len(items) > 100 {
		return 0, rma.ErrInvalid
	}
	seen := map[string]bool{}
	for _, it := range items {
		if it.Line == "" || it.Qty < 1 || seen[it.Line] {
			return 0, rma.ErrInvalid
		}
		seen[it.Line] = true
	}
	if m.hasTime && now < m.clock {
		return 0, rma.ErrClockBack
	}
	ols, ok := m.lines[order]
	if !ok {
		return 0, rma.ErrNotFound
	}
	for _, it := range items {
		if ols[it.Line] == nil {
			return 0, rma.ErrNotFound
		}
	}
	if _, ok := m.auths[id]; ok {
		return 0, rma.ErrConflict
	}
	if now-m.shipAt[order] > m.wd {
		return 0, rma.ErrWindow
	}
	gone := m.expire(now)
	for _, it := range items {
		ls := ols[it.Line]
		free := ls.shipped - ls.a - ls.b - m.reserved(order, it.Line)
		if it.Qty > free {
			restore(gone, m.fee)
			return 0, rma.ErrCapacity
		}
	}
	a := &simAuth{order: order, exp: now + m.v, valid: true,
		open: map[string]int64{}, lines: map[string]bool{}, owe: m.fee}
	for _, it := range items {
		a.open[it.Line] += it.Qty
		a.lines[it.Line] = true
	}
	m.auths[id] = a
	m.clock, m.hasTime = now, true
	return a.exp, nil
}

func (m *simulator) receive(id, order, line string, qty int64, g Grade, now int64) (Result, error) {
	if id == "" || order == "" || line == "" || qty < 1 || g < A || g > C {
		return Result{}, rma.ErrInvalid
	}
	if m.hasTime && now < m.clock {
		return Result{}, rma.ErrClockBack
	}
	a := m.auths[id]
	if a == nil {
		return Result{}, rma.ErrNotFound
	}
	if a.order != order || !a.lines[line] {
		return Result{}, rma.ErrNotFound
	}
	// 只读判定已过期（含未落地的到期），先不改状态。
	if !a.valid || a.exp <= now {
		return Result{}, rma.ErrExpired
	}
	if qty > a.open[line] {
		return Result{}, rma.ErrCapacity
	}
	// 全部校验通过：落地到期并作废其欠额。
	for _, x := range m.expire(now) {
		x.owe = 0 // 接受：到期单欠额作废
	}
	ls := m.lines[order][line]
	oldA, oldB := ls.a, ls.b
	a.open[line] -= qty
	if a.open[line] == 0 {
		delete(a.open, line)
	}
	var due int64
	switch g {
	case A:
		ls.a += qty
	case B:
		ls.b += qty
	case C:
		m.clock, m.hasTime = now, true
		return Result{}, nil
	}
	rOld := simRefund(ls.paid, ls.shipped, oldA, oldB, m.beta)
	rNew := simRefund(ls.paid, ls.shipped, ls.a, ls.b, m.beta)
	due = rNew - rOld
	d := due
	if d > a.owe {
		d = a.owe
	}
	a.owe -= d
	m.clock, m.hasTime = now, true
	return Result{Due: due, Fee: d, Paid: due - d}, nil
}

// simRefund 用 math/big 独立计算 R，与产品代码的手写 128 位除法互为对照。
func simRefund(paid, shipped, a, b, beta int64) int64 {
	num := new(big.Int).Mul(big.NewInt(paid),
		new(big.Int).Add(new(big.Int).Mul(big.NewInt(100), big.NewInt(a)),
			new(big.Int).Mul(big.NewInt(beta), big.NewInt(b))))
	den := new(big.Int).Mul(big.NewInt(100), big.NewInt(shipped))
	return new(big.Int).Quo(num, den).Int64()
}

type simOp struct {
	kind              int // 0=AddOrder 1=Authorize 2=Receive
	now               int64
	order, auth, line string
	qty               int64
	g                 Grade
	shipAt            int64
	lines             map[string]rma.Line
	items             []rma.Item
}

func errName(err error) string {
	switch {
	case err == nil:
		return "ok"
	case errorsIs(err, rma.ErrInvalid):
		return "Invalid"
	case errorsIs(err, rma.ErrClockBack):
		return "ClockBack"
	case errorsIs(err, rma.ErrNotFound):
		return "NotFound"
	case errorsIs(err, rma.ErrConflict):
		return "Conflict"
	case errorsIs(err, rma.ErrWindow):
		return "Window"
	case errorsIs(err, rma.ErrExpired):
		return "Expired"
	case errorsIs(err, rma.ErrCapacity):
		return "Capacity"
	default:
		return err.Error()
	}
}

func errorsIs(err, target error) bool {
	return err != nil && err.Error() == target.Error()
}

func TestRandomAgainstNaive(t *testing.T) {
	t.Logf("对照 1500 组随机操作序列：朴素模拟(big.Int) vs 产品实现；-v 查看逐步输入/输出/判定依据")
	const groups = 1500
	rngGlobal := rand.New(rand.NewSource(20261004))
	for group := 0; group < groups; group++ {
		seed := rngGlobal.Int63()
		rng := rand.New(rand.NewSource(seed))
		wd := int64(1 + rng.Intn(40))
		v := int64(1 + rng.Intn(15))
		beta := int64(rng.Intn(101))
		fee := int64(rng.Intn(300))

		_, real := newSystem(t, wd, v, beta, fee)
		sim := newSimulator(wd, v, beta, fee)

		// 预生成订单池（含偶尔的重复 AddOrder 与缺失引用）
		type lineRef struct{ order, line string }
		var refs []lineRef
		nOrders := 1 + rng.Intn(3)
		var ops []simOp
		baseNow := int64(0)
		for oi := 0; oi < nOrders; oi++ {
			oid := fmt.Sprintf("O%d", oi)
			nLines := 1 + rng.Intn(4)
			lines := map[string]rma.Line{}
			for li := 0; li < nLines; li++ {
				lid := fmt.Sprintf("L%d", li)
				sh := int64(1 + rng.Intn(6))
				paid := rng.Int63n(5000)
				if rng.Intn(20) == 0 {
					paid = 1_000_000_000_000 // 偶尔打满 paid 上限
					sh = 1_000_000
				}
				lines[lid] = rma.Line{Shipped: sh, Paid: paid}
				refs = append(refs, lineRef{oid, lid})
			}
			ops = append(ops, simOp{
				kind: 0, now: baseNow, order: oid,
				shipAt: rng.Int63n(5), lines: lines,
			})
		}
		baseNow++

		const steps = 60
		authSeq := 0
		var liveAuths []string
		for step := 0; step < steps; step++ {
			// 时间单调推进，偶发回退/相等
			switch rng.Intn(8) {
			case 0:
				if baseNow > 2 {
					baseNow -= 1 + int64(rng.Intn(2))
				}
			case 1:
				// 原地不动（相等 now）
			default:
				baseNow += int64(rng.Intn(4))
			}
			op := simOp{now: baseNow}
			if len(refs) == 0 || rng.Intn(2) == 0 {
				// Authorize
				op.kind = 1
				if rng.Intn(10) == 0 {
					op.order = "GHOST" // 订单不存在
				} else {
					r := refs[rng.Intn(len(refs))]
					op.order = r.order
				}
				nItems := 1 + rng.Intn(3)
				seen := map[string]bool{}
				for i := 0; i < nItems; i++ {
					var ln string
					if op.order != "GHOST" && rng.Intn(8) != 0 {
						r := refs[rng.Intn(len(refs))]
						for r.order != op.order {
							r = refs[rng.Intn(len(refs))]
						}
						ln = r.line
					} else {
						ln = fmt.Sprintf("X%d", rng.Intn(3))
					}
					if seen[ln] && rng.Intn(2) == 0 {
						continue
					}
					seen[ln] = true
					op.items = append(op.items, rma.Item{Line: ln, Qty: int64(1 + rng.Intn(4))})
				}
				if len(op.items) == 0 {
					op.items = []rma.Item{{Line: "L0", Qty: 1}}
				}
				if rng.Intn(6) == 0 && len(liveAuths) > 0 {
					op.auth = liveAuths[rng.Intn(len(liveAuths))] // 撞号冲突
				} else {
					op.auth = fmt.Sprintf("A%d", authSeq)
					authSeq++
				}
			} else {
				// Receive
				op.kind = 2
				if rng.Intn(10) == 0 || len(liveAuths) == 0 {
					op.auth = fmt.Sprintf("GHOST%d", rng.Intn(5))
					r := refs[rng.Intn(len(refs))]
					op.order, op.line = r.order, r.line
				} else {
					op.auth = liveAuths[rng.Intn(len(liveAuths))]
				}
				r := refs[rng.Intn(len(refs))]
				op.order, op.line = r.order, r.line
				op.qty = int64(1 + rng.Intn(4))
				op.g = Grade(1 + rng.Intn(3))
			}
			ops = append(ops, op)
			// 预登记“可能存在”的授权号供后续撞号/收货使用（产品与模拟各自判存在性）
			if op.kind == 1 {
				liveAuths = append(liveAuths, op.auth)
				if len(liveAuths) > 40 {
					liveAuths = liveAuths[1:]
				}
			}
		}

		var log strings.Builder
		fmt.Fprintf(&log, "group %d seed=%d Wd=%d V=%d beta=%d fee=%d\n",
			group, seed, wd, v, beta, fee)

		for i, o := range ops {
			var rExp int64
			var rErr, sErr error
			var rRes, sRes Result
			switch o.kind {
			case 0:
				rErr = real.store.AddOrder(o.order, o.shipAt, o.lines, o.now)
				sErr = sim.addOrder(o.order, o.shipAt, o.lines, o.now)
				fmt.Fprintf(&log, "[%02d] t=%d AddOrder(%s shipAt=%d lines=%d) -> %s\n",
					i, o.now, o.order, o.shipAt, len(o.lines), errName(rErr))
			case 1:
				rExp, rErr = real.Authorize(o.auth, o.order, o.items, o.now)
				var sExp int64
				sExp, sErr = sim.authorize(o.auth, o.order, o.items, o.now)
				if rErr == nil && rExp != sExp {
					t.Fatalf("group %d op %d exp mismatch real=%d sim=%d\n%s", group, i, rExp, sExp, log.String())
				}
				fmt.Fprintf(&log, "[%02d] t=%d Authorize(%s on %s items=%v) -> %s exp=%d\n",
					i, o.now, o.auth, o.order, o.items, errName(rErr), rExp)
			case 2:
				rRes, rErr = real.Receive(o.auth, o.order, o.line, o.qty, o.g, o.now)
				sRes, sErr = sim.receive(o.auth, o.order, o.line, o.qty, o.g, o.now)
				gradeName := []string{"-", "A", "B", "C"}[o.g]
				fmt.Fprintf(&log, "[%02d] t=%d Receive(%s %s/%s qty=%d %s) -> %s %+v\n",
					i, o.now, o.auth, o.order, o.line, o.qty, gradeName,
					errName(rErr), rRes)
			}
			if errName(rErr) != errName(sErr) {
				t.Fatalf("group %d op %d 错误不一致 real=%s sim=%s\n日志:\n%s",
					group, i, errName(rErr), errName(sErr), log.String())
			}
			if rErr == nil && o.kind == 2 && rRes != sRes {
				t.Fatalf("group %d op %d 退款不一致 real=%+v sim=%+v\n日志:\n%s",
					group, i, rRes, sRes, log.String())
			}
		}

		// 终态不变量逐行核对（产品 vs 模拟）。
		for _, oref := range refs {
			ri, ok1 := real.store.InspectLine(oref.order, oref.line)
			sl := sim.lines[oref.order][oref.line]
			if !ok1 || sl == nil {
				continue
			}
			if ri.A != sl.a || ri.B != sl.b || ri.Reserved != sim.reserved(oref.order, oref.line) {
				t.Fatalf("group %d 行 %s/%s 终态不一致 real={a=%d b=%d res=%d} sim={a=%d b=%d res=%d}\n%s",
					group, oref.order, oref.line, ri.A, ri.B, ri.Reserved,
					sl.a, sl.b, sim.reserved(oref.order, oref.line), log.String())
			}
			if ri.A+ri.B+ri.Reserved > ri.Shipped {
				t.Fatalf("group %d 不变量被破坏: %+v", group, ri)
			}
			rTotal := simRefund(ri.Paid, ri.Shipped, ri.A, ri.B, beta)
			if rTotal > ri.Paid {
				t.Fatalf("group %d 累计应退 %d > paid %d", group, rTotal, ri.Paid)
			}
		}
		// 每组首末打印一次判定依据（-v 下可见）；失败时上方已打印完整日志。
		if group == 0 || group == groups-1 {
			t.Logf("group %d 全部 %d 步判定一致，终态不变量成立\n%s", group, len(ops), log.String())
		}
	}
}
