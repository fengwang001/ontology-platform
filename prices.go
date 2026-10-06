package ontology

import "sort"

type priceKey struct {
	supplier ID
	product  ID
}

// priceAgreement 是一条带左闭右开生效区间 [start,end) 的价格协议。
type priceAgreement struct {
	start int64
	end   int64
	price int64
}

// priceBook 保存每个 (供应商,商品) 按生效起点有序、互不重叠的协议列表。
// 纯内存、无锁；由 system 的互斥锁保护。
type priceBook struct {
	agreements map[priceKey][]priceAgreement
}

func newPriceBook() *priceBook {
	return &priceBook{agreements: map[priceKey][]priceAgreement{}}
}

// register 登记协议；区间非法或与现有区间重叠时报错。
func (pb *priceBook) register(supplier, product ID, start, end, price int64) error {
	list := pb.agreements[priceKey{supplier, product}]
	// 找到第一个 start' > start 的位置；重叠检查其前驱与该位置。
	idx := sort.Search(len(list), func(i int) bool { return list[i].start > start })
	if idx > 0 && list[idx-1].end > start {
		return newErr(KindPriceOverlap, "agreement overlaps a preceding interval")
	}
	if idx < len(list) && end > list[idx].start {
		return newErr(KindPriceOverlap, "agreement overlaps a following interval")
	}
	ag := priceAgreement{start: start, end: end, price: price}
	list = append(list, priceAgreement{})
	copy(list[idx+1:], list[idx:])
	list[idx] = ag
	pb.agreements[priceKey{supplier, product}] = list
	return nil
}

// priceAt 返回时刻 t 有效的协议单价；不存在有效协议时 ok=false。
// 该方法是纯查询，不改变任何状态。
func (pb *priceBook) priceAt(supplier, product ID, t int64) (int64, bool) {
	list := pb.agreements[priceKey{supplier, product}]
	idx := sort.Search(len(list), func(i int) bool { return list[i].end > t })
	if idx < len(list) && list[idx].start <= t {
		return list[idx].price, true
	}
	return 0, false
}
