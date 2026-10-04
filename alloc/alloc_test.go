package alloc

import (
	"math/rand"
	"reflect"
	"testing"

	"ontology/auction"
)

func TestAllocateExample(t *testing.T) {
	// 题面例1：P=101。
	orders := []Order{
		{Seq: 1, ID: "b1", Buy: true, Price: 102, Qty: 300},
		{Seq: 2, ID: "b2", Buy: true, Price: 101, Qty: 200},
		{Seq: 3, ID: "b3", Buy: true, Price: 100, Qty: 400},
		{Seq: 4, ID: "s1", Buy: false, Price: 99, Qty: 200},
		{Seq: 5, ID: "s2", Buy: false, Price: 100, Qty: 300},
		{Seq: 6, ID: "s3", Buy: false, Price: 101, Qty: 300},
		{Seq: 7, ID: "s4", Buy: false, Price: 103, Qty: 100},
	}
	trades, remaining := Allocate(orders, 101)
	wantTrades := []Trade{
		{BuySeq: 1, SellSeq: 4, BuyID: "b1", SellID: "s1", Price: 101, Qty: 200},
		{BuySeq: 1, SellSeq: 5, BuyID: "b1", SellID: "s2", Price: 101, Qty: 100},
		{BuySeq: 2, SellSeq: 5, BuyID: "b2", SellID: "s2", Price: 101, Qty: 200},
	}
	if !reflect.DeepEqual(trades, wantTrades) {
		t.Fatalf("trades=%+v, want %+v", trades, wantTrades)
	}
	wantRem := []Order{
		{Seq: 3, ID: "b3", Buy: true, Price: 100, Qty: 400},
		{Seq: 6, ID: "s3", Buy: false, Price: 101, Qty: 300},
		{Seq: 7, ID: "s4", Buy: false, Price: 103, Qty: 100},
	}
	if !reflect.DeepEqual(remaining, wantRem) {
		t.Fatalf("remaining=%+v, want %+v", remaining, wantRem)
	}
}

func TestAllocateTable(t *testing.T) {
	cases := []struct {
		name        string
		orders      []Order
		price       int64
		wantTrades  []Trade
		wantRemSeqs []int
		wantRemQtys []int64
	}{
		{
			name: "量多一侧按价格优先截断，至多一条部分成交",
			orders: []Order{
				{Seq: 1, ID: "b1", Buy: true, Price: 100, Qty: 100},
				{Seq: 2, ID: "b2", Buy: true, Price: 101, Qty: 50},
				{Seq: 3, ID: "s1", Buy: false, Price: 99, Qty: 120},
			},
			price: 100,
			wantTrades: []Trade{
				{BuySeq: 2, SellSeq: 3, Price: 100, Qty: 50},
				{BuySeq: 1, SellSeq: 3, Price: 100, Qty: 70},
			},
			wantRemSeqs: []int{1},
			wantRemQtys: []int64{30},
		},
		{
			name: "同价按序号优先",
			orders: []Order{
				{Seq: 1, ID: "b1", Buy: true, Price: 100, Qty: 10},
				{Seq: 2, ID: "b2", Buy: true, Price: 100, Qty: 10},
				{Seq: 3, ID: "s1", Buy: false, Price: 100, Qty: 15},
			},
			price: 100,
			wantTrades: []Trade{
				{BuySeq: 1, SellSeq: 3, Price: 100, Qty: 10},
				{BuySeq: 2, SellSeq: 3, Price: 100, Qty: 5},
			},
			wantRemSeqs: []int{2},
			wantRemQtys: []int64{5},
		},
		{
			name: "卖队列价格升序",
			orders: []Order{
				{Seq: 1, ID: "b1", Buy: true, Price: 105, Qty: 100},
				{Seq: 2, ID: "s1", Buy: false, Price: 100, Qty: 30},
				{Seq: 3, ID: "s2", Buy: false, Price: 99, Qty: 30},
			},
			price: 105,
			wantTrades: []Trade{
				{BuySeq: 1, SellSeq: 3, Price: 105, Qty: 30},
				{BuySeq: 1, SellSeq: 2, Price: 105, Qty: 30},
			},
			wantRemSeqs: []int{1},
			wantRemQtys: []int64{40},
		},
		{
			name: "无对手方不产生成交",
			orders: []Order{
				{Seq: 1, ID: "b1", Buy: true, Price: 100, Qty: 10},
			},
			price:       100,
			wantTrades:  nil,
			wantRemSeqs: []int{1},
			wantRemQtys: []int64{10},
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			trades, remaining := Allocate(c.orders, c.price)
			if len(trades) != len(c.wantTrades) {
				t.Fatalf("trades=%+v, want %+v", trades, c.wantTrades)
			}
			for i, tr := range trades {
				w := c.wantTrades[i]
				if tr.BuySeq != w.BuySeq || tr.SellSeq != w.SellSeq ||
					tr.Qty != w.Qty || tr.Price != c.price {
					t.Fatalf("trade[%d]=%+v, want %+v (price=%d)", i, tr, w, c.price)
				}
			}
			if len(remaining) != len(c.wantRemSeqs) {
				t.Fatalf("remaining=%+v", remaining)
			}
			for i, o := range remaining {
				if o.Seq != c.wantRemSeqs[i] || o.Qty != c.wantRemQtys[i] {
					t.Fatalf("remaining[%d]=%+v, want seq=%d qty=%d",
						i, o, c.wantRemSeqs[i], c.wantRemQtys[i])
				}
			}
		})
	}
}

func TestAllocateRandomInvariants(t *testing.T) {
	rng := rand.New(rand.NewSource(7))
	for iter := 0; iter < 1500; iter++ {
		n := 1 + rng.Intn(80)
		orders := make([]Order, n)
		aorders := make([]auction.Order, n)
		for i := range orders {
			b := rng.Intn(2) == 0
			p := int64(90 + rng.Intn(21))
			q := int64(1 + rng.Intn(500))
			orders[i] = Order{Seq: i + 1, Buy: b, Price: p, Qty: q}
			aorders[i] = auction.Order{Buy: b, Price: p, Qty: q}
		}
		price := auction.Price(aorders, 100)
		if price == 0 {
			continue
		}
		trades, remaining := Allocate(orders, price)
		trades2, remaining2 := Allocate(orders, price)
		if !reflect.DeepEqual(trades, trades2) || !reflect.DeepEqual(remaining, remaining2) {
			t.Fatalf("iter=%d: 重放结果不一致", iter)
		}

		var traded, wantB, wantS int64
		for _, tr := range trades {
			traded += tr.Qty
			if tr.Price != price {
				t.Fatalf("iter=%d: 成交价 %d != P=%d", iter, tr.Price, price)
			}
		}
		for _, o := range orders {
			if o.Buy && o.Price >= price {
				wantB += o.Qty
			}
			if !o.Buy && o.Price <= price {
				wantS += o.Qty
			}
		}
		if v := min(wantB, wantS); traded != v {
			t.Fatalf("iter=%d: 成交总量 %d != V(P)=%d", iter, traded, v)
		}

		filled := map[int]int64{}
		for _, tr := range trades {
			filled[tr.BuySeq] += tr.Qty
			filled[tr.SellSeq] += tr.Qty
		}
		remQty := map[int]int64{}
		var remBuyOK, remSellOK bool
		prevSeq := 0
		for _, o := range remaining {
			remQty[o.Seq] = o.Qty
			if o.Seq <= prevSeq {
				t.Fatalf("iter=%d: 剩余清单未按序号升序", iter)
			}
			prevSeq = o.Seq
			if o.Buy && o.Price >= price {
				remBuyOK = true
			}
			if !o.Buy && o.Price <= price {
				remSellOK = true
			}
		}
		if remBuyOK && remSellOK {
			t.Fatalf("iter=%d: 分配后仍存在可配对余量", iter)
		}
		for _, o := range orders {
			if filled[o.Seq]+remQty[o.Seq] != o.Qty {
				t.Fatalf("iter=%d: seq=%d 成交+剩余 != 委托量", iter, o.Seq)
			}
			if filled[o.Seq] > o.Qty {
				t.Fatalf("iter=%d: seq=%d 成交量超过委托量", iter, o.Seq)
			}
		}
	}
}
