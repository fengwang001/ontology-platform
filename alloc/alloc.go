// Package alloc 实现开盘价确定后的成交分配。
package alloc

import (
	"sort"

	"ontology/auction"
)

// Order 是分配所需的委托视图（含剩余量）。
type Order = auction.Order

// Fill 是一笔成交。
type Fill struct {
	BuyID  []byte
	SellID []byte
	Qty    int64
}

// Rest 是一笔剩余委托。
type Rest struct {
	ID    []byte
	Acct  []byte
	Side  auction.Side
	Price int64
	Qty   int64
}

// Match 在开盘价 P 下撮合，返回成交与剩余清单。
func Match(orders []Order, p int64) ([]Fill, []Rest) {
	fills := []Fill{}
	if p == 0 {
		rests := make([]Rest, 0, len(orders))
		for _, o := range orders {
			rests = append(rests, Rest{ID: o.ID, Acct: o.Acct, Side: o.Side, Price: o.Price, Qty: o.Qty})
		}
		sortRests(rests, orders)
		return fills, rests
	}

	rem := make(map[int64]int64, len(orders)) // Seq -> 剩余量
	var buys, sells []Order
	for _, o := range orders {
		rem[o.Seq] = o.Qty
		switch {
		case o.Side == auction.Buy && o.Price >= p:
			buys = append(buys, o)
		case o.Side == auction.Sell && o.Price <= p:
			sells = append(sells, o)
		}
	}
	sort.SliceStable(buys, func(i, j int) bool {
		if buys[i].Price != buys[j].Price {
			return buys[i].Price > buys[j].Price
		}
		return buys[i].Seq < buys[j].Seq
	})
	sort.SliceStable(sells, func(i, j int) bool {
		if sells[i].Price != sells[j].Price {
			return sells[i].Price < sells[j].Price
		}
		return sells[i].Seq < sells[j].Seq
	})

	bi, si := 0, 0
	for bi < len(buys) && si < len(sells) {
		b, s := buys[bi], sells[si]
		q := rem[b.Seq]
		if rem[s.Seq] < q {
			q = rem[s.Seq]
		}
		fills = append(fills, Fill{BuyID: b.ID, SellID: s.ID, Qty: q})
		rem[b.Seq] -= q
		rem[s.Seq] -= q
		if rem[b.Seq] == 0 {
			bi++
		}
		if rem[s.Seq] == 0 {
			si++
		}
	}

	rests := make([]Rest, 0, len(orders))
	for _, o := range orders {
		if r := rem[o.Seq]; r > 0 {
			rests = append(rests, Rest{ID: o.ID, Acct: o.Acct, Side: o.Side, Price: o.Price, Qty: r})
		}
	}
	// orders 已按接受次序（序号升序）排列，剩余自然按序号升序。
	sortRests(rests, orders)
	return fills, rests
}

// sortRests 按序号升序稳定排序剩余清单（防御性：保证可重放确定性）。
func sortRests(rests []Rest, orders []Order) {
	seqOf := make(map[string]int64, len(orders))
	for _, o := range orders {
		seqOf[string(o.ID)] = o.Seq
	}
	sort.SliceStable(rests, func(i, j int) bool {
		return seqOf[string(rests[i].ID)] < seqOf[string(rests[j].ID)]
	})
}
