package margin

import (
	"errors"
	"fmt"
	"sort"
	"testing"
)

// pcg 是独立、确定性的轻量随机源，保证 2000 组序列可逐字重放。
type pcg struct {
	state uint64
	inc   uint64
}

func newPCG(seed uint64) *pcg {
	p := &pcg{inc: 1}
	p.state = 0
	p.next()
	p.state += seed
	p.next()
	return p
}

func (p *pcg) next() uint32 {
	old := p.state
	p.state = old*6364136223846793005 + p.inc
	xorshifted := uint32(((old >> 18) ^ old) >> 27)
	rot := uint(old >> 59)
	return (xorshifted >> rot) | (xorshifted << ((-rot) & 31))
}

func (p *pcg) intn(n int) int {
	if n <= 0 {
		panic("bad range")
	}
	return int(p.next()) % n
}

func (p *pcg) i64n(n int64) int64 {
	if n <= 0 {
		panic("bad range")
	}
	return int64(p.next()) % n
}

type opKind int

const (
	opDeposit opKind = iota
	opOpen
	opClose
	opWithdraw
	opMark
)

type op struct {
	kind opKind
	a    string
	dir  Direction
	n, p int64
	x    int64
}

func (o op) String() string {
	switch o.kind {
	case opDeposit:
		return fmt.Sprintf("Deposit(%s,%d)", o.a, o.x)
	case opOpen:
		d := "多"
		if o.dir == Short {
			d = "空"
		}
		return fmt.Sprintf("Open(%s,%s,%d,%d)", o.a, d, o.n, o.p)
	case opClose:
		return fmt.Sprintf("Close(%s,%d)", o.a, o.p)
	case opWithdraw:
		return fmt.Sprintf("Withdraw(%s,%d)", o.a, o.x)
	default:
		return fmt.Sprintf("Mark(%d)", o.p)
	}
}

func engineErrKind(err error) naiveErrKind {
	switch {
	case err == nil:
		return nOK
	case errors.Is(err, ErrInvalidArgument):
		return nInvalid
	case errors.Is(err, ErrAccountNotFound):
		return nNotFound
	case errors.Is(err, ErrPositionConflict):
		return nConflict
	case errors.Is(err, ErrNoPosition):
		return nNoPosition
	case errors.Is(err, ErrInsufficientFunds):
		return nInsufficient
	default:
		return -1
	}
}

func sameItems(a, b []LiquidationItem) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if !sameItem(a[i], b[i]) {
			return false
		}
	}
	return true
}

// 生成一条 60 步的随机操作序列。
// 为保证各条账务路径都被覆盖：保证金按开仓需求成比例给足（偶尔少给制造资金不足），
// 价格跨度大，并周期性插入极端 Mark 以制造负权益强平、ADL 与坏账。
func genOps(r *pcg, iRate int) []op {
	const names = "ABCDE"
	ops := make([]op, 60)
	for i := 0; i < len(ops); i++ {
		a := string(runes(names)[r.intn(len(names))])
		if i%15 == 14 {
			// 周期性极端价格：一半极低、一半极高，制造负权益强平、ADL 与坏账。
			if r.intn(2) == 0 {
				ops[i] = op{kind: opMark, p: 1 + r.i64n(30)}
			} else {
				ops[i] = op{kind: opMark, p: 1_000_000 - r.i64n(30)}
			}
			continue
		}
		// 先随机决定一笔开仓意图，再按需配套存款，保证多数账户真持有仓位；
		// 存款在需求附近 ±3 扰动，覆盖恰等、少 1（资金不足）与资金充裕。
		if i+1 < len(ops) && r.intn(3) != 0 {
			dir := Long
			if r.intn(2) == 0 {
				dir = Short
			}
			nQty := 1 + r.i64n(100)
			price := 1 + r.i64n(1000)
			req := (nQty*price*int64(iRate) + 9999) / 10000
			delta := int64(r.intn(7) - 3) // -3..+3
			dep := req + delta
			if dep < 1 {
				dep = 1
			}
			ops[i] = op{kind: opDeposit, a: a, x: dep}
			ops[i+1] = op{kind: opOpen, a: a, dir: dir, n: nQty, p: price}
			i++ // 跳过已配套占用的下一槽
			continue
		}
		switch r.intn(4) {
		case 0:
			ops[i] = op{kind: opDeposit, a: a, x: 1 + r.i64n(5000)}
		case 1:
			ops[i] = op{kind: opClose, a: a, p: 1 + r.i64n(1000)}
		case 2:
			ops[i] = op{kind: opWithdraw, a: a, x: 1 + r.i64n(3000)}
		default:
			ops[i] = op{kind: opMark, p: 1 + r.i64n(1000)}
		}
	}
	return ops
}

func runes(s string) []rune { return []rune(s) }

func applyNaive(n *naiveEngine, o op) ([]LiquidationItem, naiveErrKind) {
	switch o.kind {
	case opDeposit:
		return nil, n.deposit(o.a, o.x)
	case opOpen:
		return nil, n.open(o.a, int(o.dir), o.n, o.p)
	case opClose:
		return nil, n.close(o.a, o.p)
	case opWithdraw:
		return nil, n.withdraw(o.a, o.x)
	default:
		return n.mark(o.p)
	}
}

func applyEngine(e *Engine, o op) ([]LiquidationItem, error) {
	switch o.kind {
	case opDeposit:
		return nil, e.Deposit(o.a, o.x)
	case opOpen:
		return nil, e.Open(o.a, o.dir, o.n, o.p)
	case opClose:
		return nil, e.Close(o.a, o.p)
	case opWithdraw:
		return nil, e.Withdraw(o.a, o.x)
	default:
		return e.Mark(o.p)
	}
}

func sameSnapshots(e *Engine, n *naiveEngine) (string, bool) {
	var ids []string
	for id := range n.accounts {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	for _, id := range ids {
		got, err := e.AccountSnapshot(id)
		if err != nil {
			return fmt.Sprintf("engine missing account %s", id), false
		}
		want := n.accounts[id]
		if got.M != want.m || got.Q != want.q || got.C != want.c {
			return fmt.Sprintf("account %s: engine (m=%d q=%d c=%d) naive (m=%d q=%d c=%d)",
				id, got.M, got.Q, got.C, want.m, want.q, want.c), false
		}
	}
	if e.InsuranceFund() != n.z || e.BadDebt() != n.b || e.MarkPrice() != n.lastMark {
		return fmt.Sprintf("globals: engine Z=%d B=%d P=%d; naive Z=%d B=%d P=%d",
			e.InsuranceFund(), e.BadDebt(), e.MarkPrice(), n.z, n.b, n.lastMark), false
	}
	return "", true
}

// TestRandomAgainstNaive 用 2000 组随机序列对照朴素模拟，日志打印输入、输出与判定依据。
func TestRandomAgainstNaive(t *testing.T) {
	const sequences = 2000
	rateChoices := [][3]int{
		{1000, 500, 100},
		{1000, 999, 0},
		{2000, 100, 10000},
		{5000, 1000, 500},
		{10000, 1, 1},
		{3333, 2222, 777},
	}
	for seq := 0; seq < sequences; seq++ {
		r := newPCG(0x9e3779b97f4a7c15 ^ uint64(seq)*2862933555777941757)
		rates := rateChoices[r.intn(len(rateChoices))]
		e, err := New(rates[0], rates[1], rates[2])
		if err != nil {
			t.Fatalf("seq %d constructor: %v", seq, err)
		}
		n, kind := newNaive(rates[0], rates[1], rates[2])
		if kind != nOK {
			t.Fatalf("seq %d naive constructor: %d", seq, kind)
		}
		ops := genOps(r, rates[0])

		t.Logf("=== seq %d rates I=%d Mm=%d F=%d, %d ops ===",
			seq, rates[0], rates[1], rates[2], len(ops))
		for step, o := range ops {
			engItems, engErr := applyEngine(e, o)
			nItems, nKind := applyNaive(n, o)

			// 判定依据：拒绝原因必须逐类一致；Mark 清单必须逐项一致。
			if gotKind := engineErrKind(engErr); gotKind != nKind {
				t.Fatalf("seq %d step %d %s: engine err=%v (%d), naive kind=%d",
					seq, step, o, engErr, gotKind, nKind)
			}
			if nKind == nOK && o.kind == opMark && !sameItems(engItems, nItems) {
				t.Fatalf("seq %d step %d %s: items differ\nengine=%+v\nnaive =%+v",
					seq, step, o, engItems, nItems)
			}

			outcome := "accepted"
			if nKind != nOK {
				outcome = fmt.Sprintf("rejected(kind=%d)", nKind)
			}
			t.Logf("seq %d step %2d | %-28s | %-16s | mark items=%v",
				seq, step, o.String(), outcome, nItems)

			if msg, ok := sameSnapshots(e, n); !ok {
				t.Fatalf("seq %d step %d state mismatch: %s", seq, step, msg)
			}
			if !n.invariantsHold() {
				t.Fatalf("seq %d step %d naive invariants violated after %s",
					seq, step, o)
			}
		}
	}
}
