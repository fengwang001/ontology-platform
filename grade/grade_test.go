package grade_test

import (
	"errors"
	"fmt"
	"math/rand"
	"sync"
	"testing"

	"ontology/grade"
	"ontology/refund"
	"ontology/rma"
)

func newStack(t *testing.T, window, valid, beta, fee int64) (*rma.RMA, *grade.Service) {
	t.Helper()
	r, err := rma.New(window, valid)
	if err != nil {
		t.Fatal(err)
	}
	calc, err := refund.New(beta)
	if err != nil {
		t.Fatal(err)
	}
	svc, err := grade.New(r, fee, calc)
	if err != nil {
		t.Fatal(err)
	}
	return r, svc
}

func TestSpecExample(t *testing.T) {
	r, svc := newStack(t, 30, 10, 80, 50)
	if err := r.AddOrder("O", 0, []rma.Line{{ID: 1, Shipped: 3, Paid: 1000}}, 0); err != nil {
		t.Fatal(err)
	}
	exp, err := r.Authorize("R1", "O", []rma.Item{{Line: 1, Qty: 2}}, 5)
	if err != nil || exp != 15 {
		t.Fatalf("R1 exp=%d err=%v", exp, err)
	}
	if _, err := r.Authorize("R2", "O", []rma.Item{{Line: 1, Qty: 2}}, 6); !errors.Is(err, rma.ErrCapacity) {
		t.Fatalf("R2 err=%v want capacity", err)
	}

	got, err := svc.Receive("R1", 1, 1, grade.A, 7)
	if err != nil || got != (grade.Result{Due: 333, Deducted: 50, Paid: 283}) {
		t.Fatalf("A: %+v err=%v", got, err)
	}
	got, err = svc.Receive("R1", 1, 1, grade.B, 8)
	if err != nil || got != (grade.Result{Due: 267, Deducted: 0, Paid: 267}) {
		t.Fatalf("B: %+v err=%v (累计取整 267，非逐件 266)", got, err)
	}

	if _, err := r.Authorize("R3", "O", []rma.Item{{Line: 1, Qty: 1}}, 16); err != nil {
		t.Fatalf("R3: %v", err)
	}
	if _, err := svc.Receive("R3", 1, 1, grade.A, 26); !errors.Is(err, rma.ErrExpired) {
		t.Fatalf("at exp: err=%v want expired", err)
	}
	if _, err := r.Authorize("R4", "O", []rma.Item{{Line: 1, Qty: 1}}, 30); err != nil {
		t.Fatalf("R4 boundary window: %v", err)
	}
	got, err = svc.Receive("R4", 1, 1, grade.C, 30)
	if err != nil || got != (grade.Result{}) {
		t.Fatalf("C: %+v err=%v, want zero", got, err)
	}
	if _, err := r.Authorize("R5", "O", []rma.Item{{Line: 1, Qty: 1}}, 30); err != nil {
		t.Fatalf("R5 after C freed: %v", err)
	}
	got, err = svc.Receive("R5", 1, 1, grade.A, 30)
	if err != nil || got != (grade.Result{Due: 333, Deducted: 50, Paid: 283}) {
		t.Fatalf("R5 A: %+v err=%v, want 333/50/283", got, err)
	}
	if _, err := r.Authorize("RX", "O", []rma.Item{{Line: 1, Qty: 1}}, 31); !errors.Is(err, rma.ErrWindow) {
		t.Fatalf("t=31: err=%v want window", err)
	}
}

func TestFeeInstallments(t *testing.T) {
	// 应退小于欠额：fee=1000，每次应退 100，分多次扣完后实付才为正。
	r, svc := newStack(t, 30, 100, 0, 1000)
	_ = r.AddOrder("O", 0, []rma.Line{{ID: 1, Shipped: 10, Paid: 1000}}, 0)
	_, _ = r.Authorize("R", "O", []rma.Item{{Line: 1, Qty: 10}}, 1)
	var totalDed int64
	for i := 0; i < 10; i++ {
		res, err := svc.Receive("R", 1, 1, grade.A, 2)
		if err != nil {
			t.Fatal(err)
		}
		totalDed += res.Deducted
		if res.Deducted+res.Paid != res.Due {
			t.Fatalf("due != deducted+paid: %+v", res)
		}
	}
	if totalDed != 1000 {
		t.Fatalf("total deducted=%d want fee 1000", totalDed)
	}
	// 再收已无未收，超收报错。
	if _, err := svc.Receive("R", 1, 1, grade.A, 3); !errors.Is(err, rma.ErrCapacity) {
		t.Fatalf("over: %v", err)
	}
}

func TestZeroDueNoFee(t *testing.T) {
	r, svc := newStack(t, 30, 100, 80, 50)
	_ = r.AddOrder("O", 0, []rma.Line{{ID: 1, Shipped: 100, Paid: 1}}, 0)
	_, _ = r.Authorize("R", "O", []rma.Item{{Line: 1, Qty: 1}}, 1)
	// floor(1*100/10000)=0：应退 0，不扣费，欠额仍在。
	res, err := svc.Receive("R", 1, 1, grade.B, 2)
	if err != nil || res != (grade.Result{}) {
		t.Fatalf("%+v err=%v want zero", res, err)
	}
}

func TestReceiveRejectPriorities(t *testing.T) {
	r, svc := newStack(t, 30, 10, 80, 50)
	_ = r.AddOrder("O", 0, []rma.Line{{ID: 1, Shipped: 1, Paid: 10}, {ID: 2, Shipped: 1, Paid: 10}}, 0)
	_, _ = r.Authorize("R", "O", []rma.Item{{Line: 1, Qty: 1}}, 5)
	if _, err := svc.Receive("R", 1, 0, grade.A, 6); !errors.Is(err, grade.ErrInvalid) {
		t.Fatalf("invalid grade args: %v", err)
	}
	if _, err := svc.Receive("R", 1, 1, grade.Grade(9), 6); !errors.Is(err, grade.ErrInvalid) {
		t.Fatalf("invalid grade enum: %v", err)
	}
	if _, err := svc.Receive("R", 1, 1, grade.A, 3); !errors.Is(err, rma.ErrClock) {
		t.Fatalf("clock first: %v", err)
	}
	if _, err := svc.Receive("NOPE", 1, 1, grade.A, 6); !errors.Is(err, rma.ErrNotFound) {
		t.Fatalf("missing rma: %v", err)
	}
	if _, err := svc.Receive("R", 2, 1, grade.A, 6); !errors.Is(err, rma.ErrNotFound) {
		t.Fatalf("line not in rma: %v", err)
	}
	if _, err := svc.Receive("R", 1, 1, grade.A, 15); !errors.Is(err, rma.ErrExpired) {
		t.Fatalf("expired: %v", err)
	}
}

func TestConcurrent(t *testing.T) {
	r, svc := newStack(t, 10_000, 10_000, 80, 5)
	const n = 40
	var lines []rma.Line
	for i := 0; i < n; i++ {
		lines = append(lines, rma.Line{ID: rma.LineID(i + 1), Shipped: 1, Paid: 100})
	}
	if err := r.AddOrder("O", 0, lines, 0); err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	errs := make(chan error, n)
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			id := rma.RMAID(fmt.Sprintf("R%d", i))
			lid := rma.LineID(i + 1)
			if _, err := r.Authorize(id, "O", []rma.Item{{Line: lid, Qty: 1}}, 1); err != nil {
				errs <- err
				return
			}
			if _, err := svc.Receive(id, lid, 1, grade.A, 1); err != nil {
				errs <- err
			}
		}(i)
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Fatal(err)
	}
}

// 朴素模拟：逐步独立维护全部状态，作为实现的对照基线。
type naiveLine struct {
	shipped, paid, a, b int64
}

type naiveAuth struct {
	order string
	exp   int64
	items map[int64]int64 // 未收
	owes  int64
}

type naive struct {
	wd, V, beta, fee int64
	lastNow          int64
	shipAt           map[string]int64
	lines            map[string]map[int64]*naiveLine
	auths            map[string]*naiveAuth
	calc             *refund.Calculator
}

func newNaive(t *testing.T, wd, V, beta, fee int64) *naive {
	t.Helper()
	c, err := refund.New(beta)
	if err != nil {
		t.Fatal(err)
	}
	return &naive{
		wd: wd, V: V, beta: beta, fee: fee,
		shipAt: map[string]int64{},
		lines:  map[string]map[int64]*naiveLine{},
		auths:  map[string]*naiveAuth{},
		calc:   c,
	}
}

func (n *naive) expire(now int64) {
	for id, a := range n.auths {
		if a.exp <= now {
			delete(n.auths, id)
		}
	}
}

func (n *naive) cum(l *naiveLine) int64 {
	return n.calc.Cumulative(l.paid, l.shipped, l.a, l.b)
}

type simOp struct {
	kind                string // add / auth / recv
	now                 int64
	order, rma          string
	line, qty           int64
	g                   grade.Grade
	addShipped, addPaid int64
	shipAt              int64
}

func runNaive(n *naive, op simOp) (due, ded, paid int64, exp int64, err error) {
	// 参数校验（与正式实现同一规则）。
	switch op.kind {
	case "add":
		if op.order == "" || op.now < 0 || op.addShipped < 1 || op.addShipped > 1e6 ||
			op.addPaid < 0 || op.addPaid > 1e12 {
			return 0, 0, 0, 0, rma.ErrInvalid
		}
		if op.now < n.lastNow {
			return 0, 0, 0, 0, rma.ErrClock
		}
		if _, ok := n.lines[op.order]; ok {
			return 0, 0, 0, 0, rma.ErrConflict
		}
	case "auth":
		if op.rma == "" || op.order == "" || op.qty < 1 || op.now < 0 {
			return 0, 0, 0, 0, rma.ErrInvalid
		}
		if op.now < n.lastNow {
			return 0, 0, 0, 0, rma.ErrClock
		}
		lm, ok := n.lines[op.order]
		if !ok {
			return 0, 0, 0, 0, rma.ErrNotFound
		}
		if _, ok := lm[op.line]; !ok {
			return 0, 0, 0, 0, rma.ErrNotFound
		}
		if _, ok := n.auths[op.rma]; ok {
			return 0, 0, 0, 0, rma.ErrConflict
		}
		if op.now-n.shipAt[op.order] > n.wd {
			return 0, 0, 0, 0, rma.ErrWindow
		}
		// 拒绝操作不落地到期：先在副本视角计算余量。
		free := func() int64 {
			l := lm[op.line]
			var reserved int64
			for _, a := range n.auths {
				if a.order != op.order {
					continue
				}
				if a.exp <= op.now {
					continue
				}
				reserved += a.items[op.line]
			}
			return l.shipped - l.a - l.b - reserved
		}()
		if op.qty > free {
			return 0, 0, 0, 0, rma.ErrCapacity
		}
	case "recv":
		if op.rma == "" || op.qty < 1 || op.now < 0 ||
			(op.g != grade.A && op.g != grade.B && op.g != grade.C) {
			return 0, 0, 0, 0, rma.ErrInvalid
		}
		if op.now < n.lastNow {
			return 0, 0, 0, 0, rma.ErrClock
		}
		a, ok := n.auths[op.rma]
		if !ok {
			return 0, 0, 0, 0, rma.ErrNotFound
		}
		if _, ok := a.items[op.line]; !ok {
			return 0, 0, 0, 0, rma.ErrNotFound
		}
		if a.exp <= op.now {
			return 0, 0, 0, 0, rma.ErrExpired
		}
		if a.items[op.line] < op.qty {
			return 0, 0, 0, 0, rma.ErrCapacity
		}
	}

	// 通过：落地到期、推进时钟、执行。
	n.expire(op.now)
	n.lastNow = op.now
	switch op.kind {
	case "add":
		n.shipAt[op.order] = op.now - op.now // shipAt 单独存
		n.shipAt[op.order] = op.shipAt
		n.lines[op.order] = map[int64]*naiveLine{
			op.line: {shipped: op.addShipped, paid: op.addPaid},
		}
	case "auth":
		a := &naiveAuth{
			order: op.order, exp: op.now + n.V,
			items: map[int64]int64{op.line: op.qty}, owes: n.fee,
		}
		n.auths[op.rma] = a
		return 0, 0, 0, a.exp, nil
	case "recv":
		a := n.auths[op.rma]
		l := n.lines[a.order][op.line]
		var d int64
		if op.g != grade.C {
			before := n.cum(l)
			if op.g == grade.A {
				l.a += op.qty
			} else {
				l.b += op.qty
			}
			due = n.cum(l) - before
			d = due
			if d > a.owes {
				d = a.owes
			}
			a.owes -= d
		}
		a.items[op.line] -= op.qty
		return due, d, due - d, 0, nil
	}
	return 0, 0, 0, 0, nil
}

func TestRandomVsNaive(t *testing.T) {
	const seeds = 1500
	for seed := int64(1); seed <= seeds; seed++ {
		rng := rand.New(rand.NewSource(seed))
		wd := int64(1 + rng.Intn(30))
		V := int64(1 + rng.Intn(12))
		beta := int64(rng.Intn(101))
		fee := int64(rng.Intn(200))
		r, svc := newStack(t, wd, V, beta, fee)
		nv := newNaive(t, wd, V, beta, fee)

		const orderCount = 3
		type oinfo struct {
			shipAt        int64
			line          int64
			shipped, paid int64
		}
		infos := make([]oinfo, orderCount)
		now := int64(0)
		for i := range infos {
			ship := int64(rng.Intn(10))
			shipped := int64(1 + rng.Intn(6))
			paid := int64(rng.Intn(5000))
			infos[i] = oinfo{shipAt: ship, line: 1, shipped: shipped, paid: paid}
			op := simOp{
				kind: "add", now: now, order: fmt.Sprintf("O%d", i),
				line: 1, addShipped: shipped, addPaid: paid, shipAt: ship,
			}
			if err := r.AddOrder(rma.OrderID(op.order), ship,
				[]rma.Line{{ID: 1, Shipped: shipped, Paid: paid}}, now); err != nil {
				t.Fatalf("seed=%d add err=%v", seed, err)
			}
			if _, _, _, _, err := runNaive(nv, op); err != nil {
				t.Fatalf("seed=%d naive add err=%v", seed, err)
			}
		}

		var authSeq int
		active := []string{}
		const ops = 40
		for step := 0; step < ops; step++ {
			// now 单调：多数小幅递增，偶尔跳跃触发到期/窗口。
			switch rng.Intn(6) {
			case 5:
				now += V + int64(rng.Intn(3))
			default:
				now += int64(rng.Intn(3))
			}
			oi := infos[rng.Intn(orderCount)]
			order := fmt.Sprintf("O%d", rng.Intn(orderCount))
			op := simOp{kind: "auth", now: now, order: order, line: 1,
				qty: int64(1 + rng.Intn(int(oi.shipped)+2))}
			id := fmt.Sprintf("R%d", authSeq)
			authSeq++
			op.rma = id
			// 少量重复单号/回退时钟，制造拒绝分支。
			if rng.Intn(10) == 0 && len(active) > 0 {
				op.rma = active[rng.Intn(len(active))]
			}
			opNow := now
			if rng.Intn(12) == 0 {
				opNow = now - 1
			}
			op.now = opNow

			t.Logf("seed=%d step=%d AUTH in=%+v", seed, step, op)
			gotExp, gotErr := r.Authorize(rma.RMAID(op.rma), rma.OrderID(op.order),
				[]rma.Item{{Line: 1, Qty: op.qty}}, op.now)
			_, _, _, wantExp, wantErr := runNaive(nv, op)
			if !sameErr(gotErr, wantErr) {
				t.Fatalf("seed=%d step=%d auth err got=%v want=%v op=%+v", seed, step, gotErr, wantErr, op)
			}
			if gotErr == nil {
				active = append(active, id)
				if gotExp != wantExp {
					t.Fatalf("seed=%d exp got=%d want=%d", seed, gotExp, wantExp)
				}
			}

			// 对一张单收货：随机挑历史单号（可能已到期）。
			if len(active) == 0 {
				continue
			}
			rid := active[rng.Intn(len(active))]
			var g grade.Grade
			switch rng.Intn(3) {
			case 0:
				g = grade.A
			case 1:
				g = grade.B
			default:
				g = grade.C
			}
			rop := simOp{kind: "recv", now: now, rma: rid, line: 1,
				qty: int64(1 + rng.Intn(4)), g: g}
			if rng.Intn(12) == 0 {
				rop.now = now - 1
			}
			t.Logf("seed=%d step=%d RECV in=%+v", seed, step, rop)
			res, gErr := svc.Receive(rma.RMAID(rop.rma), 1, rop.qty, rop.g, rop.now)
			wd2, wded, wpaid, _, werr := runNaive(nv, rop)
			if !sameErr(gErr, werr) {
				t.Fatalf("seed=%d step=%d recv err got=%v want=%v op=%+v", seed, step, gErr, werr, rop)
			}
			if gErr == nil {
				if res.Due != wd2 || res.Deducted != wded || res.Paid != wpaid {
					t.Fatalf("seed=%d step=%d money got=%+v want=(%d,%d,%d)", seed, step, res, wd2, wded, wpaid)
				}
				t.Logf("seed=%d step=%d RECV out=(due=%d ded=%d paid=%d) match naive", seed, step, res.Due, res.Deducted, res.Paid)
			} else {
				t.Logf("seed=%d step=%d RECV rejected=%v match naive", seed, step, gErr)
			}
		}
	}
}

func sameErr(a, b error) bool {
	if a == nil || b == nil {
		return a == b
	}
	// 容量错误在批量场景由 ItemError 包装，随机用单项，可直接比对哨兵。
	if errors.Is(a, grade.ErrInvalid) && errors.Is(b, rma.ErrInvalid) {
		return true
	}
	return errors.Is(a, b)
}
