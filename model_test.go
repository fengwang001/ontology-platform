package baggage

import (
	"fmt"
	"math/rand"
	"testing"
)

// naiveResult 是按题面规则独立重写的朴素模型，刻意不复用生产代码的路由/计费函数。
type naiveResult struct {
	fees    []Money // 各段托运费用
	carrier []string
	claims  map[string][]string
	reject  int // 第一件超限行李全局序号；0 表示未拒收
}

// naiveCompute 朴素对照：逐步显式扫描中转站、切段、逐件判定。
func naiveCompute(req CheckInRequest, airports map[string]Airport,
	carriers map[string]Carrier, tiers map[string]map[string]Tier, cfg Config,
	logf func(format string, args ...any)) naiveResult {
	logf("[naive] 输入 航段=%d 交运方=%d 时刻=%d", len(req.Itinerary), len(req.Parties), req.At)

	// 1) 逐站扫描断点。
	type cut struct {
		at int
	}
	var cuts []cut
	for i := 0; i+1 < len(req.Itinerary); i++ {
		st := req.Itinerary[i].To
		ap := airports[st]
		lay := int64(req.Itinerary[i+1].DepartsAt - req.Itinerary[i].ArrivesAt)
		why := ""
		if ap.Customs {
			why = "清关"
		} else if req.Itinerary[i].PNR != req.Itinerary[i+1].PNR {
			why = "订座变更"
		} else if lay < cfg.MinConnect || lay > cfg.MaxConnect {
			why = fmt.Sprintf("停留%d出界[%d,%d]", lay, cfg.MinConnect, cfg.MaxConnect)
		}
		if why != "" {
			cuts = append(cuts, cut{i})
			logf("[naive] 断点 @%s 原因=%s", st, why)
		}
	}

	// 2) 切段。
	type leg struct {
		from, to int
		pnr      string
	}
	var legs []leg
	start := 0
	for _, c := range cuts {
		legs = append(legs, leg{start, c.at, req.Itinerary[start].PNR})
		start = c.at + 1
	}
	legs = append(legs, leg{start, len(req.Itinerary) - 1, req.Itinerary[start].PNR})

	res := naiveResult{claims: make(map[string][]string)}

	// 3) 逐段确定适用承运人并计费；行李全局序号按交运方顺序展开。
	type bag struct {
		pnr, pax string
		w        Weight
		idx      int
	}
	var all []bag
	idx := 0
	for _, p := range req.Parties {
		for _, w := range p.Weights {
			idx++
			all = append(all, bag{p.PNR, p.PassengerID, w, idx})
		}
	}

	for li, lg := range legs {
		// 适用承运人：第一个跨区域航段，否则首段。
		appl := req.Itinerary[lg.from].CarrierID
		for i := lg.from; i <= lg.to; i++ {
			if airports[req.Itinerary[i].From].Region != airports[req.Itinerary[i].To].Region {
				appl = req.Itinerary[i].CarrierID
				break
			}
		}
		c := carriers[appl]
		res.carrier = append(res.carrier, appl)
		logf("[naive] 段%d %s->%s pnr=%s 适用=%s(%s)", li,
			req.Itinerary[lg.from].From, req.Itinerary[lg.to].To, lg.pnr, appl, modeName(c.Mode))

		var fee Money
		usedPiece := map[string]int{}
		for _, b := range all {
			if b.pnr != lg.pnr {
				continue
			}
			if b.w > c.AbsWeight {
				logf("[naive] 拒收 第一件超限行李 全局序号=%d 重量=%d 绝对上限=%d",
					b.idx, b.w, c.AbsWeight)
				res.reject = b.idx
				return res
			}
			if c.Mode == ModePiece {
				free := c.FreePieces + cfg.TierExtraPiece[tiers[b.pnr][b.pax]]
				if usedPiece[b.pax] >= free {
					fee += c.ExtraPieceRate
					logf("[naive] 件#%d 超件 +%d", b.idx, c.ExtraPieceRate)
				}
				usedPiece[b.pax]++
				if b.w > c.PieceFreeWeight {
					fee += c.OverweightRate
					logf("[naive] 件#%d 超重 +%d", b.idx, c.OverweightRate)
				}
			}
		}
		if c.Mode == ModeWeight {
			ownW := map[string]Weight{}
			for _, b := range all {
				if b.pnr == lg.pnr {
					ownW[b.pax] += b.w
				}
			}
			var excess, spare Weight
			for pax, own := range ownW {
				extra := cfg.TierExtraWeight[tiers[lg.pnr][pax]]
				base := own - extra
				if base < 0 {
					base = 0
				}
				if base > c.FreeWeight {
					excess += base - c.FreeWeight
					base = c.FreeWeight
				}
				spare += c.FreeWeight - base
			}
			charge := excess - spare
			if charge < 0 {
				charge = 0
			}
			fee = Money(charge) * c.PerUnitRate
			logf("[naive] 计重 超额=%d 共享余=%d 计费量=%d 费用=%d", excess, spare, charge, fee)
		}
		res.fees = append(res.fees, fee)
	}

	// 4) 提取点：按各段端点序列，按 PNR 展开。
	for _, lg := range legs {
		pnr := lg.pnr
		if len(res.claims[pnr]) == 0 {
			res.claims[pnr] = append(res.claims[pnr], req.Itinerary[lg.from].From)
		}
		res.claims[pnr] = append(res.claims[pnr], req.Itinerary[lg.to].To)
	}
	logf("[naive] 输出 段费=%v 承运人=%v 拒收=%d", res.fees, res.carrier, res.reject)
	return res
}

func modeName(m AllowanceMode) string {
	if m == ModePiece {
		return "计件"
	}
	return "计重"
}

// worldSnap 朴素模型需要的纯数据快照。
type worldSnap struct {
	airports map[string]Airport
	carriers map[string]Carrier
	tiers    map[string]map[string]Tier
	cfg      Config
}

// TestRandomDifferential 与朴素模型比对随机行程/行李组合，打印每步输入输出与依据。
func TestRandomDifferential(t *testing.T) {
	if testing.Verbose() {
		t.Log("差分测试开始：下方逐条打印输入、输出与判定依据")
	}
	rng := rand.New(rand.NewSource(20261006))
	const cases = 1000
	for iter := 0; iter < cases; iter++ {
		svc, reg, snap := randomWorld(t, rng, iter)
		req := randomRequest(t, rng, reg, iter)
		got, gerr := svc.CheckIn(req)
		want := naiveCompute(req, snap.airports, snap.carriers, snap.tiers, snap.cfg,
			func(format string, args ...any) {
				if testing.Verbose() {
					t.Logf("case=%03d %s", iter, fmt.Sprintf(format, args...))
				}
			})

		if want.reject != 0 {
			if gerr == nil {
				t.Fatalf("case %d: naive rejected bag %d but system accepted", iter, want.reject)
			}
			ow, ok := gerr.(*OverweightRejectedError)
			if !ok || ow.BagIndex != want.reject {
				t.Fatalf("case %d: reject mismatch %v vs idx %d", iter, gerr, want.reject)
			}
			if testing.Verbose() {
				t.Logf("case=%03d [系统] 一致拒收 序号=%d", iter, want.reject)
			}
			continue
		}
		if gerr != nil {
			t.Fatalf("case %d: system rejected (%v) but naive accepted: %+v", iter, gerr, req)
		}
		if len(got.Consignments) != len(want.fees) {
			t.Fatalf("case %d: consignment count %d vs %d", iter, len(got.Consignments), len(want.fees))
		}
		var total Money
		for i, ci := range got.Consignments {
			if ci.CarrierID != want.carrier[i] || ci.Fee != want.fees[i] {
				for _, s := range req.Itinerary {
					t.Logf("SEG %s pnr=%s %s(%s)->%s(%s) dep=%d arr=%d customs=%v",
						s.CarrierID, s.PNR, s.From, snap.airports[s.From].Region,
						s.To, snap.airports[s.To].Region, s.DepartsAt, s.ArrivesAt,
						snap.airports[s.To].Customs)
				}
				t.Fatalf("case %d leg %d: got %s/%d want %s/%d (parts=%d)",
					iter, i, ci.CarrierID, ci.Fee, want.carrier[i], want.fees[i], len(req.Parties))
			}
			total += ci.Fee
		}
		if total != got.TotalFee {
			t.Fatalf("case %d total fee mismatch", iter)
		}
		// 提取点逐条比对。
		for _, bag := range got.Bags {
			pnr := req.Parties[bag.Party].PNR
			if joinClaims(bag.ClaimPoints) != joinClaims(want.claims[pnr]) {
				t.Fatalf("case %d bag %d pnr %s claims %v vs naive %v",
					iter, bag.Index, pnr, bag.ClaimPoints, want.claims[pnr])
			}
		}
		if testing.Verbose() {
			t.Logf("case=%03d [系统] 一致接受 总费=%d 段数=%d", iter, got.TotalFee, len(got.Consignments))
		}
	}
}

// randomWorld 为每个用例造独立世界：固定 6 个机场（三区域，随机清关），
// CA 计件 / CW 计重，3 条随机会员组成的订座记录。
func randomWorld(t *testing.T, rng *rand.Rand, seed int) (*Service, *Registry, worldSnap) {
	t.Helper()
	reg := NewRegistry()
	snap := worldSnap{
		airports: map[string]Airport{},
		carriers: map[string]Carrier{},
		tiers:    map[string]map[string]Tier{},
		cfg:      testConfig(),
	}
	port := []struct {
		code string
		reg  Region
	}{
		{"A1", "R1"}, {"A2", "R1"}, {"B1", "R2"}, {"B2", "R2"}, {"C1", "R3"}, {"C2", "R3"},
	}
	for i, p := range port {
		ap := Airport{Code: p.code, Region: p.reg,
			Customs: i > 0 && rng.Intn(3) == 0} // A1 永不清关，保证起点合法
		if err := reg.AddAirport(ap); err != nil {
			t.Fatal(err)
		}
		snap.airports[p.code] = ap
	}
	ca := Carrier{ID: "CA", Mode: ModePiece, FreePieces: 1, PieceFreeWeight: 200,
		AbsWeight: 500, ExtraPieceRate: 1000, OverweightRate: 300}
	cw := Carrier{ID: "CW", Mode: ModeWeight, FreeWeight: 300,
		AbsWeight: 500, PerUnitRate: 10}
	if err := reg.AddCarrier(ca); err != nil {
		t.Fatal(err)
	}
	if err := reg.AddCarrier(cw); err != nil {
		t.Fatal(err)
	}
	snap.carriers["CA"] = ca
	snap.carriers["CW"] = cw

	for pn := 0; pn < 3; pn++ {
		pnr := fmt.Sprintf("P%d", pn)
		n := 1 + rng.Intn(3)
		var ps []Passenger
		snap.tiers[pnr] = map[string]Tier{}
		for k := 0; k < n; k++ {
			id := fmt.Sprintf("%s-%d", pnr, k)
			tier := Tier("")
			if rng.Intn(2) == 0 {
				tier = "GOLD"
			}
			ps = append(ps, Passenger{ID: id, Tier: tier})
			snap.tiers[pnr][id] = tier
		}
		if err := reg.RegisterPNR(pnr, ps); err != nil {
			t.Fatal(err)
		}
	}
	return NewService(testConfig(), reg), reg, snap
}

// randomRequest 生成连贯、时刻合法的随机行程与交运方。
func randomRequest(t *testing.T, rng *rand.Rand, reg *Registry, seed int) CheckInRequest {
	t.Helper()
	n := 1 + rng.Intn(4)
	codes := []string{"A1", "A2", "B1", "B2", "C1", "C2"}
	pnrs := []string{"P0", "P1", "P2"}

	var segs []Segment
	cur := codes[rng.Intn(len(codes))]
	dep := int64(10000)
	pnr := pnrs[rng.Intn(3)]
	for i := 0; i < n; i++ {
		if i > 0 {
			// 停留：多数落在 [60,240]，偶尔出界；以 1/3 概率换订座记录。
			gap := int64(60 + rng.Intn(181))
			if rng.Intn(4) == 0 {
				gap = int64(300 + rng.Intn(200))
			}
			dep += gap
			if rng.Intn(3) == 0 {
				pnr = pnrs[rng.Intn(3)]
			}
		}
		nxt := codes[rng.Intn(len(codes))]
		for nxt == cur {
			nxt = codes[rng.Intn(len(codes))]
		}
		arr := dep + 60 + int64(rng.Intn(300))
		carrier := []string{"CA", "CW"}[rng.Intn(2)]
		segs = append(segs, segm(carrier, pnr, cur, nxt, dep, arr))
		cur = nxt
		dep = arr
	}

	// 行程中出现过的 PNR 才允许交运。
	usedPNR := map[string]bool{}
	for _, s := range segs {
		usedPNR[s.PNR] = true
	}
	var reqParties []PartyBags
	for _, pnr := range pnrs {
		if !usedPNR[pnr] || rng.Intn(2) == 0 {
			continue
		}
		members, _ := reg.pnrPassengers(pnr)
		for id := range members {
			if rng.Intn(2) == 0 {
				continue
			}
			k := 1 + rng.Intn(3)
			var ws []int
			for j := 0; j < k; j++ {
				// 多数在上限内，少量超过绝对上限以触发拒收。
				w := rng.Intn(520)
				ws = append(ws, w)
			}
			reqParties = append(reqParties, bags(pnr, id, ws...))
		}
	}
	if len(reqParties) == 0 {
		// 兜底：取第一个使用中的 PNR 的第一名旅客交一件。
		var p string
		for _, s := range segs {
			p = s.PNR
			break
		}
		members, _ := reg.pnrPassengers(p)
		for id := range members {
			reqParties = []PartyBags{bags(p, id, rng.Intn(520))}
			break
		}
	}
	firstDep := int64(segs[0].DepartsAt)
	at := firstDep - 46 - int64(rng.Intn(2000)) // 严格早于截止
	return CheckInRequest{Itinerary: segs, Parties: reqParties, At: Minute(at)}
}
