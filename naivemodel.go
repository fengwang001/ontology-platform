package ontology

import "sort"

// naiveSystem 是独立编写的朴素参考模型。
// 与 System 的区别仅在于内部数据组织：
//   - 批次保存在扁平 map 中，每次领用/查询都全量线性扫描并排序；
//   - 没有堆索引、没有惰性摘除、没有到期分桶；
//   - 价格协议使用独立的朴素实现（区间数组线性查找）。
//
// 业务规则、拒绝优先级、时钟语义与 System 完全一致，
// 供随机差分测试对照分配结果与金额。
type naiveSystem struct {
	clock   int64
	limits  map[pk]int64
	batches map[pk]map[BatchID]*naiveBatch
	seq     map[pk]BatchID
	prices  map[pk][]naivePrice
	lines   map[LineID]*SettlementLine
	lineSeq LineID
	ledger  []ledgerEntry
}

type pk struct {
	supplier ID
	product  ID
}

type naiveBatch struct {
	arrival   int64
	quantity  int64
	remaining int64
	duration  int64
}

type naivePrice struct {
	start, end, price int64
}

func newNaiveSystem() *naiveSystem {
	return &naiveSystem{
		limits:  map[pk]int64{},
		batches: map[pk]map[BatchID]*naiveBatch{},
		seq:     map[pk]BatchID{},
		prices:  map[pk][]naivePrice{},
		lines:   map[LineID]*SettlementLine{},
	}
}

func (n *naiveSystem) clockErr(t int64) error {
	if t < n.clock {
		return newErr(KindClockRollback, "naive: clock rollback")
	}
	return nil
}

func (n *naiveSystem) batchMap(key pk) map[BatchID]*naiveBatch {
	m := n.batches[key]
	if m == nil {
		m = map[BatchID]*naiveBatch{}
		n.batches[key] = m
	}
	return m
}

func (n *naiveSystem) onHand(key pk, now int64) (total, expired int64) {
	for _, b := range n.batches[key] {
		total += b.remaining
		if now >= b.arrival+b.duration {
			expired += b.remaining
		}
	}
	return total, expired
}

func (n *naiveSystem) setLimit(t int64, supplier, product ID, limit int64) error {
	if t < 0 || limit < 0 {
		return newErr(KindInvalidParam, "naive: bad limit args")
	}
	if err := n.clockErr(t); err != nil {
		return err
	}
	n.limits[pk{supplier, product}] = limit
	n.clock = t
	return nil
}

func (n *naiveSystem) registerPrice(t int64, supplier, product ID, start, end, price int64) error {
	if t < 0 || start < 0 || end <= start || price < 0 {
		return newErr(KindInvalidParam, "naive: bad price args")
	}
	if err := n.clockErr(t); err != nil {
		return err
	}
	key := pk{supplier, product}
	list := n.prices[key]
	for _, a := range list {
		if start < a.end && a.start < end {
			return newErr(KindPriceOverlap, "naive: overlap")
		}
	}
	list = append(list, naivePrice{start, end, price})
	sort.Slice(list, func(i, j int) bool { return list[i].start < list[j].start })
	n.prices[key] = list
	n.clock = t
	return nil
}

func (n *naiveSystem) priceAt(supplier, product ID, t int64) (int64, bool) {
	for _, a := range n.prices[pk{supplier, product}] {
		if a.start <= t && t < a.end {
			return a.price, true
		}
	}
	return 0, false
}

func (n *naiveSystem) receive(t int64, supplier, product ID, quantity, duration int64) (BatchID, error) {
	if t < 0 || quantity <= 0 || duration < 0 {
		return 0, newErr(KindInvalidParam, "naive: bad receive args")
	}
	if err := n.clockErr(t); err != nil {
		return 0, err
	}
	key := pk{supplier, product}
	limit, ok := n.limits[key]
	if !ok {
		return 0, newErr(KindNotFound, "naive: no limit")
	}
	total, _ := n.onHand(key, t)
	if total+quantity > limit {
		return 0, newErr(KindOverCap, "naive: over cap")
	}
	n.seq[key]++
	num := n.seq[key]
	n.batchMap(key)[num] = &naiveBatch{
		arrival:   t,
		quantity:  quantity,
		remaining: quantity,
		duration:  duration,
	}
	n.clock = t
	return num, nil
}

func (n *naiveSystem) returnBatch(t int64, supplier, product ID, number BatchID, quantity int64) error {
	if t < 0 || quantity <= 0 {
		return newErr(KindInvalidParam, "naive: bad return args")
	}
	if err := n.clockErr(t); err != nil {
		return err
	}
	key := pk{supplier, product}
	b, ok := n.batches[key][number]
	if !ok {
		return newErr(KindNotFound, "naive: batch not found")
	}
	if quantity > b.remaining {
		return newErr(KindExcessQuantity, "naive: return excess")
	}
	b.remaining -= quantity
	n.clock = t
	return nil
}

type naiveCandidate struct {
	supplier ID
	number   BatchID
	batch    *naiveBatch
}

// candidates 全量扫描某商品所有供应商的所有批次，按规定 FIFO 次序排序，
// 跳过已到期与剩余为零的批次。
func (n *naiveSystem) candidates(product ID, now int64) []naiveCandidate {
	var out []naiveCandidate
	for key, m := range n.batches {
		if key.product != product {
			continue
		}
		for num, b := range m {
			if b.remaining > 0 && now < b.arrival+b.duration {
				out = append(out, naiveCandidate{supplier: key.supplier, number: num, batch: b})
			}
		}
	}
	sort.SliceStable(out, func(i, j int) bool {
		a, c := out[i], out[j]
		if a.batch.arrival != c.batch.arrival {
			return a.batch.arrival < c.batch.arrival
		}
		if a.supplier != c.supplier {
			return a.supplier < c.supplier
		}
		return a.number < c.number
	})
	return out
}

func (n *naiveSystem) consume(t int64, product ID, quantity int64) (*Receipt, error) {
	if t < 0 || quantity <= 0 {
		return nil, newErr(KindInvalidParam, "naive: bad consume args")
	}
	if err := n.clockErr(t); err != nil {
		return nil, err
	}
	cands := n.candidates(product, t)
	need := quantity
	var used []naiveCandidate
	involved := map[ID]bool{}
	for _, c := range cands {
		if need == 0 {
			break
		}
		take := c.batch.remaining
		if take > need {
			take = need
		}
		used = append(used, c)
		involved[c.supplier] = true
		need -= take
	}
	if need > 0 {
		for sup := range involved {
			if _, ok := n.priceAt(sup, product, t); !ok {
				return nil, newErr(KindNoValidPrice, "naive: no price")
			}
		}
		return nil, newErr(KindInsufficientStock, "naive: insufficient")
	}
	unit := map[ID]int64{}
	for sup := range involved {
		p, ok := n.priceAt(sup, product, t)
		if !ok {
			return nil, newErr(KindNoValidPrice, "naive: no price")
		}
		unit[sup] = p
	}
	r := &Receipt{Time: t}
	// 提交：按 FIFO 顺序扣减并写结算行。
	need = quantity
	for _, c := range used {
		take := c.batch.remaining
		if take > need {
			take = need
		}
		c.batch.remaining -= take
		need -= take
		n.lineSeq++
		l := &SettlementLine{
			ID:          n.lineSeq,
			ConsumeTime: t,
			Supplier:    c.supplier,
			Product:     product,
			BatchNumber: c.number,
			Quantity:    take,
			UnitPrice:   unit[c.supplier],
		}
		n.lines[l.ID] = l
		r.Lines = append(r.Lines, *l)
		n.ledger = append(n.ledger, ledgerEntry{
			time:     t,
			supplier: c.supplier,
			amount:   take * unit[c.supplier],
		})
	}
	n.clock = t
	return r, nil
}

func (n *naiveSystem) reverse(t int64, line LineID, quantity int64) error {
	if t < 0 || quantity <= 0 {
		return newErr(KindInvalidParam, "naive: bad reverse args")
	}
	if err := n.clockErr(t); err != nil {
		return err
	}
	l, ok := n.lines[line]
	if !ok {
		return newErr(KindNotFound, "naive: line not found")
	}
	if quantity > l.Quantity-l.Reversed {
		return newErr(KindExcessQuantity, "naive: reverse excess")
	}
	key := pk{l.Supplier, l.Product}
	total, _ := n.onHand(key, t)
	if total+quantity > n.limits[key] {
		return newErr(KindOverCap, "naive: reverse over cap")
	}
	n.batches[key][l.BatchNumber].remaining += quantity
	l.Reversed += quantity
	n.ledger = append(n.ledger, ledgerEntry{
		time:     t,
		supplier: l.Supplier,
		amount:   quantity * l.UnitPrice,
		reversal: true,
	})
	n.clock = t
	return nil
}

func (n *naiveSystem) statement(supplier ID, start, end int64) (*Statement, error) {
	if start < 0 || end < start {
		return nil, newErr(KindInvalidParam, "naive: bad period")
	}
	if end > n.clock {
		return nil, newErr(KindPeriodOpen, "naive: period open")
	}
	st := &Statement{Supplier: supplier, Start: start, End: end}
	for _, e := range n.ledger {
		if e.supplier != supplier || e.time < start || e.time >= end {
			continue
		}
		if e.reversal {
			st.Reversed += e.amount
		} else {
			st.Consumed += e.amount
		}
	}
	st.Net = st.Consumed - st.Reversed
	return st, nil
}

func (n *naiveSystem) onHandQuery(supplier, product ID) (int64, int64) {
	return n.onHand(pk{supplier, product}, n.clock)
}

func (n *naiveSystem) reversedQty(line LineID) (int64, error) {
	l, ok := n.lines[line]
	if !ok {
		return 0, newErr(KindNotFound, "naive: line not found")
	}
	return l.Reversed, nil
}
