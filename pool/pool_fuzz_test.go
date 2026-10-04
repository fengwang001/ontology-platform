package pool

import (
	"errors"
	"fmt"
	"math/rand"
	"reflect"
	"sort"
	"strings"
	"testing"

	"ontology/loan"
)

// realSnapshot 是产品实现每一步之后的完整可观察状态。
type realSnapshot struct {
	now       int64
	prices    map[string]int64
	idle      map[string]int64 // sym|lender
	contracts map[int64]cSnap
	handed    map[string]int64
	lent      map[string]int64
	payable   map[string]int64
	buyins    []Buyin
}

type cSnap struct {
	lender   string
	borrower string
	sym      string
	qty      int64
	recall   bool
	dl       int64
}

func snapshotReal(p *Pool) realSnapshot {
	p.mu.Lock()
	defer p.mu.Unlock()
	s := realSnapshot{
		now:       p.now,
		prices:    map[string]int64{},
		idle:      map[string]int64{},
		contracts: map[int64]cSnap{},
		handed:    map[string]int64{},
		lent:      map[string]int64{},
		payable:   map[string]int64{},
		buyins:    p.sched.Buyins(),
	}
	bookSyms := map[string]bool{}
	for _, key := range p.book.Symbols() {
		sym := string(key)
		bookSyms[sym] = true
		if st := p.syms[key]; st != nil && st.priced {
			s.prices[sym] = st.price
		}
		s.handed[sym] = p.book.HandedBack(key)
		s.lent[sym] = p.book.TotalLend(key)
		for _, lid := range p.book.Lenders(key) {
			s.idle[sym+"|"+string(lid)] = p.book.Idle(key, lid)
		}
	}
	for key, st := range p.syms {
		if st.priced && !bookSyms[string(key)] {
			s.prices[string(key)] = st.price
		}
	}
	for id := int64(1); id <= p.nextID; id++ {
		if c, ok := p.book.Get(id); ok {
			s.contracts[id] = cSnap{
				string(c.Lender), string(c.Borrower), string(c.Symbol),
				c.Qty, c.Kind == loan.Recalled, c.Dl,
			}
		}
	}
	for k, v := range p.payable {
		s.payable[string(k)] = v
	}
	return s
}

func snapshotNaive(m *naiveModel) realSnapshot {
	s := realSnapshot{
		now:       m.now,
		prices:    map[string]int64{},
		idle:      map[string]int64{},
		contracts: map[int64]cSnap{},
		handed:    map[string]int64{},
		lent:      map[string]int64{},
		payable:   map[string]int64{},
	}
	for k, v := range m.prices {
		s.prices[k] = v
	}
	for sym, ls := range m.lenders {
		s.lent[sym] = m.lent[sym]
		s.handed[sym] = m.handed[sym]
		for name, l := range ls {
			s.idle[sym+"|"+name] = l.idle
		}
	}
	for id, c := range m.contracts {
		s.contracts[id] = cSnap{c.lender, c.borrower, c.sym, c.qty, c.recall, c.dl}
	}
	for k, v := range m.payable {
		s.payable[k] = v
	}
	for _, b := range m.buyins {
		s.buyins = append(s.buyins, Buyin{
			ContractID: b.id, Qty: b.qty, Price: b.price, Penalty: b.penalty, At: b.at,
		})
	}
	return s
}

func callReal(p *Pool, o op) (error, []int64) {
	switch o.kind {
	case "price":
		return p.SetPrice(o.now, []byte(o.sym), o.price), nil
	case "lend":
		return p.Lend(o.now, []byte(o.actor), []byte(o.sym), o.qty), nil
	case "borrow":
		ids, err := p.Borrow(o.now, []byte(o.actor), []byte(o.sym), o.qty)
		return err, ids
	case "withdraw":
		return p.Withdraw(o.now, []byte(o.actor), []byte(o.sym), o.qty), nil
	case "return":
		return p.Return(o.now, []byte(o.actor), []byte(o.sym), o.qty), nil
	}
	return ErrInvalid, nil
}

func errClass(err error) string {
	switch {
	case err == nil:
		return "ok"
	case errors.Is(err, ErrInvalid):
		return "invalid"
	case errors.Is(err, ErrClockBack):
		return "clock"
	case errors.Is(err, ErrNotFound):
		return "notfound"
	case errors.Is(err, ErrOverQty):
		return "overqty"
	case errors.Is(err, ErrInsufficient):
		return "insufficient"
	default:
		return err.Error()
	}
}

func fmtOp(o op) string {
	if o.badParam {
		return fmt.Sprintf("INVALID(%s now=%d actor=%q sym=%q qty=%d price=%d)",
			o.kind, o.now, o.actor, o.sym, o.qty, o.price)
	}
	return fmt.Sprintf("%s(now=%d actor=%q sym=%q qty=%d price=%d)",
		o.kind, o.now, o.actor, o.sym, o.qty, o.price)
}

// TestRandomAgainstNaive：1500 组确定性随机序列，与逐步朴素模拟逐步对照。
func TestRandomAgainstNaive(t *testing.T) {
	const groups = 1500
	for g := 0; g < groups; g++ {
		rng := rand.New(rand.NewSource(int64(g) + 1))
		n := int64(1 + rng.Intn(20))
		pen := int64(rng.Intn(2000))
		p, err := New(n, pen)
		if err != nil {
			t.Fatal(err)
		}
		m := newNaive(n, pen)

		syms := []string{"S", "T"}
		lenders := []string{"L1", "L2", "L3", "L4"}
		borrowers := []string{"B1", "B2", "B3"}
		var log []string
		steps := 40 + rng.Intn(40)
		var now int64
		for st := 0; st < steps; st++ {
			o := op{
				kind:  []string{"price", "lend", "borrow", "withdraw", "return"}[rng.Intn(5)],
				sym:   syms[rng.Intn(len(syms))],
				qty:   int64(1 + rng.Intn(120)),
				price: int64(1 + rng.Intn(50)),
			}
			switch rng.Intn(10) {
			case 0:
				now -= int64(rng.Intn(3)) // 偶发时钟回退
				if now < 0 {
					now = 0
				}
			case 1:
				now += 0
			default:
				now += int64(rng.Intn(4))
			}
			o.now = now
			if o.kind == "lend" || o.kind == "withdraw" {
				o.actor = lenders[rng.Intn(len(lenders))]
			} else {
				o.actor = borrowers[rng.Intn(len(borrowers))]
			}
			if rng.Intn(20) == 0 {
				o.badParam = true
				if o.kind == "price" {
					if rng.Intn(2) == 0 {
						o.price = 0
					} else {
						o.now = -1
					}
				} else {
					switch rng.Intn(3) {
					case 0:
						o.qty = 0
					case 1:
						o.now = -1
					case 2:
						o.actor = ""
					}
				}
			}

			rErr, rIDs := callReal(p, o)
			mErr, mIDs := m.run(o)
			verdict := "MATCH"
			if errClass(rErr) != errClass(mErr) {
				verdict = fmt.Sprintf("ERR-DIFF real=%s naive=%s", errClass(rErr), errClass(mErr))
			} else if rErr == nil && fmt.Sprint(rIDs) != fmt.Sprint(mIDs) {
				verdict = fmt.Sprintf("ID-DIFF real=%v naive=%v", rIDs, mIDs)
			}
			log = append(log, fmt.Sprintf("  step %2d %-70s => real=%s naive=%s [%s]",
				st, fmtOp(o), errClass(rErr), errClass(mErr), verdict))

			rs, ms := snapshotReal(p), snapshotNaive(m)
			if verdict != "MATCH" || !reflect.DeepEqual(rs, ms) {
				var sb strings.Builder
				sb.WriteString(fmt.Sprintf("group %d (seed=%d n=%d pen=%d) diverged at step %d: %s\n",
					g, g+1, n, pen, st, verdict))
				sb.WriteString("判定依据: errors.Is 分类与全量快照（价格/空闲/合约/交还/累计Lend/应付/买入）\n")
				for _, line := range log {
					sb.WriteString(line + "\n")
				}
				if reflect.DeepEqual(rs, ms) {
					t.Fatalf("%s", sb.String())
				}
				sb.WriteString("snapshot real : " + fmtSnap(rs) + "\n")
				sb.WriteString("snapshot naive: " + fmtSnap(ms) + "\n")
				t.Fatalf("%s", sb.String())
			}

			// 每个接受步骤后复核恒等不变量。
			if rErr == nil {
				for _, sym := range syms {
					if err := checkInvariant(p, sym); err != nil {
						t.Fatalf("group %d step %d: %v\n%s", g, st, err, strings.Join(log, "\n"))
					}
				}
			}
		}
		t.Logf("group %4d: %d steps OK; final contracts=%d buyins=%d; 判定依据=%s",
			g, steps, len(snapshotReal(p).contracts), len(snapshotReal(p).buyins),
			"逐步 errors.Is 分类 + 全量快照 DeepEqual + 不变量")
	}
}

func fmtSnap(s realSnapshot) string {
	keys := make([]int64, 0, len(s.contracts))
	for id := range s.contracts {
		keys = append(keys, id)
	}
	sort.Slice(keys, func(i, j int) bool { return keys[i] < keys[j] })
	var b strings.Builder
	fmt.Fprintf(&b, "now=%d prices=%v lent=%v handed=%v payable=%v buyins=%d",
		s.now, s.prices, s.lent, s.handed, s.payable, len(s.buyins))
	b.WriteString(" contracts={")
	for _, id := range keys {
		c := s.contracts[id]
		flag := ""
		if c.recall {
			flag = fmt.Sprintf("R@%d", c.dl)
		}
		fmt.Fprintf(&b, "%d:(%s->%s %s q%d%s) ", id, c.lender, c.borrower, c.sym, c.qty, flag)
	}
	b.WriteString("} idle=" + fmt.Sprintf("%v", s.idle))
	return b.String()
}
