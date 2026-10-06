package consignment

import "sort"

// agreement 是一条价格协议：某供应商某商品在 [from, to) 区间内的单价。
type agreement struct {
	from, to  int64 // 左闭右开生效区间
	unitPrice int64 // 单价（正数）
}

// covers 报告时刻 t 是否落在生效区间内。
func (a agreement) covers(t int64) bool {
	return a.from <= t && t < a.to
}

// pairKey 标识一个（供应商，商品）对。
type pairKey struct {
	supplier, item uint64
}

// pricebook 按（供应商，商品）登记价格协议。
// 同一键下的协议区间不得重叠，按 from 升序保存。
type pricebook struct {
	byPair map[pairKey][]agreement
}

func newPricebook() *pricebook {
	return &pricebook{byPair: make(map[pairKey][]agreement)}
}

// overlaps 报告新区间是否与已有协议重叠。
func (p *pricebook) overlaps(supplier, item uint64, a agreement) bool {
	key := pairKey{supplier: supplier, item: item}
	list := p.byPair[key]
	i := sort.Search(len(list), func(i int) bool { return list[i].from >= a.from })
	if i > 0 && list[i-1].to > a.from {
		return true
	}
	if i < len(list) && a.to > list[i].from {
		return true
	}
	return false
}

// insert 插入一条协议。调用方须先通过 overlaps 确认无重叠。
func (p *pricebook) insert(supplier, item uint64, a agreement) {
	key := pairKey{supplier: supplier, item: item}
	list := p.byPair[key]
	i := sort.Search(len(list), func(i int) bool { return list[i].from >= a.from })
	list = append(list, agreement{})
	copy(list[i+1:], list[i:])
	list[i] = a
	p.byPair[key] = list
}

// priceAt 查询某供应商某商品在时刻 t 的有效单价。
func (p *pricebook) priceAt(supplier, item uint64, t int64) (int64, bool) {
	list := p.byPair[pairKey{supplier: supplier, item: item}]
	// 找最后一个 from <= t 的协议，再看 t 是否落在其区间内。
	i := sort.Search(len(list), func(i int) bool { return list[i].from > t }) - 1
	if i < 0 || !list[i].covers(t) {
		return 0, false
	}
	return list[i].unitPrice, true
}
