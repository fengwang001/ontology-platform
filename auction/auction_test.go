package auction

import (
	"math/rand"
	"strings"
	"testing"
)

type tri struct {
	side  string
	price int64
	qty   int64
}

// build 从三元组构造委托，序号按给出次序，id 取字母。
func build(triples []tri) []Order {
	os := make([]Order, len(triples))
	for i, t := range triples {
		sd := Buy
		if t.side == "s" {
			sd = Sell
		}
		os[i] = Order{Seq: int64(i + 1), ID: []byte{byte('a' + i%26), byte('a' + i/26)}, Side: sd, Price: t.price, Qty: t.qty}
	}
	return os
}

func TestPriceTieBreaks(t *testing.T) {
	cases := []struct {
		name string
		ref  int64
		in   []tri
		want int64
	}{
		{
			name: "题面例1_二级I决胜_P101",
			ref:  100,
			in: []tri{
				{"b", 102, 300}, {"b", 101, 200}, {"b", 100, 400},
				{"s", 99, 200}, {"s", 100, 300}, {"s", 101, 300}, {"s", 103, 100},
			},
			want: 101,
		},
		{
			name: "三级_方向不一_ref100取100",
			ref:  100,
			in: []tri{
				{"b", 102, 300}, {"b", 100, 200},
				{"s", 99, 300}, {"s", 101, 200},
			},
			want: 100,
		},
		{
			name: "三级_方向不一_ref103取102",
			ref:  103,
			in: []tri{
				{"b", 102, 300}, {"b", 100, 200},
				{"s", 99, 300}, {"s", 101, 200},
			},
			want: 102,
		},
		{
			name: "三级_全买压取最高_忽略ref",
			ref:  100,
			in: []tri{
				{"b", 101, 500}, {"s", 99, 200}, {"s", 100, 100},
			},
			want: 101,
		},
		{
			name: "三级_全卖压取最低",
			ref:  100,
			in: []tri{
				{"b", 100, 100}, {"b", 101, 200},
				{"s", 99, 500},
			},
			want: 99,
		},
		{
			name: "V最大为0_无开盘价",
			ref:  100,
			in: []tri{
				{"b", 90, 100}, {"s", 110, 100},
			},
			want: 0,
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := Price(build(c.in), c.ref)
			if got.Price != c.want {
				t.Fatalf("price=%d want %d; stats=%+v", got.Price, c.want, got.Stats)
			}
		})
	}
}

func TestEquidistantPickLower(t *testing.T) {
	// 99 与 101 的 V=200/I=200 完全并列且方向相反，距 ref=100 同距，取低价 99。
	in := build([]tri{
		{"b", 101, 200}, {"b", 99, 200},
		{"s", 99, 200}, {"s", 101, 200},
	})
	got := Price(in, 100)
	if got.Price != 99 {
		t.Fatalf("price=%d want 99; stats=%+v", got.Price, got.Stats)
	}
}

func TestStatsExample(t *testing.T) {
	in := build([]tri{
		{"b", 102, 300}, {"b", 101, 200}, {"b", 100, 400},
		{"s", 99, 200}, {"s", 100, 300}, {"s", 101, 300}, {"s", 103, 100},
	})
	r := Price(in, 100)
	byPrice := map[int64]Stat{}
	for _, s := range r.Stats {
		byPrice[s.Price] = s
	}
	s100 := byPrice[100]
	if s100.B != 900 || s100.S != 500 || s100.V != 500 || s100.I != 400 {
		t.Fatalf("p100 stat wrong: %+v", s100)
	}
	s101 := byPrice[101]
	if s101.B != 500 || s101.S != 800 || s101.V != 500 || s101.I != 300 {
		t.Fatalf("p101 stat wrong: %+v", s101)
	}
	if r.Price != 101 {
		t.Fatalf("price=%d want 101", r.Price)
	}
}

// naivePrice 逐候选价位重扫全簿，作为随机对照的朴素基准。
func naivePrice(orders []Order, ref int64) int64 {
	if len(orders) == 0 {
		return 0
	}
	pset := map[int64]bool{}
	for _, o := range orders {
		pset[o.Price] = true
	}
	type ps struct {
		p, v, i int64
		buy     bool
		sell    bool
	}
	var cands []ps
	for p := range pset {
		var b, s int64
		for _, o := range orders {
			if o.Side == Buy && o.Price >= p {
				b += o.Qty
			}
			if o.Side == Sell && o.Price <= p {
				s += o.Qty
			}
		}
		c := ps{p: p, buy: b > s, sell: b < s}
		if b >= s {
			c.v, c.i = s, b-s
		} else {
			c.v, c.i = b, s-b
		}
		cands = append(cands, c)
	}
	maxV := int64(-1)
	for _, c := range cands {
		if c.v > maxV {
			maxV = c.v
		}
	}
	var t1 []ps
	for _, c := range cands {
		if c.v == maxV {
			t1 = append(t1, c)
		}
	}
	if maxV == 0 {
		return 0
	}
	minI := t1[0].i
	for _, c := range t1 {
		if c.i < minI {
			minI = c.i
		}
	}
	var t2 []ps
	for _, c := range t1 {
		if c.i == minI {
			t2 = append(t2, c)
		}
	}
	allBuy, allSell := true, true
	for _, c := range t2 {
		allBuy = allBuy && c.buy
		allSell = allSell && c.sell
	}
	dist := func(p int64) int64 {
		d := p - ref
		if d < 0 {
			return -d
		}
		return d
	}
	pick := t2[0]
	for _, c := range t2[1:] {
		switch {
		case allBuy:
			if c.p > pick.p {
				pick = c
			}
		case allSell:
			if c.p < pick.p {
				pick = c
			}
		default:
			d, dp := dist(c.p), dist(pick.p)
			if d < dp || (d == dp && c.p < pick.p) {
				pick = c
			}
		}
	}
	return pick.p
}

func TestRandomAgainstNaive(t *testing.T) {
	rng := rand.New(rand.NewSource(20261005))
	for g := 0; g < 1500; g++ {
		n := 1 + rng.Intn(24)
		ref := int64(1 + rng.Intn(40))
		in := make([]Order, n)
		var sb strings.Builder
		for i := 0; i < n; i++ {
			sd := Buy
			ch := "b"
			if rng.Intn(2) == 1 {
				sd, ch = Sell, "s"
			}
			t := tri{side: ch, price: int64(1 + rng.Intn(50)), qty: int64(1 + rng.Intn(300))}
			in[i] = Order{Seq: int64(i + 1), ID: []byte{byte(i)}, Side: sd, Price: t.price, Qty: t.qty}
			if i > 0 {
				sb.WriteByte(' ')
			}
			sb.WriteString(ch)
			sb.WriteByte('@')
			sb.WriteString(itoa(t.price))
			sb.WriteByte('x')
			sb.WriteString(itoa(t.qty))
		}
		got := Price(in, ref)
		want := naivePrice(in, ref)
		var stLog []string
		for _, s := range got.Stats {
			stLog = append(stLog, "p="+itoa(s.Price)+" B="+itoa(s.B)+" S="+itoa(s.S)+" V="+itoa(s.V)+" I="+itoa(s.I))
		}
		t.Logf("group %d ref=%d input=[%s] -> P=%d; %s", g, ref, sb.String(), got.Price, strings.Join(stLog, " | "))
		if got.Price != want {
			t.Fatalf("group %d: price=%d naive=%d", g, got.Price, want)
		}
		if got.visited > 4*int64(n) {
			t.Fatalf("group %d visited=%d > 4N=%d", g, got.visited, 4*n)
		}
	}
}

func TestVisitedBoundKControl(t *testing.T) {
	for _, k := range []int{10, 5000} {
		n := 10000
		in := make([]Order, n)
		for i := 0; i < n; i++ {
			sd := Buy
			if i%2 == 1 {
				sd = Sell
			}
			id := []byte{byte(i % 251), byte(i / 251)}
			in[i] = Order{Seq: int64(i + 1), ID: id, Side: sd, Price: int64(1 + (i*7919)%k), Qty: int64(1 + i%97)}
		}
		r := Price(in, int64(k/2)+1)
		t.Logf("N=%d K=%d visited=%d price=%d", n, k, r.visited, r.Price)
		if r.visited > 4*int64(n) {
			t.Fatalf("K=%d visited=%d > 4N=%d", k, r.visited, 4*n)
		}
	}
}

func itoa(x int64) string {
	if x == 0 {
		return "0"
	}
	var buf [21]byte
	i := len(buf)
	for x > 0 {
		i--
		buf[i] = byte('0' + x%10)
		x /= 10
	}
	return string(buf[i:])
}
