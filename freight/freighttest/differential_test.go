package freighttest

import (
	"bytes"
	"math/rand"
	"reflect"
	"strconv"
	"sync"
	"sync/atomic"
	"testing"

	"ontology/freight/model"
	"ontology/freight/system"
)

var (
	allCarriers = []string{"顺丰", "圆通", "韵达", "德邦"}
	allCities   = []model.Region{"北京", "上海", "广州", "成都", "西藏"}
	allClasses  = []model.ServiceClass{model.Standard, model.Express}
)

type opKind int

const (
	opAdd opKind = iota
	opPrice
	opSettle
	opQuote
)

type recordedOp struct {
	kind opKind
	c    *model.Contract
	w    *model.Waybill
	q    system.QuoteRequest
	num  string
}

func genContract(rng *rand.Rand, seq int) *model.Contract {
	start := int64(rng.Intn(10)) * 100
	length := int64(1+rng.Intn(3)) * 100
	n := 1 + rng.Intn(4)
	var tiers []model.WeightTier
	bound := int64(0)
	for i := 0; i < n; i++ {
		lower := int64(0)
		if i > 0 {
			lower = tiers[i-1].Upper
		}
		upper := int64(0)
		if i < n-1 {
			bound += int64(1+rng.Intn(4)) * 10_000
			upper = bound
		}
		tiers = append(tiers, model.WeightTier{
			Lower: lower, Upper: upper,
			PricePerUnit: int64(10 + rng.Intn(200)),
		})
	}
	var remotes []model.Region
	if rng.Intn(2) == 0 {
		remotes = []model.Region{"西藏"}
	}
	return &model.Contract{
		ID:        "CT" + strconv.Itoa(seq),
		CarrierID: allCarriers[rng.Intn(len(allCarriers))],
		Route: model.Route{
			From: allCities[rng.Intn(4)],
			To:   allCities[rng.Intn(len(allCities))],
		},
		Class:       allClasses[rng.Intn(2)],
		EffectiveAt: model.Time(start),
		ExpiresAt:   model.Time(start + length),

		VolumeFactor:        int64(500 + rng.Intn(2000)),
		BillingUnit:         1000,
		Tiers:               tiers,
		FuelRatePerMille:    int64(rng.Intn(300)),
		RemoteRegions:       remotes,
		RemoteSurcharge:     int64(rng.Intn(1000)),
		SideThreshold:       int64(500 + rng.Intn(1500)),
		WeightThreshold:     int64(20_000 + rng.Intn(80_000)),
		OversizeSurcharge:   int64(rng.Intn(1000)),
		OverweightSurcharge: int64(rng.Intn(1000)),
		MinimumCharge:       int64(rng.Intn(5000)),
		MaxWeight:           int64(80_000 + rng.Intn(100_000)),
		MaxSide:             int64(1500 + rng.Intn(1500)),
	}
}

func genWaybill(rng *rand.Rand, num string) *model.Waybill {
	return &model.Waybill{
		Number:    num,
		CarrierID: allCarriers[rng.Intn(len(allCarriers))],
		Route: model.Route{
			From: allCities[rng.Intn(4)],
			To:   allCities[rng.Intn(len(allCities))],
		},
		Class:    allClasses[rng.Intn(2)],
		PickupAt: model.Time(rng.Intn(500)),
		Weight:   int64(1 + rng.Intn(160_000)),
		Dim: model.Dimensions{
			Length: int64(rng.Intn(2500)),
			Width:  int64(rng.Intn(2500)),
			Height: int64(rng.Intn(2500)),
		},
	}
}

// recordSequence 仅依赖种子生成操作序列，保证可无损重放。
func recordSequence(seed int64, ops int) []recordedOp {
	rng := rand.New(rand.NewSource(seed))
	out := make([]recordedOp, 0, ops)
	var known []*model.Contract
	var priced []string

	for i := 0; i < ops; i++ {
		roll := rng.Intn(10)
		switch {
		case roll < 4:
			var c *model.Contract
			if len(known) > 0 && rng.Intn(4) == 0 {
				base := known[rng.Intn(len(known))]
				c = naiveClone(base)
				c.EffectiveAt = model.Time(rng.Intn(1000))
				c.ExpiresAt = c.EffectiveAt + model.Time(1+rng.Intn(300))
				c.MinimumCharge = int64(rng.Intn(6000))
			} else {
				c = genContract(rng, i)
				known = append(known, c)
			}
			out = append(out, recordedOp{kind: opAdd, c: c})
		case roll < 8:
			num := "WB" + strconv.Itoa(i)
			w := genWaybill(rng, num)
			priced = append(priced, num)
			out = append(out, recordedOp{kind: opPrice, w: w})
		case roll == 8:
			num := "WB" + strconv.Itoa(i)
			if rng.Intn(2) == 0 && len(priced) > 0 {
				num = priced[rng.Intn(len(priced))]
			}
			out = append(out, recordedOp{kind: opSettle, num: num})
		default:
			req := system.QuoteRequest{
				Route:    model.Route{From: allCities[0], To: allCities[rng.Intn(len(allCities))]},
				Class:    allClasses[rng.Intn(2)],
				PickupAt: model.Time(rng.Intn(500)),
				Weight:   int64(1 + rng.Intn(160_000)),
				Dim: model.Dimensions{
					Length: int64(rng.Intn(2500)),
					Width:  int64(rng.Intn(2500)),
					Height: int64(rng.Intn(2500)),
				},
			}
			if rng.Intn(2) == 0 {
				req.Carriers = []string{
					allCarriers[rng.Intn(len(allCarriers))],
					allCarriers[rng.Intn(len(allCarriers))],
				}
			}
			out = append(out, recordedOp{kind: opQuote, q: req})
		}
	}
	return out
}

func assertCode(t *testing.T, tag string, i int, e1, e2 error) {
	t.Helper()
	c1, c2 := model.CodeOf(e1), model.CodeOf(e2)
	if c1 != c2 {
		t.Fatalf("%s op %d 错误码不一致: prod=%q(%v) naive=%q(%v)", tag, i, c1, e1, c2, e2)
	}
}

func breakdownEqual(g, w *model.FeeBreakdown) bool {
	return reflect.DeepEqual(g, w)
}

func compareFB(t *testing.T, tag string, i int, g, w *model.FeeBreakdown) {
	t.Helper()
	if g == nil || w == nil {
		t.Fatalf("%s op %d 明细缺失", tag, i)
	}
	gn, wn := *g, *w
	if gn.WaybillNumber == "__quote__" {
		gn.WaybillNumber = wn.WaybillNumber
	}
	if !breakdownEqual(&gn, &wn) {
		t.Fatalf("%s op %d 明细不一致\n got=%+v\nwant=%+v", tag, i, gn, wn)
	}
}

func compareQuotes(t *testing.T, i int, got []system.QuoteLine, want []quoteLineSpec) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("op %d 询价条数 %d != %d", i, len(got), len(want))
	}
	for j := range got {
		g, w := got[j], want[j]
		if g.CarrierID != w.carrier || g.Reason != w.reason {
			t.Fatalf("op %d 第 %d 行承运商/原因不一致: %+v vs %+v", i, j, g, w)
		}
		if g.Reason == "" {
			if g.Total != w.total {
				t.Fatalf("op %d 第 %d 行总价不一致: %d vs %d", i, j, g.Total, w.total)
			}
			compareFB(t, "quote", i, g.Breakdown, w.fb)
		}
	}
}

func cloneWaybill(w *model.Waybill) *model.Waybill { cp := *w; return &cp }

// playSequence 在给定生产系统与朴素模型上执行录制的操作序列。
func playSequence(t *testing.T, prod *system.System, naive *NaiveModel,
	seq []recordedOp) map[string]*model.FeeBreakdown {
	t.Helper()
	finalSettled := map[string]*model.FeeBreakdown{}
	for i, op := range seq {
		switch op.kind {
		case opAdd:
			assertCode(t, "add", i,
				prod.AddContract(naiveClone(op.c)), naive.Add(naiveClone(op.c)))
		case opPrice:
			g, e1 := prod.Price(cloneWaybill(op.w))
			w, e2 := naive.Price(cloneWaybill(op.w))
			assertCode(t, "price", i, e1, e2)
			if e1 == nil {
				compareFB(t, "price", i, g, w)
			}
		case opSettle:
			g, e1 := prod.Settle(op.num)
			w, e2 := naive.Settle(op.num)
			assertCode(t, "settle", i, e1, e2)
			if e1 == nil {
				compareFB(t, "settle", i, g, w)
				finalSettled[op.num] = g
			}
		case opQuote:
			gl, e1 := prod.Quote(op.q)
			if e1 != nil {
				t.Fatalf("op %d 询价不应整单失败: %v", i, e1)
			}
			wl := naive.Quote(quoteReqSpec{
				route: op.q.Route, class: op.q.Class, pickupAt: op.q.PickupAt,
				weight: op.q.Weight, dim: op.q.Dim, carriers: op.q.Carriers,
			})
			compareQuotes(t, i, gl, wl)
		}
	}
	return finalSettled
}

// TestRandomDifferential 大量随机操作序列下生产系统与朴素模型逐结果对照。
func TestRandomDifferential(t *testing.T) {
	if testing.Short() {
		t.Skip("跳过随机差分")
	}
	for iter := 0; iter < 60; iter++ {
		seed := int64(1000 + iter)
		seq := recordSequence(seed, 400)
		prod := system.New(nil)
		naive := NewNaive()
		finalSettled := playSequence(t, prod, naive, seq)

		// 序列结束后再追加一批合同变更，已结算金额仍必须不变。
		for k := 0; k < 20; k++ {
			rr := rand.New(rand.NewSource(seed + int64(k)*7 + 1))
			c := genContract(rr, 900_000+k)
			_ = prod.AddContract(naiveClone(c))
			_ = naive.Add(naiveClone(c))
		}
		for num, want := range finalSettled {
			g := prod.Settlement(num)
			w := naive.Settlement(num)
			if g == nil || w == nil {
				t.Fatalf("seed=%d 已结算运单 %s 快照丢失", seed, num)
			}
			compareFB(t, "freeze", 0, g, want)
			compareFB(t, "freeze", 0, g, w)
		}
	}
}

// TestReplayDeterminism 同序列在两套全新系统上重放，日志字节级一致。
func TestReplayDeterminism(t *testing.T) {
	seed := int64(42)
	seq := recordSequence(seed, 300)

	var buf1, buf2 bytes.Buffer
	p1 := system.New(system.NewJSONLogger(&buf1))
	p2 := system.New(system.NewJSONLogger(&buf2))
	playSequence(t, p1, NewNaive(), seq)
	playSequence(t, p2, NewNaive(), seq)
	if !bytes.Equal(buf1.Bytes(), buf2.Bytes()) {
		t.Fatalf("相同操作序列重放日志不一致\n%s\n----\n%s",
			buf1.String(), buf2.String())
	}
}

// TestConcurrentSerializability 高并发下不崩、不死锁、每运单至多结算一次。
func TestConcurrentSerializability(t *testing.T) {
	s := system.New(nil)
	for i := 0; i < 8; i++ {
		c := baseContract("K"+strconv.Itoa(i), "顺丰",
			model.Time(i*100), model.Time(i*100+100))
		if err := s.AddContract(c); err != nil {
			t.Fatal(err)
		}
	}
	var wg sync.WaitGroup
	var success int64
	var duplicateRejects int64
	counts := make([]int64, 40)
	var countsMu sync.Mutex
	for g := 0; g < 16; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			rng := rand.New(rand.NewSource(int64(g)))
			for k := 0; k < 400; k++ {
				n := rng.Intn(40)
				num := "WB" + strconv.Itoa(n)
				w := genWaybill(rng, num)
				w.CarrierID = "顺丰"
				w.PickupAt = model.Time(100 + rng.Intn(600))
				w.Weight = int64(1 + rng.Intn(60_000))
				if _, err := s.Price(w); err == nil {
					_, err := s.Settle(num)
					switch model.CodeOf(err) {
					case "":
						atomic.AddInt64(&success, 1)
						countsMu.Lock()
						counts[n]++
						countsMu.Unlock()
					case model.CodeAlreadySettled:
						atomic.AddInt64(&duplicateRejects, 1)
					}
				}
			}
		}(g)
	}
	wg.Wait()
	countsMu.Lock()
	defer countsMu.Unlock()
	for n, c := range counts {
		if c > 1 {
			t.Fatalf("运单 %d 被成功结算了 %d 次，串行化不变量被破坏", n, c)
		}
	}
	if success == 0 || duplicateRejects == 0 {
		t.Fatalf("并发证据不足: 成功=%d 重复拒绝=%d", success, duplicateRejects)
	}
}
