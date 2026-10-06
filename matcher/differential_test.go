package matcher_test

import (
	"flag"
	"fmt"
	"math/rand"
	"testing"

	"ontology/matcher"
	"ontology/matcher/naive"
)

var diffLog = flag.Bool("difflog", false, "print every differential operation, output and verdict")

type diffOp struct {
	kind    string
	id      string
	side    matcher.Side
	price   int64
	qty     int64
	ordKind matcher.OrderKind
	display int64
	newRem  int64
}

func nSide(s matcher.Side) naive.Side {
	if s == matcher.Buy {
		return naive.Buy
	}
	return naive.Sell
}

func nKind(k matcher.OrderKind) naive.Kind {
	switch k {
	case matcher.Iceberg:
		return naive.Iceberg
	case matcher.Hidden:
		return naive.Hidden
	default:
		return naive.Plain
	}
}

func nCode(c matcher.ErrorCode) naive.ErrorCode {
	switch c {
	case matcher.ErrDuplicateID:
		return naive.ErrDuplicate
	case matcher.ErrOrderNotFound:
		return naive.ErrNotFound
	case matcher.ErrOrderFinished:
		return naive.ErrFinished
	default:
		return naive.ErrInvalid
	}
}

func generateOps(rng *rand.Rand, n int) []diffOp {
	var ops []diffOp
	known := map[string]bool{}
	var idList []string
	counter := 0
	newID := func() string {
		counter++
		id := fmt.Sprintf("o%d", counter)
		known[id] = true
		idList = append(idList, id)
		return id
	}
	existingID := func() string {
		if len(idList) == 0 {
			return ""
		}
		return idList[rng.Intn(len(idList))]
	}

	for i := 0; i < n; i++ {
		roll := rng.Intn(100)
		switch {
		case roll < 62 || len(known) == 0:
			op := diffOp{kind: "submit", side: matcher.Side(rng.Intn(2)), price: int64(1 + rng.Intn(6)), qty: int64(1 + rng.Intn(12))}
			if rng.Intn(10) == 0 {
				op.id = existingID() // force duplicate sometimes
				if op.id == "" {
					op.id = newID()
				}
			} else {
				op.id = newID()
			}
			switch rng.Intn(3) {
			case 0:
				op.ordKind = matcher.Plain
			case 1:
				op.ordKind = matcher.Iceberg
				op.display = int64(1 + rng.Intn(int(op.qty)+2)) // may exceed qty -> invalid
			case 2:
				op.ordKind = matcher.Hidden
			}
			// Invalid parameters are injected deterministically below, so
			// generated quantities/prices stay valid here.
			ops = append(ops, op)
		case roll < 80:
			id := existingID()
			ops = append(ops, diffOp{kind: "cancel", id: id})
		default:
			id := existingID()
			ops = append(ops, diffOp{kind: "modify", id: id, newRem: int64(rng.Intn(20))})
		}
	}
	return ops
}

func assertBookEquiv(t *testing.T, e *matcher.Engine, m *naive.Engine, ctx string) {
	t.Helper()
	for _, side := range []matcher.Side{matcher.Buy, matcher.Sell} {
		for p := int64(1); p <= 6; p++ {
			g := e.VisibleQtyAt(side, p)
			w := m.VisibleQty(nSide(side), p)
			if g != w {
				t.Fatalf("%s: visible qty side=%d price=%d engine=%d naive=%d", ctx, side, p, g, w)
			}
		}
	}
}

func assertOrderEquiv(t *testing.T, id string, e *matcher.Engine, m *naive.Engine, ctx string) {
	t.Helper()
	gg, ok1 := e.GetOrder(id)
	mo, ok2 := m.State(id)
	if ok1 != ok2 {
		t.Fatalf("%s: presence mismatch for %s: %v vs %v", ctx, id, ok1, ok2)
	}
	if !ok1 {
		return
	}
	if gg.Seq != mo.Seq || gg.Total != mo.Total || gg.Filled != mo.Filled || gg.Remaining != mo.Remaining || gg.Visible != mo.Visible {
		t.Fatalf("%s: state mismatch for %s engine=%+v naive=%+v", ctx, id, gg, mo)
	}
	cancelStatus := gg.Status == matcher.Cancelled
	if cancelStatus != mo.Cancelled {
		t.Fatalf("%s: cancelled mismatch for %s: %+v vs %+v", ctx, id, gg, mo)
	}
	if gg.Filled+gg.Remaining != gg.Total {
		t.Fatalf("%s: conservation broken for %s: %+v", ctx, id, gg)
	}
}

func tradesEquiv(g []matcher.Trade, n []naive.Trade) bool {
	if len(g) != len(n) {
		return false
	}
	for i := range g {
		if g[i].TakerID != n[i].TakerID || g[i].MakerID != n[i].MakerID ||
			g[i].Price != n[i].Price || g[i].Quantity != n[i].Quantity {
			return false
		}
	}
	return true
}

func runDifferential(t *testing.T, seed int64, n int) {
	rng := rand.New(rand.NewSource(seed))
	ops := generateOps(rng, n)
	e := matcher.NewEngine()
	m := naive.New()

	var idPool []string
	seen := map[string]bool{}

	for i, op := range ops {
		ctx := fmt.Sprintf("seed=%d op=%d (%s)", seed, i, op.kind)
		if !seen[op.id] {
			seen[op.id] = true
			idPool = append(idPool, op.id)
		}

		var gTrades []matcher.Trade
		var nTrades []naive.Trade
		var gErr, nErr error

		switch op.kind {
		case "submit":
			var res matcher.Result
			res, gErr = e.Submit(matcher.NewOrderRequest{
				ID: op.id, Side: op.side, Price: op.price, Quantity: op.qty,
				Kind: op.ordKind, DisplaySize: op.display,
			})
			gTrades = res.Trades
			var nt []naive.Trade
			var ne *naive.Error
			nt, ne = m.Submit(naive.Request{
				ID: op.id, Side: nSide(op.side), Price: op.price, Quantity: op.qty,
				Kind: nKind(op.ordKind), DisplaySize: op.display,
			})
			nTrades = nt
			if ne != nil {
				nErr = ne
			}
		case "cancel":
			_, gErr = e.Cancel(op.id)
			if ne := m.Cancel(op.id); ne != nil {
				nErr = ne
			}
		case "modify":
			_, gErr = e.Modify(op.id, op.newRem)
			if ne := m.Modify(op.id, op.newRem); ne != nil {
				nErr = ne
			}
		}

		if (gErr != nil) != (nErr != nil) {
			t.Fatalf("%s: rejection mismatch: engine=%v naive=%v", ctx, gErr, nErr)
		}
		if gErr != nil {
			gc := codeOf(gErr)
			nc := nErr.(*naive.Error).Code
			if nCode(gc) != nc {
				t.Fatalf("%s: error code mismatch: engine=%d naive=%d", ctx, gc, nc)
			}
		} else if !tradesEquiv(gTrades, nTrades) {
			t.Fatalf("%s: trade stream mismatch:\n engine=%+v\n naive =%+v", ctx, gTrades, nTrades)
		}

		// Cumulative trade history must also agree.
		if !tradesEquiv(e.Trades(), m.Trades()) {
			t.Fatalf("%s: global trade history mismatch", ctx)
		}
		for _, id := range idPool {
			assertOrderEquiv(t, id, e, m, ctx)
		}
		assertBookEquiv(t, e, m, ctx)

		if *diffLog {
			verdict := "ACCEPT match"
			if gErr != nil {
				verdict = fmt.Sprintf("REJECT match code=%d", codeOf(gErr))
			}
			fmt.Printf("%s | input=%+v | trades=%d | verdict=%s\n", ctx, op, len(gTrades), verdict)
		}
	}
}

func TestDifferentialRandom(t *testing.T) {
	for seed := int64(1); seed <= 60; seed++ {
		runDifferential(t, seed, 400)
	}
}

// Explicitly invalid requests must be rejected identically by both engines
// and leave no state behind.
func TestDifferentialInvalidInputs(t *testing.T) {
	e := matcher.NewEngine()
	m := naive.New()
	invalid := []diffOp{
		{kind: "submit", id: "", side: matcher.Buy, price: 10, qty: 1},
		{kind: "submit", id: "a", side: matcher.Buy, price: 0, qty: 1},
		{kind: "submit", id: "b", side: matcher.Buy, price: 10, qty: 0},
		{kind: "submit", id: "c", side: matcher.Buy, price: 10, qty: 3, ordKind: matcher.Iceberg, display: 4},
		{kind: "submit", id: "d", side: matcher.Buy, price: 10, qty: 3, ordKind: matcher.Iceberg, display: 0},
		{kind: "cancel", id: "ghost"},
		{kind: "modify", id: "ghost", newRem: 0},
	}
	for i, op := range invalid {
		var gErr, nErr error
		switch op.kind {
		case "submit":
			_, gErr = e.Submit(matcher.NewOrderRequest{
				ID: op.id, Side: op.side, Price: op.price, Quantity: op.qty,
				Kind: op.ordKind, DisplaySize: op.display,
			})
			_, nErr = m.Submit(naive.Request{
				ID: op.id, Side: nSide(op.side), Price: op.price, Quantity: op.qty,
				Kind: nKind(op.ordKind), DisplaySize: op.display,
			})
		case "cancel":
			_, gErr = e.Cancel(op.id)
			nErr = m.Cancel(op.id)
		case "modify":
			_, gErr = e.Modify(op.id, op.newRem)
			nErr = m.Modify(op.id, op.newRem)
		}
		if gErr == nil || nErr == nil {
			t.Fatalf("case %d: expected rejection, got engine=%v naive=%v", i, gErr, nErr)
		}
		if nCode(codeOf(gErr)) != nErr.(*naive.Error).Code {
			t.Fatalf("case %d: code mismatch %d vs %d", i, codeOf(gErr), nErr.(*naive.Error).Code)
		}
	}
	if len(e.Trades()) != 0 || len(m.Trades()) != 0 {
		t.Fatalf("invalid operations produced trades")
	}
	if _, ok := e.BestBid(); ok {
		t.Fatalf("invalid operations changed the book")
	}
}
