// Package alloc 按开盘价对买卖双队列进行成交分配。
package alloc

import "sort"

// Order 是参与分配的委托快照，Qty 为剩余量。
type Order struct {
	Seq   int
	ID    string
	Buy   bool
	Price int64
	Qty   int64
}

// Trade 是一笔成交，成交价一律为开盘价。
type Trade struct {
	BuySeq  int
	SellSeq int
	BuyID   string
	SellID  string
	Price   int64
	Qty     int64
}

// Allocate 按开盘价 price 配对：买方队列为买价>=price 的委托按（价格降序，序号升序），
// 卖方队列为卖价<=price 的委托按（价格升序，序号升序）。取两队队首配对，成交量为
// 二者剩余量的较小者，用尽者出队，直到一侧耗尽。返回按产生次序的成交清单与
// 按序号升序的剩余清单（含部分成交的余量）。
func Allocate(orders []Order, price int64) ([]Trade, []Order) {
	buys := make([]Order, 0, len(orders))
	sells := make([]Order, 0, len(orders))
	for _, o := range orders {
		if o.Buy {
			if o.Price >= price {
				buys = append(buys, o)
			}
		} else if o.Price <= price {
			sells = append(sells, o)
		}
	}
	sort.Slice(buys, func(i, j int) bool {
		if buys[i].Price != buys[j].Price {
			return buys[i].Price > buys[j].Price
		}
		return buys[i].Seq < buys[j].Seq
	})
	sort.Slice(sells, func(i, j int) bool {
		if sells[i].Price != sells[j].Price {
			return sells[i].Price < sells[j].Price
		}
		return sells[i].Seq < sells[j].Seq
	})

	var trades []Trade
	filled := make(map[int]int64, len(buys)+len(sells))
	bi, si := 0, 0
	for bi < len(buys) && si < len(sells) {
		q := min(buys[bi].Qty, sells[si].Qty)
		trades = append(trades, Trade{
			BuySeq:  buys[bi].Seq,
			SellSeq: sells[si].Seq,
			BuyID:   buys[bi].ID,
			SellID:  sells[si].ID,
			Price:   price,
			Qty:     q,
		})
		filled[buys[bi].Seq] += q
		filled[sells[si].Seq] += q
		buys[bi].Qty -= q
		sells[si].Qty -= q
		if buys[bi].Qty == 0 {
			bi++
		}
		if sells[si].Qty == 0 {
			si++
		}
	}

	remaining := make([]Order, 0, len(orders))
	for _, o := range orders {
		o.Qty -= filled[o.Seq]
		if o.Qty > 0 {
			remaining = append(remaining, o)
		}
	}
	sort.Slice(remaining, func(i, j int) bool { return remaining[i].Seq < remaining[j].Seq })
	return trades, remaining
}
