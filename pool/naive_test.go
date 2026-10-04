package pool

import "sort"

// naiveContract 是朴素模型对合约的逐字段重述。
type naiveContract struct {
	id       int64
	lender   string
	borrower string
	sym      string
	qty      int64
	recall   bool
	dl       int64
}

type naiveLender struct {
	regSeq int64
	idle   int64
}

type naiveBuyin struct {
	id      int64
	qty     int64
	price   int64
	penalty int64
	at      int64
}

// naiveModel 与产品实现完全独立，仅按题面规则逐步重放。
type naiveModel struct {
	now       int64
	n         int64
	pen       int64
	nextID    int64
	nextReg   map[string]int64
	prices    map[string]int64
	lenders   map[string]map[string]*naiveLender // sym -> lender -> state
	contracts map[int64]*naiveContract
	handed    map[string]int64
	lent      map[string]int64
	payable   map[string]int64
	buyins    []naiveBuyin
}

func newNaive(n, pen int64) *naiveModel {
	return &naiveModel{
		n:         n,
		pen:       pen,
		nextReg:   map[string]int64{},
		prices:    map[string]int64{},
		lenders:   map[string]map[string]*naiveLender{},
		contracts: map[int64]*naiveContract{},
		handed:    map[string]int64{},
		lent:      map[string]int64{},
		payable:   map[string]int64{},
	}
}

func (m *naiveModel) lender(sym, lender string) *naiveLender {
	ls := m.lenders[sym]
	if ls == nil {
		ls = map[string]*naiveLender{}
		m.lenders[sym] = ls
	}
	l := ls[lender]
	if l == nil {
		l = &naiveLender{}
		ls[lender] = l
	}
	return l
}

func ceilPenalty(r, p, pen int64) int64 {
	v := r*p*pen + 9999
	return v / 10000
}

// settle 推进时钟，并把 dl<=now 的召回按 (dl,id) 强制买入。
func (m *naiveModel) settle(now int64) {
	m.now = now
	var due []*naiveContract
	for _, c := range m.contracts {
		if c.recall && c.dl <= now {
			due = append(due, c)
		}
	}
	sort.Slice(due, func(i, j int) bool {
		return due[i].dl < due[j].dl || (due[i].dl == due[j].dl && due[i].id < due[j].id)
	})
	for _, c := range due {
		r := c.qty
		price := m.prices[c.sym]
		pen := ceilPenalty(r, price, m.pen)
		m.payable[c.borrower] += r*price + pen
		m.handed[c.sym] += r
		delete(m.contracts, c.id)
		m.buyins = append(m.buyins, naiveBuyin{id: c.id, qty: r, price: price, penalty: pen, at: c.dl})
	}
}

func (m *naiveModel) contractsOf(pred func(*naiveContract) bool) []*naiveContract {
	var out []*naiveContract
	for _, c := range m.contracts {
		if pred(c) {
			out = append(out, c)
		}
	}
	return out
}

// op 是朴素模型与产品实现共用的一步操作。
type op struct {
	kind     string
	now      int64
	actor    string
	sym      string
	qty      int64
	price    int64
	badParam bool
}

// run 返回拒绝原因（nil 表示接受）与接受的 Borrow 合约号。
func (m *naiveModel) run(o op) (error, []int64) {
	if o.badParam {
		return ErrInvalid, nil
	}
	if o.now < m.now {
		return ErrClockBack, nil
	}

	switch o.kind {
	case "price":
		m.settle(o.now) // 旧价
		m.prices[o.sym] = o.price
		return nil, nil

	case "lend":
		m.settle(o.now)
		if _, ok := m.prices[o.sym]; !ok {
			return ErrNotFound, nil
		}
		l := m.lender(o.sym, o.actor)
		if l.regSeq == 0 {
			m.nextReg[o.sym]++
			l.regSeq = m.nextReg[o.sym]
		}
		l.idle += o.qty
		m.lent[o.sym] += o.qty
		return nil, nil

	case "borrow":
		m.settle(o.now)
		if len(m.lenders[o.sym]) == 0 {
			return ErrNotFound, nil
		}
		var total int64
		for _, l := range m.lenders[o.sym] {
			total += l.idle
		}
		if total < o.qty {
			return ErrInsufficient, nil
		}
		type si struct {
			seq  int64
			name string
		}
		var order []si
		for name, l := range m.lenders[o.sym] {
			if l.idle > 0 {
				order = append(order, si{l.regSeq, name})
			}
		}
		sort.Slice(order, func(i, j int) bool { return order[i].seq < order[j].seq })
		need := o.qty
		var ids []int64
		for _, e := range order {
			if need == 0 {
				break
			}
			take := need
			if take > m.lenders[o.sym][e.name].idle {
				take = m.lenders[o.sym][e.name].idle
			}
			m.lenders[o.sym][e.name].idle -= take
			m.nextID++
			id := m.nextID
			m.contracts[id] = &naiveContract{
				id: id, lender: e.name, borrower: o.actor, sym: o.sym, qty: take,
			}
			ids = append(ids, id)
			need -= take
		}
		return nil, ids

	case "withdraw":
		m.settle(o.now)
		lmap := m.lenders[o.sym]
		var wl *naiveLender
		if lmap != nil {
			wl = lmap[o.actor]
		}
		if wl == nil {
			return ErrNotFound, nil
		}
		var ordinaryTotal int64
		var ordIDs []int64
		for id, c := range m.contracts {
			if !c.recall && c.lender == o.actor && c.sym == o.sym {
				ordinaryTotal += c.qty
				ordIDs = append(ordIDs, id)
			}
		}
		if o.qty > wl.idle+ordinaryTotal {
			return ErrOverQty, nil
		}
		need := o.qty
		fromIdle := min64(need, wl.idle)
		wl.idle -= fromIdle
		m.handed[o.sym] += fromIdle
		need -= fromIdle

		sort.Slice(ordIDs, func(i, j int) bool { return ordIDs[i] > ordIDs[j] })
		for _, id := range ordIDs {
			if need == 0 {
				break
			}
			orig := m.contracts[id]
			if orig == nil {
				continue
			}
			cq := min64(orig.qty, need)
			// 先替换：其他出借人按登记序号升序，不碰撤回人自己的空闲。
			type si struct {
				seq  int64
				name string
			}
			var others []si
			for name, l := range m.lenders[o.sym] {
				if name != o.actor && l.idle > 0 {
					others = append(others, si{l.regSeq, name})
				}
			}
			sort.Slice(others, func(i, j int) bool { return others[i].seq < others[j].seq })
			replace := cq
			for _, e := range others {
				if replace == 0 {
					break
				}
				take := min64(replace, m.lenders[o.sym][e.name].idle)
				m.lenders[o.sym][e.name].idle -= take
				m.nextID++
				newID := m.nextID
				m.contracts[newID] = &naiveContract{
					id: newID, lender: e.name, borrower: orig.borrower, sym: o.sym, qty: take,
				}
				orig.qty -= take
				m.handed[o.sym] += take
				replace -= take
			}
			replaced := cq - replace
			if replace > 0 { // 后召回
				m.nextID++
				newID := m.nextID
				m.contracts[newID] = &naiveContract{
					id:       newID,
					lender:   orig.lender,
					borrower: orig.borrower,
					sym:      o.sym,
					qty:      replace,
					recall:   true,
					dl:       o.now + m.n,
				}
				orig.qty -= replace
			}
			if orig.qty == 0 {
				delete(m.contracts, orig.id)
			}
			need -= replaced + replace
		}
		return nil, nil

	case "return":
		m.settle(o.now)
		var debt int64
		for _, c := range m.contracts {
			if c.borrower == o.actor && c.sym == o.sym {
				debt += c.qty
			}
		}
		if debt == 0 {
			return ErrNotFound, nil
		}
		if o.qty > debt {
			return ErrOverQty, nil
		}
		need := o.qty
		recalls := m.contractsOf(func(c *naiveContract) bool {
			return c.recall && c.borrower == o.actor && c.sym == o.sym
		})
		sort.Slice(recalls, func(i, j int) bool {
			return recalls[i].dl < recalls[j].dl || (recalls[i].dl == recalls[j].dl && recalls[i].id < recalls[j].id)
		})
		for _, c := range recalls {
			if need == 0 {
				break
			}
			take := min64(need, c.qty)
			c.qty -= take
			m.handed[c.sym] += take
			need -= take
			if c.qty == 0 {
				delete(m.contracts, c.id)
			}
		}
		ord := m.contractsOf(func(c *naiveContract) bool {
			return !c.recall && c.borrower == o.actor && c.sym == o.sym
		})
		sort.Slice(ord, func(i, j int) bool { return ord[i].id < ord[j].id })
		for _, c := range ord {
			if need == 0 {
				break
			}
			take := min64(need, c.qty)
			c.qty -= take
			m.lender(c.sym, c.lender).idle += take
			need -= take
			if c.qty == 0 {
				delete(m.contracts, c.id)
			}
		}
		return nil, nil
	}
	return ErrInvalid, nil
}
