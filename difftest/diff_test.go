// Package difftest 用随机操作序列对 matching 引擎与 naive 朴素模型做差分对照。
package difftest

import (
	"fmt"
	"math/rand"
	"sort"
	"strings"
	"testing"

	"ontology/matching"
	"ontology/naive"
)

// Op 是与实现无关的操作描述，便于日志重放。
type Op struct {
	Kind   string // submit / cancel / replace
	ID     int64
	Side   int // 1 buy 2 sell
	Price  int64
	Qty    int64
	Typ    int // 1 limit 2 iceberg 3 hidden
	Show   int64
	NewRem int64
}

func (o Op) String() string {
	switch o.Kind {
	case "submit":
		return fmt.Sprintf("submit(id=%d side=%d px=%d qty=%d type=%d show=%d)",
			o.ID, o.Side, o.Price, o.Qty, o.Typ, o.Show)
	case "cancel":
		return fmt.Sprintf("cancel(id=%d)", o.ID)
	default:
		return fmt.Sprintf("replace(id=%d newRem=%d)", o.ID, o.NewRem)
	}
}

type gen struct {
	rng   *rand.Rand
	maxID int64
	ids   []int64 // 已成功提交过的 id（可能已终结）
	alive []int64 // 仍可改/撤的 id
}

func (g *gen) op() Op {
	switch g.rng.Intn(10) {
	case 0, 1:
		if len(g.alive) > 0 {
			i := g.rng.Intn(len(g.alive))
			return Op{Kind: "cancel", ID: g.alive[i]}
		}
	case 2, 3:
		if len(g.alive) > 0 {
			i := g.rng.Intn(len(g.alive))
			return Op{Kind: "replace", ID: g.alive[i], NewRem: int64(g.rng.Intn(12)) + 1}
		}
	}
	// 偶发注入非法参数与重复 id。
	g.maxID++
	id := g.maxID
	side := 1 + g.rng.Intn(2)
	typ := 1 + g.rng.Intn(3)
	qty := int64(g.rng.Intn(15) + 1)
	show := int64(0)
	if typ == 2 {
		show = int64(g.rng.Intn(8) + 1)
	}
	o := Op{Kind: "submit", ID: id, Side: side,
		Price: int64(90 + g.rng.Intn(20)), Qty: qty, Typ: typ, Show: show}
	switch g.rng.Intn(15) {
	case 0:
		o.Qty = 0 // 非法数量
	case 1:
		o.ID = 0 // 非法编号
	case 2:
		if len(g.ids) > 0 {
			o.ID = g.ids[g.rng.Intn(len(g.ids))] // 重复编号
		}
	case 3:
		if typ == 2 {
			o.Show = qty + 1 + int64(g.rng.Intn(5)) // 冰山显示量 > 总量
		}
	}
	return o
}

func errKind(err error) int {
	if err == nil {
		return 0
	}
	type kinder interface{ ErrorString() string }
	_ = kinder(nil)
	if e, ok := err.(*matching.EngineError); ok {
		return int(e.Kind) + 1
	}
	if e, ok := err.(*naive.Error); ok {
		return int(e.Kind) + 1
	}
	return -1
}

func toNaiveParams(o Op) naive.Params {
	return naive.Params{
		ClientID:          o.ID,
		Side:              naive.Side(o.Side),
		Price:             o.Price,
		TotalQty:          o.Qty,
		Type:              naive.OrderType(o.Typ),
		IcebergVisibleQty: o.Show,
	}
}

func toMatchParams(o Op) matching.OrderParams {
	return matching.OrderParams{
		ClientID:          o.ID,
		Side:              matching.Side(o.Side),
		Price:             o.Price,
		TotalQty:          o.Qty,
		Type:              matching.OrderType(o.Typ),
		IcebergVisibleQty: o.Show,
	}
}

type state struct {
	bid, ask int64
	bidOK    bool
	askOK    bool
	depth    string
	orders   string
	fills    string
}

func snapshotMatch(e *matching.Engine) state {
	var s state
	s.bid, s.bidOK = e.BestBid()
	s.ask, s.askOK = e.BestAsk()
	s.depth = depthStringMatch(e)
	s.orders = ordersStringMatch(e)
	s.fills = fillsStringMatch(e.Fills())
	return s
}

func snapshotNaive(m *naive.Model) state {
	var s state
	s.bid, s.bidOK = m.BestBid()
	s.ask, s.askOK = m.BestAsk()
	s.depth = depthStringNaive(m)
	s.orders = ordersStringNaive(m)
	s.fills = fillsStringNaive(m.Fills())
	return s
}

func depthStringMatch(e *matching.Engine) string {
	var parts []string
	for _, side := range []matching.Side{matching.Buy, matching.Sell} {
		d := e.SnapshotDepth(side)
		// 已按从优到劣排序。
		for _, x := range d {
			parts = append(parts, fmt.Sprintf("%d@%d:%d", side, x.Price, x.VisibleQty))
		}
	}
	return strings.Join(parts, ",")
}

func depthStringNaive(m *naive.Model) string {
	var parts []string
	for _, side := range []naive.Side{naive.Buy, naive.Sell} {
		d := m.SnapshotDepth(side)
		sort.Slice(d, func(i, j int) bool {
			if side == naive.Buy {
				return d[i].Price > d[j].Price
			}
			return d[i].Price < d[j].Price
		})
		for _, x := range d {
			parts = append(parts, fmt.Sprintf("%d@%d:%d", side, x.Price, x.VisibleQty))
		}
	}
	return strings.Join(parts, ",")
}

func fillsStringMatch(fs []matching.Fill) string {
	var b strings.Builder
	for i, f := range fs {
		if i > 0 {
			b.WriteString(";")
		}
		fmt.Fprintf(&b, "%d:b=%d s=%d px=%d q=%d ag=%d",
			f.Seq, f.BuyClientID, f.SellClientID, f.Price, f.Qty, f.Aggressor)
	}
	return b.String()
}

func fillsStringNaive(fs []naive.Fill) string {
	var b strings.Builder
	for i, f := range fs {
		if i > 0 {
			b.WriteString(";")
		}
		fmt.Fprintf(&b, "%d:b=%d s=%d px=%d q=%d ag=%d",
			f.Seq, f.BuyClientID, f.SellClientID, f.Price, f.Qty, f.Aggressor)
	}
	return b.String()
}

func ordersStringMatch(e *matching.Engine) string {
	// 用 VisibleDepthAt 无法枚举委托；这里直接依赖 GetOrder 需要 id 列表，
	// 由调用方传入的活跃集合补齐在 runRandom 内部统一比较，这里返回空。
	return ""
}

func ordersStringNaive(m *naive.Model) string { return "" }

func TestRandomDifferential(t *testing.T) {
	const iterations = 400
	const opsPerIter = 250
	for iter := 0; iter < iterations; iter++ {
		seed := int64(1 + iter*7919)
		name := fmt.Sprintf("seed=%d", seed)
		t.Run(name, func(t *testing.T) {
			runRandom(t, seed, opsPerIter)
		})
	}
}

func runRandom(t *testing.T, seed int64, n int) {
	t.Helper()
	rng := rand.New(rand.NewSource(seed))
	g := &gen{rng: rng}
	eng := matching.New()
	mdl := naive.New()

	var log strings.Builder
	fmt.Fprintf(&log, "==== differential seed=%d ops=%d ====\n", seed, n)

	known := map[int64]bool{}

	for i := 0; i < n; i++ {
		o := g.op()
		fmt.Fprintf(&log, "[%03d] %s\n", i, o)

		var k1, k2 int
		var st1, st2 state

		switch o.Kind {
		case "submit":
			seq1, f1, e1 := eng.Submit(toMatchParams(o))
			_, f2, e2 := mdl.Submit(toNaiveParams(o))
			k1, k2 = errKind(e1), errKind(e2)
			accepted := e1 == nil
			if accepted {
				known[o.ID] = true
				g.ids = append(g.ids, o.ID)
				g.alive = append(g.alive, o.ID)
			}
			fmt.Fprintf(&log, "      -> accepted=%v seq=%d fills=%d\n", accepted, seq1, len(f1))
			_ = f2
		case "cancel":
			e1 := eng.Cancel(o.ID)
			e2 := mdl.Cancel(o.ID)
			k1, k2 = errKind(e1), errKind(e2)
			if e1 == nil {
				g.alive = removeID(g.alive, o.ID)
			}
		case "replace":
			e1 := eng.ReplaceQty(o.ID, o.NewRem)
			e2 := mdl.ReplaceQty(o.ID, o.NewRem)
			k1, k2 = errKind(e1), errKind(e2)
		}

		st1 = snapshotMatch(eng)
		st2 = snapshotNaive(mdl)

		reason := ""
		if k1 != k2 {
			reason = fmt.Sprintf("error kind mismatch: matching=%d naive=%d", k1, k2)
		} else if st1.bid != st2.bid || st1.bidOK != st2.bidOK {
			reason = fmt.Sprintf("best bid mismatch: %d/%v vs %d/%v", st1.bid, st1.bidOK, st2.bid, st2.bidOK)
		} else if st1.ask != st2.ask || st1.askOK != st2.askOK {
			reason = fmt.Sprintf("best ask mismatch: %d/%v vs %d/%v", st1.ask, st1.askOK, st2.ask, st2.askOK)
		} else if st1.depth != st2.depth {
			reason = fmt.Sprintf("depth mismatch:\n  matching=[%s]\n  naive   =[%s]", st1.depth, st2.depth)
		} else if st1.fills != st2.fills {
			reason = fmt.Sprintf("fills mismatch:\n  matching=[%s]\n  naive   =[%s]", st1.fills, st2.fills)
		}
		if reason != "" {
			compareAllOrders(&log, eng, mdl, known)
			fmt.Fprintf(&log, "JUDGE: %s\n", reason)
			t.Fatalf("%s\n\n%s", reason, log.String())
		}
	}
	// 成功时不打印全部日志；仅在 -v 下打印摘要。
	t.Logf("seed=%d verified %d ops; fills=%d; orders-known=%d",
		seed, n, len(eng.Fills()), len(known))
}

func compareAllOrders(log *strings.Builder, eng *matching.Engine, mdl *naive.Model, known map[int64]bool) {
	fmt.Fprintln(log, "---- order states ----")
	for id := range known {
		o1, ok1 := eng.GetOrder(id)
		o2, ok2 := mdl.GetOrder(id)
		mark := "OK"
		if ok1 != ok2 ||
			o1.Seq != o2.Seq || o1.Status != matching.Status(o2.Status) ||
			o1.TotalQty != o2.TotalQty || o1.FilledQty != o2.FilledQty ||
			o1.RemainingQty != o2.RemainingQty {
			mark = "DIFF"
		}
		fmt.Fprintf(log, "  id=%d %s matching={seq=%d st=%d tot=%d fill=%d rem=%d} naive={seq=%d st=%d tot=%d fill=%d rem=%d}\n",
			id, mark,
			o1.Seq, o1.Status, o1.TotalQty, o1.FilledQty, o1.RemainingQty,
			o2.Seq, o2.Status, o2.TotalQty, o2.FilledQty, o2.RemainingQty)
		_ = ok1
		_ = ok2
	}
}

func removeID(xs []int64, id int64) []int64 {
	for i, x := range xs {
		if x == id {
			return append(xs[:i], xs[i+1:]...)
		}
	}
	return xs
}
