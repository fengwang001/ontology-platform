package ontology

import (
	"fmt"
	"math/rand"
	"sort"
	"testing"
)

// naiveBidder / naiveAuction 是完全按题目规则逐步抄写的朴素参照实现，
// 与生产 Engine 不共享任何代码，用于差分测试。
type naiveBidder struct {
	bid, q, budget, h, f int64
	initial              int64
	seq                  int64
}

type naiveWinner struct {
	id      string
	p       int64
	qe      int64
	clicked bool
}

type naiveAuction struct {
	winners  []naiveWinner
	resolved bool
}

type naiveEngine struct {
	bidders map[string]*naiveBidder
	aucs    map[int64]*naiveAuction
	nextSeq int64
	nextAuc int64
}

func newNaive() *naiveEngine {
	return &naiveEngine{
		bidders: map[string]*naiveBidder{},
		aucs:    map[int64]*naiveAuction{},
	}
}

func naiveEffQ(q, f int64) int64 {
	d := f - 2
	if d < 0 {
		d = 0
	}
	if d > 5 {
		d = 5
	}
	v := q * (100 - 10*d) / 100
	if v < 1 {
		v = 1
	}
	return v
}

func (n *naiveEngine) register(id string, bid, q, budget int64) error {
	if id == "" || bid < 1 || bid > 1e6 || q < 1 || q > 1000 || budget < 1 || budget > 1e12 {
		return &OpError{ErrInvalidArgument}
	}
	if _, ok := n.bidders[id]; ok {
		return &OpError{ErrBidderExists}
	}
	n.nextSeq++
	n.bidders[id] = &naiveBidder{bid: bid, q: q, budget: budget, initial: budget, seq: n.nextSeq}
	return nil
}

type naivePart struct {
	id    string
	bid   int64
	qe, s int64
	seq   int64
}

func (n *naiveEngine) auction(k int, p int64) ([]naiveWinner, error) {
	if k < 1 || k > 100 || p < 1 || p > 1e6 {
		return nil, &OpError{ErrInvalidArgument}
	}
	parts := []naivePart{}
	for id, b := range n.bidders {
		if b.bid >= p && b.budget-b.h >= b.bid {
			qe := naiveEffQ(b.q, b.f)
			parts = append(parts, naivePart{id: id, bid: b.bid, qe: qe, s: b.bid * qe, seq: b.seq})
		}
	}
	if len(parts) == 0 {
		return nil, &OpError{ErrNoParticipants}
	}
	sort.Slice(parts, func(i, j int) bool {
		if parts[i].s != parts[j].s {
			return parts[i].s > parts[j].s
		}
		return parts[i].seq < parts[j].seq
	})
	m := k
	if m > len(parts) {
		m = len(parts)
	}
	n.nextAuc++
	auc := &naiveAuction{}
	out := make([]naiveWinner, 0, m)
	for j := 0; j < m; j++ {
		cur := parts[j]
		var price int64
		if j+1 < len(parts) {
			gsp := parts[j+1].s/cur.qe + 1
			if gsp < p {
				gsp = p
			}
			price = cur.bid
			if gsp < price {
				price = gsp
			}
		} else {
			price = p
		}
		rec := naiveWinner{id: cur.id, p: price, qe: cur.qe}
		auc.winners = append(auc.winners, rec)
		n.bidders[cur.id].h += price
		out = append(out, rec)
	}
	n.aucs[n.nextAuc] = auc
	return out, nil
}

func (n *naiveEngine) resolve(id int64, clicks []string) error {
	seen := map[string]bool{}
	for _, c := range clicks {
		if seen[c] {
			return &OpError{ErrInvalidArgument}
		}
		seen[c] = true
	}
	a, ok := n.aucs[id]
	if !ok {
		return &OpError{ErrAuctionNotFound}
	}
	if a.resolved {
		return &OpError{ErrAuctionResolved}
	}
	for _, c := range clicks {
		found := false
		for _, w := range a.winners {
			if w.id == c {
				found = true
				break
			}
		}
		if !found {
			return &OpError{ErrClickNotWinner}
		}
	}
	for wi := range a.winners {
		w := &a.winners[wi]
		b := n.bidders[w.id]
		b.h -= w.p
		if seen[w.id] {
			w.clicked = true
			b.budget -= w.p
			b.f = 0
			b.q = min64(1000, b.q+(1000-b.q)/8)
		} else {
			b.f++
			b.q = max64(1, b.q-ceilDiv(b.q, 16))
		}
	}
	a.resolved = true
	return nil
}

// op 是重放日志中的一条操作。
type op struct {
	kind   string // register / auction / resolve
	id     string
	bid    int64
	q      int64
	budget int64
	k      int
	p      int64
	aucID  int64
	clicks []string
}

// TestRandomDifferential 与朴素模拟对照 2000 组随机操作序列，
// 每组打印输入、输出与判定依据；失败时转储完整序列。
func TestRandomDifferential(t *testing.T) {
	const seeds = 2000
	const opsPerSeed = 60
	for seed := int64(0); seed < seeds; seed++ {
		rng := rand.New(rand.NewSource(seed))
		eng := NewEngine()
		nav := newNaive()
		var log []op
		var ids []string
		verdict := "与朴素模拟逐步一致"

		failf := func(format string, args ...any) {
			t.Helper()
			t.Fatalf("seed=%d %s\n输入序列:\n%s\n判定依据: %s",
				seed, fmt.Sprintf(format, args...), dumpOps(log), verdict)
		}

		for step := 0; step < opsPerSeed; step++ {
			roll := rng.Intn(100)
			switch {
			case roll < 45 || len(ids) == 0:
				// Register：20% 概率复用已有 id 以覆盖“已存在”拒绝。
				var o op
				if len(ids) > 0 && rng.Intn(5) == 0 {
					id := ids[rng.Intn(len(ids))]
					o = op{kind: "register", id: id,
						bid: rngInt(rng, 1, 1e6), q: rngInt(rng, 1, 1000),
						budget: rngInt(rng, 1, 1e12)}
				} else {
					id := fmt.Sprintf("b%d-%d", step, rng.Intn(100000))
					o = op{kind: "register", id: id,
						bid: rngInt(rng, 1, 1e6), q: rngInt(rng, 1, 1000),
						budget: rngInt(rng, 1, 1e12)}
					// 偶尔注入非法参数。
					if rng.Intn(10) == 0 {
						switch rng.Intn(4) {
						case 0:
							o.id = ""
						case 1:
							if rng.Intn(2) == 0 {
								o.bid = 0
							} else {
								o.bid = 1e6 + 1
							}
						case 2:
							if rng.Intn(2) == 0 {
								o.q = 0
							} else {
								o.q = 1001
							}
						case 3:
							o.budget = 1e12 + 1
						}
					}
					ids = append(ids, id)
				}
				log = append(log, o)
				e1 := eng.Register(o.id, o.bid, o.q, o.budget)
				e2 := nav.register(o.id, o.bid, o.q, o.budget)
				if errKind(e1) != errKind(e2) {
					failf("step=%d register 拒绝原因不一致 engine=%v naive=%v", step, e1, e2)
				}
			case roll < 80:
				k := 1 + rng.Intn(100)
				p := rngInt(rng, 1, 1e6)
				o := op{kind: "auction", k: k, p: p}
				log = append(log, o)
				w1, e1 := eng.Auction(k, p)
				w2, e2 := nav.auction(k, p)
				if errKind(e1) != errKind(e2) {
					failf("step=%d auction 拒绝原因不一致 engine=%v naive=%v", step, e1, e2)
				}
				if e1 == nil {
					if len(w1) != len(w2) {
						failf("step=%d winner 数量不一致 %d vs %d", step, len(w1), len(w2))
					}
					for i := range w1 {
						if w1[i].ID != w2[i].id || w1[i].P != w2[i].p || w1[i].Qe != w2[i].qe {
							failf("step=%d winner[%d] 不一致 engine=%+v naive=%+v", step, i, w1[i], w2[i])
						}
					}
				}
			default:
				// Resolve：随机挑一个拍卖号（可能不存在）。
				nAuc := int(nav.nextAuc)
				aucID := int64(rng.Intn(nAuc+3) + 1) // 含越界 id
				var clicks []string
				if a, ok := nav.aucs[aucID]; ok && !a.resolved {
					for _, w := range a.winners {
						if rng.Intn(2) == 0 {
							clicks = append(clicks, w.id)
						}
					}
					// 偶尔塞入非赢家或重复 id。
					switch rng.Intn(8) {
					case 0:
						clicks = append(clicks, "ghost-id")
					case 1:
						if len(clicks) > 0 {
							clicks = append(clicks, clicks[0])
						}
					}
				}
				o := op{kind: "resolve", aucID: aucID, clicks: clicks}
				log = append(log, o)
				e1 := eng.Resolve(aucID, clicks)
				e2 := nav.resolve(aucID, clicks)
				if errKind(e1) != errKind(e2) {
					failf("step=%d resolve(%d,%v) 拒绝原因不一致 engine=%v naive=%v", step, aucID, clicks, e1, e2)
				}
			}

			// 每步后对照全部竞价者状态与拍卖不变量。
			if diff := diffStates(eng, nav); diff != "" {
				verdict = "状态快照出现差异"
				failf("step=%d %s", step, diff)
			}
		}
		t.Logf("seed=%d 输入=%d 条随机操作（Register/Auction/Resolve），输出与错误原因逐条一致，终态全量快照一致 => %s",
			seed, opsPerSeed, verdict)
	}
}

func rngInt(rng *rand.Rand, lo, hi int64) int64 { return lo + rng.Int63n(hi-lo+1) }

func diffStates(e *Engine, n *naiveEngine) string {
	if len(e.bidders) != len(n.bidders) {
		return fmt.Sprintf("bidder 数量 %d vs %d", len(e.bidders), len(n.bidders))
	}
	for id, nb := range n.bidders {
		eb, ok := e.bidders[id]
		if !ok {
			return fmt.Sprintf("engine 缺少竞价者 %s", id)
		}
		if eb.bid != nb.bid || eb.q != nb.q || eb.budget != nb.budget ||
			eb.h != nb.h || eb.f != nb.f || eb.seq != nb.seq {
			return fmt.Sprintf("竞价者 %s 状态不一致 engine=%+v naive=%+v", id, eb, nb)
		}
		if eb.h < 0 || eb.h > eb.budget {
			return fmt.Sprintf("竞价者 %s 违反 0<=h<=budget: h=%d budget=%d", id, eb.h, eb.budget)
		}
		// h 必须恰等于该竞价者全部未结算中标 p 之和。
		var wantH int64
		for _, a := range e.auctions {
			if a.resolved {
				continue
			}
			for _, w := range a.winners {
				if w.bidderSeq == eb.seq {
					wantH += w.p
				}
			}
		}
		if eb.h != wantH {
			return fmt.Sprintf("竞价者 %s h=%d 不等于未结算占用之和 %d", id, eb.h, wantH)
		}
	}
	// 累计扣费不变量：每个竞价者的已扣费用（initial-budget）之和
	// 恰等于全部已结算点击赢家的 p 之和；两实现逐一对照。
	var spentE, spentN, clickedE, clickedN int64
	for id, eb := range e.bidders {
		spentE += eb.initial - eb.budget
		nb := n.bidders[id]
		spentN += nb.initial - nb.budget
	}
	if spentE != spentN {
		return fmt.Sprintf("累计扣费不一致 engine=%d naive=%d", spentE, spentN)
	}
	if len(e.auctions) != len(n.aucs) {
		return fmt.Sprintf("拍卖数量 %d vs %d", len(e.auctions), len(n.aucs))
	}
	for _, a := range e.auctions {
		if a.resolved {
			for _, w := range a.winners {
				if w.clicked {
					clickedE += w.p
				}
			}
		}
	}
	for _, a := range n.aucs {
		if a.resolved {
			for _, w := range a.winners {
				if w.clicked {
					clickedN += w.p
				}
			}
		}
	}
	if clickedE != spentE {
		return fmt.Sprintf("engine 累计扣费 %d 不等于点击总额 %d", spentE, clickedE)
	}
	if clickedN != spentN {
		return fmt.Sprintf("naive 累计扣费 %d 不等于点击总额 %d", spentN, clickedN)
	}
	for id, na := range n.aucs {
		ea, ok := e.auctions[id]
		if !ok {
			return fmt.Sprintf("engine 缺少拍卖 %d", id)
		}
		if ea.resolved != na.resolved || len(ea.winners) != len(na.winners) {
			return fmt.Sprintf("拍卖 %d 元数据不一致", id)
		}
		for i, w := range ea.winners {
			nw := na.winners[i]
			if w.id != nw.id || w.p != nw.p || w.qe != nw.qe || w.clicked != nw.clicked {
				return fmt.Sprintf("拍卖 %d winner[%d] 不一致", id, i)
			}
			if w.p < 1 {
				return fmt.Sprintf("拍卖 %d winner[%d] 非法价格 %d", id, i, w.p)
			}
		}
	}
	return ""
}

func dumpOps(ops []op) string {
	out := ""
	for i, o := range ops {
		switch o.kind {
		case "register":
			out += fmt.Sprintf("  %d Register(%q,%d,%d,%d)\n", i, o.id, o.bid, o.q, o.budget)
		case "auction":
			out += fmt.Sprintf("  %d Auction(K=%d,P=%d)\n", i, o.k, o.p)
		case "resolve":
			out += fmt.Sprintf("  %d Resolve(%d,%v)\n", i, o.aucID, o.clicks)
		}
	}
	return out
}
