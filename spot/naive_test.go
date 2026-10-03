package spot

// 朴素模拟：严格按题目规则逐步写成，与正式实现对照。
// 刻意采用不同写法：稳定排序排名、逐小时循环计费、线性扫描价格历史。

import (
	"fmt"
	"math/big"
	"math/rand"
	"sort"
	"strings"
	"testing"
)

type naiveReq struct {
	id         int64
	bid        int64
	terminated bool
	running    bool
	segStart   int64
	bill       *big.Int
}

type naiveMarket struct {
	k    int
	pmin int64
	reqs []*naiveReq
	byID map[int64]*naiveReq
	maxT int64
	hist [][2]int64
}

func newNaive(k int, pmin int64) *naiveMarket {
	return &naiveMarket{
		k:    k,
		pmin: pmin,
		byID: make(map[int64]*naiveReq),
		hist: [][2]int64{{0, pmin}},
	}
}

func (n *naiveMarket) priceAt(x int64) int64 {
	for i := len(n.hist) - 1; i >= 0; i-- {
		if n.hist[i][0] <= x {
			return n.hist[i][1]
		}
	}
	return n.pmin
}

func (n *naiveMarket) record(t, price int64) {
	if len(n.hist) > 0 && n.hist[len(n.hist)-1][0] == t {
		n.hist[len(n.hist)-1][1] = price
		return
	}
	n.hist = append(n.hist, [2]int64{t, price})
}

func (n *naiveMarket) settle(r *naiveReq, e int64, user bool) *big.Int {
	s := r.segStart
	r.running = false
	dur := e - s
	var hours int64
	if user {
		hours = (dur + HourSeconds - 1) / HourSeconds
	} else {
		hours = dur / HourSeconds
	}
	fee := new(big.Int)
	for k := int64(0); k < hours; k++ {
		fee.Add(fee, big.NewInt(n.priceAt(s+HourSeconds*k)))
	}
	r.bill.Add(r.bill, fee)
	return fee
}

func (n *naiveMarket) recompute(t int64) (ends, starts []Event) {
	var alive []*naiveReq
	for _, r := range n.reqs {
		if !r.terminated {
			alive = append(alive, r)
		}
	}
	sort.SliceStable(alive, func(i, j int) bool {
		return alive[i].bid > alive[j].bid
	})
	topK := make(map[*naiveReq]bool, n.k)
	for i := 0; i < len(alive) && i < n.k; i++ {
		topK[alive[i]] = true
	}
	for _, r := range alive {
		if r.running && !topK[r] {
			fee := n.settle(r, t, false)
			ends = append(ends, Event{Kind: EventEnd, ID: r.id,
				Reason: ReasonMarketInterrupt, Start: r.segStart, End: t, Fee: fee})
		} else if !r.running && topK[r] {
			r.running = true
			r.segStart = t
			starts = append(starts, Event{Kind: EventStart, ID: r.id})
		}
	}
	price := n.pmin
	if len(alive) > n.k {
		price = alive[n.k].bid
	}
	if price != n.priceAt(t) {
		n.record(t, price)
	}
	return ends, starts
}

func naiveMerge(ends, starts []Event) []Event {
	sort.Slice(ends, func(i, j int) bool { return ends[i].ID < ends[j].ID })
	sort.Slice(starts, func(i, j int) bool { return starts[i].ID < starts[j].ID })
	return append(ends, starts...)
}

func (n *naiveMarket) request(id, bid, t int64) ([]Event, error) {
	if id < MinID || id > MaxID || bid < n.pmin || bid > MaxPrice ||
		t < MinTime || t > MaxTime {
		return nil, ErrInvalidArgument
	}
	if t < n.maxT {
		return nil, ErrClockRegression
	}
	if _, ok := n.byID[id]; ok {
		return nil, ErrDuplicateID
	}
	n.maxT = t
	r := &naiveReq{id: id, bid: bid, bill: new(big.Int)}
	n.reqs = append(n.reqs, r)
	n.byID[id] = r
	ends, starts := n.recompute(t)
	return naiveMerge(ends, starts), nil
}

func (n *naiveMarket) terminate(id, t int64) ([]Event, error) {
	if id < MinID || id > MaxID || t < MinTime || t > MaxTime {
		return nil, ErrInvalidArgument
	}
	if t < n.maxT {
		return nil, ErrClockRegression
	}
	r, ok := n.byID[id]
	if !ok {
		return nil, ErrNotFound
	}
	if r.terminated {
		return nil, ErrAlreadyTerminated
	}
	n.maxT = t
	r.terminated = true
	var ends []Event
	if r.running {
		fee := n.settle(r, t, true)
		ends = append(ends, Event{Kind: EventEnd, ID: r.id,
			Reason: ReasonUserTerminate, Start: r.segStart, End: t, Fee: fee})
	}
	re, st := n.recompute(t)
	ends = append(ends, re...)
	return naiveMerge(ends, st), nil
}

func (n *naiveMarket) setCapacity(k2 int, t int64) ([]Event, error) {
	if k2 < MinCapacity || k2 > MaxCapacity || t < MinTime || t > MaxTime {
		return nil, ErrInvalidArgument
	}
	if t < n.maxT {
		return nil, ErrClockRegression
	}
	n.maxT = t
	n.k = k2
	ends, starts := n.recompute(t)
	return naiveMerge(ends, starts), nil
}

func (n *naiveMarket) bill(id int64) *big.Int {
	if r, ok := n.byID[id]; ok {
		return new(big.Int).Set(r.bill)
	}
	return new(big.Int)
}

func formatEvents(evs []Event) string {
	var b strings.Builder
	for _, e := range evs {
		if e.Kind == EventStart {
			fmt.Fprintf(&b, " start(%d)", e.ID)
		} else {
			reason := "user"
			if e.Reason == ReasonMarketInterrupt {
				reason = "interrupt"
			}
			fmt.Fprintf(&b, " end(%d,%s,[%d,%d),fee=%s)",
				e.ID, reason, e.Start, e.End, e.Fee)
		}
	}
	if b.Len() == 0 {
		return " (无事件)"
	}
	return b.String()
}

func sameEvents(a, b []Event) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i].Kind != b[i].Kind || a[i].ID != b[i].ID {
			return false
		}
		if a[i].Kind == EventEnd {
			if a[i].Reason != b[i].Reason || a[i].Start != b[i].Start ||
				a[i].End != b[i].End || a[i].Fee.Cmp(b[i].Fee) != 0 {
				return false
			}
		}
	}
	return true
}

// TestAgainstNaiveSimulation 对 2000 组随机操作序列，
// 逐操作对比正式实现与朴素模拟的错误与事件清单，
// 并在序列末尾对比所有 id 的 Bill 与若干 price 探针。
// 日志打印输入（操作序列）、输出（事件与费用）与判定依据。
func TestAgainstNaiveSimulation(t *testing.T) {
	const sequences = 2000
	dts := []int64{0, 0, 1, 900, 1800, 3599, 3600, 3601, 5000}
	for seq := 0; seq < sequences; seq++ {
		rng := rand.New(rand.NewSource(int64(seq)))
		k := 1 + rng.Intn(4)
		pmin := int64(1 + rng.Intn(50))
		m, err := NewMarket(k, pmin)
		if err != nil {
			t.Fatalf("序列 %d: NewMarket(%d,%d) 失败: %v", seq, k, pmin, err)
		}
		n := newNaive(k, pmin)

		var log strings.Builder
		fmt.Fprintf(&log, "序列 %d 输入: K=%d Pmin=%d\n", seq, k, pmin)

		numOps := 20 + rng.Intn(40)
		var tNow int64
		diverged := false
		for op := 0; op < numOps && !diverged; op++ {
			tNow += dts[rng.Intn(len(dts))]
			tOp := tNow
			if rng.Intn(50) == 0 && tOp > 0 {
				tOp-- // 偶发时钟回退，验证拒绝路径
			}

			var gotEv, wantEv []Event
			var gotErr, wantErr error
			var desc string
			switch kind := rng.Intn(100); {
			case kind < 50: // Request
				id := int64(rng.Intn(10))
				if rng.Intn(50) == 0 {
					id = MaxID + 1 // 偶发非法 id
				}
				bid := pmin - 1 + int64(rng.Intn(80)) // 偶发低于底价
				desc = fmt.Sprintf("Request(id=%d, bid=%d, t=%d)", id, bid, tOp)
				gotEv, gotErr = m.Request(id, bid, tOp)
				wantEv, wantErr = n.request(id, bid, tOp)
			case kind < 75: // Terminate
				id := int64(rng.Intn(12))
				desc = fmt.Sprintf("Terminate(id=%d, t=%d)", id, tOp)
				gotEv, gotErr = m.Terminate(id, tOp)
				wantEv, wantErr = n.terminate(id, tOp)
			default: // SetCapacity
				k2 := rng.Intn(7)
				if rng.Intn(50) == 0 {
					k2 = MaxCapacity + 1 // 偶发非法容量
				}
				desc = fmt.Sprintf("SetCapacity(k2=%d, t=%d)", k2, tOp)
				gotEv, gotErr = m.SetCapacity(k2, tOp)
				wantEv, wantErr = n.setCapacity(k2, tOp)
			}

			fmt.Fprintf(&log, "  op %d: %s\n    正式实现: err=%v 事件%s\n    朴素模拟: err=%v 事件%s\n",
				op, desc, gotErr, formatEvents(gotEv), wantErr, formatEvents(wantEv))

			if gotErr != wantErr {
				t.Fatalf("序列 %d op %d %s: 错误不一致 got=%v want=%v\n%s",
					seq, op, desc, gotErr, wantErr, log.String())
			}
			if gotErr == nil && !sameEvents(gotEv, wantEv) {
				t.Fatalf("序列 %d op %d %s: 事件不一致\n%s",
					seq, op, desc, log.String())
			}
		}

		// 序列末尾对比 Bill 与 price 探针。
		for id := int64(0); id < 12; id++ {
			got, want := m.Bill(id), n.bill(id)
			fmt.Fprintf(&log, "  Bill(%d): 正式=%s 朴素=%s\n", id, got, want)
			if got.Cmp(want) != 0 {
				t.Fatalf("序列 %d: Bill(%d) 不一致 got=%s want=%s\n%s",
					seq, id, got, want, log.String())
			}
		}
		for _, x := range []int64{0, 1, 1800, 3600, tNow} {
			got, want := m.PriceAt(x), n.priceAt(x)
			fmt.Fprintf(&log, "  price(%d): 正式=%d 朴素=%d\n", x, got, want)
			if got != want {
				t.Fatalf("序列 %d: price(%d) 不一致 got=%d want=%d\n%s",
					seq, x, got, want, log.String())
			}
		}

		// 判定依据：逐操作对比错误与事件清单（先结束后开始、各自按 id 升序、
		// 结束事件含段区间与费用），末尾对比全部 Bill 与 price 探针。
		t.Logf("%s判定依据: 逐操作错误+事件清单一致, 末尾 Bill 与 price 探针一致 => 序列 %d 通过",
			log.String(), seq)
	}
}
