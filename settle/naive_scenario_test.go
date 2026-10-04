package settle

import (
	"errors"
	"fmt"
	"math/rand"
	"sort"
	"strings"
	"testing"

	"ontology/instr"
)

type opKind int

const (
	opCredit opKind = iota
	opCash
	opPrice
	opInstr
	opCancel
	opRun
)

type op struct {
	kind                opKind
	day                 int
	acct, id, s, b, sym string
	qty, amount, price  int64
	sd                  int
}

func (o op) String() string {
	switch o.kind {
	case opCredit:
		return fmt.Sprintf("Credit(d=%d,%s,%s,q=%d)", o.day, o.acct, o.sym, o.qty)
	case opCash:
		return fmt.Sprintf("CreditCash(d=%d,%s,a=%d)", o.day, o.acct, o.amount)
	case opPrice:
		return fmt.Sprintf("SetPrice(d=%d,%s,p=%d)", o.day, o.sym, o.price)
	case opInstr:
		return fmt.Sprintf("Instruct(d=%d,id=%s,%s->%s,%s,q=%d,a=%d,sd=%d)",
			o.day, o.id, o.s, o.b, o.sym, o.qty, o.amount, o.sd)
	case opCancel:
		return fmt.Sprintf("Cancel(d=%d,id=%s)", o.day, o.id)
	default:
		return fmt.Sprintf("RunSettle(d=%d)", o.day)
	}
}

func runReal(eng *Engine, book *instr.Book, o op) (error, *Result) {
	switch o.kind {
	case opCredit:
		return book.Credit(o.day, o.acct, o.sym, o.qty), nil
	case opCash:
		return book.CreditCash(o.day, o.acct, o.amount), nil
	case opPrice:
		return book.SetPrice(o.day, o.sym, o.price), nil
	case opInstr:
		return book.Instruct(o.day, o.id, o.s, o.b, o.sym, o.qty, o.amount, o.sd), nil
	case opCancel:
		return book.Cancel(o.day, o.id), nil
	default:
		r, err := eng.RunSettle(o.day)
		return err, r
	}
}

func runModel(n *naive, o op) (error, []nstep) {
	switch o.kind {
	case opCredit:
		return n.credit(o.day, o.acct, o.sym, o.qty), nil
	case opCash:
		return n.creditCash(o.day, o.acct, o.amount), nil
	case opPrice:
		return n.setPrice(o.day, o.sym, o.price), nil
	case opInstr:
		return n.instruct(o.day, o.id, o.s, o.b, o.sym, o.qty, o.amount, o.sd), nil
	case opCancel:
		return n.cancel(o.day, o.id), nil
	default:
		s, err := n.runSettle(o.day)
		return err, s
	}
}

// snapshot 汇总两边可观察的全部状态做对账。
func snapshotReal(book *instr.Book) string {
	var sb strings.Builder
	type kv struct {
		id  string
		ins instr.Instruction
	}
	var all []kv
	for _, v := range allViews(book) {
		all = append(all, kv{v.ID, v})
	}
	sort.Slice(all, func(i, j int) bool { return all[i].ins.Seq < all[j].ins.Seq })
	for _, x := range all {
		v := x.ins
		fmt.Fprintf(&sb, "O %s seq=%d %s->%s %s q=%d a=%d sd=%d d=%d paid=%d st=%s sf=%d bf=%d comp=%d\n",
			x.id, v.Seq, v.Seller, v.Buyer, v.Sym, v.Qty, v.Amount, v.SD,
			v.Delivered, v.Paid, v.Status, v.SellerFine, v.BuyerFine, v.SellerComp)
	}
	return sb.String()
}

func snapshotModel(n *naive) string {
	var sb strings.Builder
	type kv struct {
		id string
		o  *norder
	}
	var all []kv
	for id, o := range n.orders {
		all = append(all, kv{id, o})
	}
	sort.Slice(all, func(i, j int) bool { return all[i].o.seq < all[j].o.seq })
	for _, x := range all {
		o := x.o
		fmt.Fprintf(&sb, "O %s seq=%d %s->%s %s q=%d a=%d sd=%d d=%d paid=%d st=%s sf=%d bf=%d comp=%d\n",
			x.id, o.seq, o.seller, o.buyer, o.sym, o.qty, o.amount, o.sd,
			o.d, o.paid, o.status, o.sFine, o.bFine, o.comp)
	}
	return sb.String()
}

// allViews 是测试用枚举（通过 instr 暴露的测试钩子；见 export_test.go）。
var allViews = func(book *instr.Book) []instr.Instruction { return book.AllForTest() }

func sameErr(a, b error) bool {
	return errors.Is(a, b) // b 也是哨兵之一，互为同一哨兵即可
}

func TestNaiveModel1500(t *testing.T) {
	const sequences = 1500
	for seed := int64(1); seed <= sequences; seed++ {
		rng := rand.New(rand.NewSource(seed))
		u := int64([]int{1, 2, 5, 10, 100, 1000}[rng.Intn(6)])
		rs := int64(rng.Intn(101))
		rb := int64(rng.Intn(101))
		age := 1 + rng.Intn(6)
		book := instr.New(u, rs, rb, age)
		eng := New(book)
		model := newNaive(u, rs, rb, age)

		const accts, syms = 4, 2
		acctName := func(i int) string { return fmt.Sprintf("acct%d", i) }
		symName := func(i int) string { return fmt.Sprintf("sym%d", i) }

		var log strings.Builder
		fmt.Fprintf(&log, "seed=%d U=%d rs=%d rb=%d age=%d\n", seed, u, rs, rb, age)

		apply := func(o op, stepsReal *Result, stepsModel []nstep) {
			er, rr := runReal(eng, book, o)
			em, sm := runModel(model, o)
			// 错误哨兵对照（两边的哨兵是同一批 error 值）。
			if (er == nil) != (em == nil) ||
				(er != nil && !errors.Is(er, em) && !errors.Is(em, er)) {
				t.Fatalf("seed=%d op=%s: real err=%v model err=%v\nLOG:\n%s",
					seed, o, er, em, log.String())
			}
			if er == nil && o.kind == opRun {
				compareSteps(t, seed, o, rr.Steps, sm, &log)
			}
			// 全状态对账。
			if got, want := snapshotReal(book), snapshotModel(model); got != want {
				t.Fatalf("seed=%d op=%s state mismatch\n--- real ---\n%s--- model ---\n%s\nLOG:\n%s",
					seed, o, got, want, log.String())
			}
			// 余额对账。
			for ai := 0; ai < accts; ai++ {
				a := acctName(ai)
				for si := 0; si < syms; si++ {
					sym := symName(si)
					if g, w := book.Holdings(a, sym), model.hold[a][sym]; g != w {
						t.Fatalf("seed=%d holdings %s/%s real=%d model=%d\nLOG:%s",
							seed, a, sym, g, w, log.String())
					}
				}
				if g, w := book.Cash(a), model.cash[a]; g != w {
					t.Fatalf("seed=%d cash %s real=%d model=%d\nLOG:%s", seed, a, g, w, log.String())
				}
				if g, w := book.Payable(a), model.payable[a]; g != w {
					t.Fatalf("seed=%d payable %s real=%d model=%d\nLOG:%s", seed, a, g, w, log.String())
				}
				if g, w := book.Receivable(a), model.receiv[a]; g != w {
					t.Fatalf("seed=%d receivable %s real=%d model=%d\nLOG:%s", seed, a, g, w, log.String())
				}
			}
			_ = stepsReal
			fmt.Fprintf(&log, "  %-55s => real=%v model=%v\n", o, er, em)
		}

		for j := 0; j < syms; j++ {
			apply(op{kind: opPrice, day: 0, sym: symName(j), price: int64(1 + rng.Intn(1000))}, nil, nil)
		}

		var idCount int
		mkID := func() string { idCount++; return fmt.Sprintf("ord%d", idCount) }
		ops := 30 + rng.Intn(70)
		for k := 0; k < ops; k++ {
			day := rng.Intn(12)
			switch rng.Intn(6) {
			case 0:
				apply(op{kind: opCredit, day: day, acct: acctName(rng.Intn(accts)),
					sym: symName(rng.Intn(syms)), qty: int64(1 + rng.Intn(3000))}, nil, nil)
			case 1:
				apply(op{kind: opCash, day: day, acct: acctName(rng.Intn(accts)),
					amount: int64(1 + rng.Intn(300000))}, nil, nil)
			case 2:
				apply(op{kind: opPrice, day: day, sym: symName(rng.Intn(syms)),
					price: int64(1 + rng.Intn(2000))}, nil, nil)
			case 3:
				qty := (1 + rng.Intn(20)) * int(u) // 保证是 U 的倍数
				apply(op{kind: opInstr, day: day, id: mkID(),
					s: acctName(rng.Intn(accts)), b: acctName(rng.Intn(accts)),
					sym: symName(rng.Intn(syms)), qty: int64(qty),
					amount: int64(1 + rng.Intn(500000)),
					sd:     day + 1 + rng.Intn(6)}, nil, nil)
			case 4:
				// 有时用不存在的编号测试 NotFound；多数用已生成编号。
				id := "ghost"
				if idCount > 0 && rng.Intn(3) > 0 {
					id = fmt.Sprintf("ord%d", 1+rng.Intn(idCount))
				}
				apply(op{kind: opCancel, day: day, id: id}, nil, nil)
			case 5:
				apply(op{kind: opRun, day: day}, nil, nil)
			}
		}
		// 序列尾部再跑若干天，尽量触发逾期买入/取消。
		for day := 0; day <= 15; day++ {
			apply(op{kind: opRun, day: day}, nil, nil)
		}
		t.Logf("seed=%d replayed %d ops OK", seed, ops)
	}
}

func compareSteps(t *testing.T, seed int64, o op, real []Step, model []nstep, log *strings.Builder) {
	t.Helper()
	if len(real) != len(model) {
		t.Fatalf("seed=%d %s touched real=%d model=%d\nLOG:%s",
			seed, o, len(real), len(model), log.String())
	}
	for i := range real {
		g, w := real[i], model[i]
		if g.ID != w.id || g.SD != w.sd || g.BeforeDelivered != w.d ||
			g.BeforePaid != w.paid || g.Remain != w.r || g.SellerAvail != w.a ||
			g.BuyerAfford != w.b || g.Deliver != w.k || g.Pay != w.pay ||
			g.AfterRemain != w.r2 || g.SellerLiable != w.sellerL ||
			g.BuyerLiable != w.buyerL || g.SellerFine != w.sf ||
			g.BuyerFine != w.bf || g.BuyIn != w.buyIn || g.Cancelled != w.cancelled ||
			g.Comp != w.comp || g.FinalStatus != w.status {
			t.Fatalf("seed=%d %s step %d mismatch\nreal =%+v\nmodel=%+v\nLOG:%s",
				seed, o, i, g, w, log.String())
		}
	}
}

// TestDeterministicReplay 相同操作序列重放两次，全部可观察结果一致。
func TestDeterministicReplay(t *testing.T) {
	play := func() string {
		b := instr.New(100, 17, 9, 3)
		e := New(b)
		var sb strings.Builder
		step := func(tag string, err error, res *Result) {
			fmt.Fprintf(&sb, "%s err=%v\n", tag, err)
			if res != nil {
				for _, s := range res.Steps {
					fmt.Fprintf(&sb, "  %s k=%d pay=%d r'=%d sf=%d bf=%d comp=%d st=%s\n",
						s.ID, s.Deliver, s.Pay, s.AfterRemain, s.SellerFine,
						s.BuyerFine, s.Comp, s.FinalStatus)
				}
			}
		}
		_ = b.SetPrice(0, "X", 37)
		_ = b.Credit(0, "S", "X", 500)
		_ = b.CreditCash(0, "B", 5000)
		_ = b.CreditCash(0, "C", 1200)
		_ = b.Instruct(0, "i1", "S", "B", "X", 1000, 10099, 2)
		_ = b.Instruct(0, "i2", "B", "C", "X", 600, 6001, 2)
		res2, err := e.RunSettle(2)
		step("run2", err, res2)
		_, errDup := e.RunSettle(2)
		fmt.Fprintf(&sb, "run2-repeat err=%v\n", errDup)
		res3, err := e.RunSettle(3)
		step("run3", err, res3)
		res5, err := e.RunSettle(5)
		step("run5", err, res5)
		for _, a := range []string{"S", "B", "C"} {
			fmt.Fprintf(&sb, "BAL %s q=%d cash=%d pay=%d rec=%d\n",
				a, b.Holdings(a, "X"), b.Cash(a), b.Payable(a), b.Receivable(a))
		}
		t.Logf("replay transcript:\n%s", sb.String())
		return sb.String()
	}
	if play() != play() {
		t.Fatal("replaying the same operation sequence gave different results")
	}
}
