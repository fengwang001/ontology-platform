package auction

import (
	"fmt"
	"math/rand"
	"sort"
	"testing"
)

func buy(price, qty int64) Order  { return Order{Buy: true, Price: price, Qty: qty} }
func sell(price, qty int64) Order { return Order{Buy: false, Price: price, Qty: qty} }

func TestPriceTable(t *testing.T) {
	cases := []struct {
		name   string
		orders []Order
		ref    int64
		want   int64
	}{
		{
			name: "题面例1：V并列后按I最小取101",
			orders: []Order{
				buy(102, 300), buy(101, 200), buy(100, 400),
				sell(99, 200), sell(100, 300), sell(101, 300), sell(103, 100),
			},
			ref: 100, want: 101,
		},
		{
			name:   "题面例2：方向不一取最近ref，ref=100得100",
			orders: []Order{buy(102, 300), buy(100, 200), sell(99, 300), sell(101, 200)},
			ref:    100, want: 100,
		},
		{
			name:   "题面例2：ref=103得102",
			orders: []Order{buy(102, 300), buy(100, 200), sell(99, 300), sell(101, 200)},
			ref:    103, want: 102,
		},
		{
			name:   "题面例3：全买压取最高101而非最近ref的100",
			orders: []Order{buy(101, 500), sell(99, 200), sell(100, 100)},
			ref:    100, want: 101,
		},
		{
			name:   "全卖压取最低99",
			orders: []Order{buy(101, 100), sell(99, 200), sell(100, 200)},
			ref:    100, want: 99,
		},
		{
			name:   "距离相同取较低价99",
			orders: []Order{buy(101, 100), sell(99, 100)},
			ref:    100, want: 99,
		},
		{
			name:   "V最大为0返回0",
			orders: []Order{buy(100, 10), sell(200, 10)},
			ref:    150, want: 0,
		},
		{
			name:   "只有买单V为0",
			orders: []Order{buy(100, 10), buy(101, 20)},
			ref:    100, want: 0,
		},
		{
			name: "一级筛选：V更大者优先于I更小者",
			orders: []Order{
				buy(100, 100), buy(102, 50),
				sell(99, 80), sell(101, 40),
			},
			// 99/100: V=80,I=70；101/102: V=50,I=70。V 决定 T={99,100}，全买压取100。
			ref: 101, want: 100,
		},
		{
			name: "T中含B=S时按参考价规则",
			orders: []Order{
				buy(100, 100),
				sell(99, 50), sell(100, 50),
			},
			// 99: B=S=100；100: B=S=100。T 全 B=S，取离 ref 最近。
			ref: 105, want: 100,
		},
		{
			name: "方向不一含相等时按参考价规则取低价",
			orders: []Order{
				buy(100, 100), buy(99, 50),
				sell(99, 100), sell(100, 50),
			},
			// 99: B=150,S=100；100: B=100,S=150。方向不一，ref=100 取 100。
			ref: 100, want: 100,
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := Price(c.orders, c.ref); got != c.want {
				t.Fatalf("Price=%d, want %d", got, c.want)
			}
		})
	}
}

// naivePrice 逐价位重新累加的朴素模拟，作为对照基准。
func naivePrice(orders []Order, ref int64) (int64, string) {
	set := map[int64]bool{}
	for _, o := range orders {
		set[o.Price] = true
	}
	prices := make([]int64, 0, len(set))
	for p := range set {
		prices = append(prices, p)
	}
	sort.Slice(prices, func(i, j int) bool { return prices[i] < prices[j] })

	type cand struct {
		p, b, s, v, i int64
	}
	cs := make([]cand, 0, len(prices))
	for _, p := range prices {
		var b, s int64
		for _, o := range orders { // 每个候选价重扫全部委托
			if o.Buy && o.Price >= p {
				b += o.Qty
			}
			if !o.Buy && o.Price <= p {
				s += o.Qty
			}
		}
		v := min(b, s)
		d := b - s
		if d < 0 {
			d = -d
		}
		cs = append(cs, cand{p, b, s, v, d})
	}
	log := ""
	for _, c := range cs {
		log += fmt.Sprintf("  p=%d B=%d S=%d V=%d I=%d\n", c.p, c.b, c.s, c.v, c.i)
	}
	bestV, bestI := int64(-1), int64(-1)
	for _, c := range cs {
		if c.v > bestV || (c.v == bestV && c.i < bestI) {
			bestV, bestI = c.v, c.i
		}
	}
	if bestV <= 0 {
		return 0, log
	}
	var ts []cand
	for _, c := range cs {
		if c.v == bestV && c.i == bestI {
			ts = append(ts, c)
		}
	}
	allBuy, allSell := true, true
	for _, c := range ts {
		if c.b <= c.s {
			allBuy = false
		}
		if c.b >= c.s {
			allSell = false
		}
	}
	switch {
	case allBuy:
		return ts[len(ts)-1].p, log
	case allSell:
		return ts[0].p, log
	}
	best := ts[0]
	for _, c := range ts[1:] {
		db, dd := c.p-ref, best.p-ref
		if db < 0 {
			db = -db
		}
		if dd < 0 {
			dd = -dd
		}
		if db < dd || (db == dd && c.p < best.p) {
			best = c
		}
	}
	return best.p, log
}

func TestPriceRandomVsNaive(t *testing.T) {
	rng := rand.New(rand.NewSource(20261005))
	for iter := 0; iter < 1500; iter++ {
		n := 1 + rng.Intn(60)
		orders := make([]Order, n)
		for i := range orders {
			orders[i] = Order{
				Buy:   rng.Intn(2) == 0,
				Price: int64(90 + rng.Intn(21)),
				Qty:   int64(1 + rng.Intn(500)),
			}
		}
		ref := int64(95 + rng.Intn(11))
		got := Price(orders, ref)
		want, rationale := naivePrice(orders, ref)
		t.Logf("iter=%d ref=%d input=%v output=%d want=%d\n候选价判定依据:\n%s",
			iter, ref, orders, got, want, rationale)
		if got != want {
			t.Fatalf("iter=%d ref=%d: Price=%d, naive=%d\ninput=%v\n%s",
				iter, ref, got, want, orders, rationale)
		}
	}
}

func TestVisitedBoundIndependentOfK(t *testing.T) {
	const n = 10000
	for _, k := range []int{10, 5000} {
		orders := make([]Order, 0, n)
		for i := 0; i < n; i++ {
			orders = append(orders, Order{
				Buy:   i%2 == 0,
				Price: int64(100 + i%k),
				Qty:   int64(1 + i%97),
			})
		}
		Price(orders, 105)
		t.Logf("N=%d K=%d visited=%d 上界=%d", n, k, visited, 4*n)
		if visited > 4*n {
			t.Fatalf("K=%d: visited=%d 超过 4N=%d", k, visited, 4*n)
		}
	}
}
