package pool

import (
	"container/heap"

	"ontology/loan"
	"ontology/recall"
)

// advance 推进时钟并按 (dl,id) 结算到期召回。必须持锁调用。
func (p *Pool) advance(now int64) {
	p.now = now
	for _, item := range p.sched.Due(now) {
		c, ok := p.book.Get(item.ID)
		if !ok {
			continue // 已在到期前归还了结
		}
		r := c.Qty
		price := p.sym(c.Symbol).price
		pen := recall.Penalty(r, price, p.pen)
		p.payable[c.Borrower] += r*price + pen
		p.book.AddHandedBack(c.Symbol, r)
		p.book.Reduce(c.ID, r)
		p.sched.RecordBuyin(recall.Buyin{
			ContractID: c.ID,
			Qty:        r,
			Price:      price,
			Penalty:    pen,
			At:         c.Dl,
		})
	}
}

// SetPrice 设置某标的现价；入口结算使用旧价（先结算、后改价）。
func (p *Pool) SetPrice(now int64, sym []byte, price int64) error {
	if !validNow(now) || !validID(sym) || !validQty(price) {
		return ErrInvalid
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if now < p.now {
		return ErrClockBack
	}
	p.advance(now)
	st := p.sym(loan.ID(sym))
	st.priced = true
	st.price = price
	return nil
}

// Lend 增加出借人空闲量。
func (p *Pool) Lend(now int64, lender, sym []byte, qty int64) error {
	if !validNow(now) || !validID(lender) || !validID(sym) || !validQty(qty) {
		return ErrInvalid
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if now < p.now {
		return ErrClockBack
	}
	key := loan.ID(sym)
	st := p.sym(key)
	p.advance(now)
	if !st.priced {
		return ErrNotFound
	}
	lkey := loan.ID(lender)
	p.book.AddLend(key, lkey, qty)
	heap.Push(&st.idle, idleEntry{regSeq: p.book.RegSeq(key, lkey), lender: lkey})
	return nil
}

// Borrow 全有或全无地借券，返回本操作生成的合约号（登记序号升序）。
func (p *Pool) Borrow(now int64, borrower, sym []byte, qty int64) ([]int64, error) {
	if !validNow(now) || !validID(borrower) || !validID(sym) || !validQty(qty) {
		return nil, ErrInvalid
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if now < p.now {
		return nil, ErrClockBack
	}
	key := loan.ID(sym)
	p.advance(now)
	if p.book.RegCount(key) == 0 {
		return nil, ErrNotFound
	}
	if p.book.IdleTotal(key) < qty {
		p.scanned = 0
		return nil, ErrInsufficient
	}

	p.scanned = 0
	st := p.sym(key)
	bkey := loan.ID(borrower)
	var ids []int64
	remaining := qty
	for remaining > 0 {
		for st.idle.Len() > 0 && p.book.Idle(key, st.idle[0].lender) == 0 {
			heap.Pop(&st.idle) // 过期空条目：不计 scanned
		}
		if st.idle.Len() == 0 {
			// 不可达：idleTotal 已证明足够；计数仍满足 ≤ 出券人数+1。
			p.scanned++
			panic("pool: idle heap exhausted despite sufficient idle total")
		}
		entry := st.idle[0]
		p.scanned++
		take := remaining
		if avail := p.book.Idle(key, entry.lender); avail < take {
			take = avail
		}
		p.book.TakeIdle(key, entry.lender, take)
		p.nextID++
		id := p.nextID
		p.book.NewOrdinary(id, key, entry.lender, bkey, take)
		ids = append(ids, id)
		remaining -= take
		if p.book.Idle(key, entry.lender) == 0 {
			heap.Pop(&st.idle)
		}
	}
	return ids, nil
}

// Withdraw 出借人撤回空闲量与名下普通合约券。
func (p *Pool) Withdraw(now int64, lender, sym []byte, qty int64) error {
	if !validNow(now) || !validID(lender) || !validID(sym) || !validQty(qty) {
		return ErrInvalid
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if now < p.now {
		return ErrClockBack
	}
	key := loan.ID(sym)
	lkey := loan.ID(lender)
	p.advance(now)
	if !p.book.HasReg(key, lkey) {
		return ErrNotFound
	}
	if qty > p.book.Idle(key, lkey)+p.book.LenderOrdinaryTotal(key, lkey) {
		return ErrOverQty
	}
	st := p.sym(key)
	need := qty

	fromIdle := need
	if avail := p.book.Idle(key, lkey); avail < fromIdle {
		fromIdle = avail
	}
	if fromIdle > 0 {
		p.book.TakeIdle(key, lkey, fromIdle)
		p.book.AddHandedBack(key, fromIdle)
		need -= fromIdle
	}

	ids := p.book.LenderOrdinaryIDs(key, lkey)
	for i := len(ids) - 1; i >= 0 && need > 0; i-- {
		origID := ids[i]
		orig, ok := p.book.Get(origID)
		if !ok {
			continue
		}
		contractQty := min64(orig.Qty, need)

		// 先替换：按登记序号升序取其他出借人的空闲量接手。
		replace := contractQty
		for replace > 0 {
			found := false
			var entry idleEntry
			for st.idle.Len() > 0 {
				top := st.idle[0]
				if top.lender == lkey || p.book.Idle(key, top.lender) == 0 {
					heap.Pop(&st.idle)
					continue
				}
				entry = top
				found = true
				break
			}
			if !found {
				break
			}
			take := min64(replace, p.book.Idle(key, entry.lender))
			p.book.TakeIdle(key, entry.lender, take)
			p.nextID++
			newID := p.nextID
			p.book.SplitReplacement(newID, origID, entry.lender, take)
			p.book.AddHandedBack(key, take)
			if p.book.Idle(key, entry.lender) == 0 {
				heap.Pop(&st.idle)
			}
			replace -= take
		}
		replaced := contractQty - replace

		// 后召回：仍不足的量从原合约拆为召回合约（同一原合约，替换号 < 召回号）。
		if replace > 0 {
			p.nextID++
			newID := p.nextID
			p.book.SplitRecall(newID, now+p.n, origID, replace)
			p.sched.Push(now+p.n, newID)
		}
		need -= replaced + replace
	}
	return nil
}

// Return 借入人还券。
func (p *Pool) Return(now int64, borrower, sym []byte, qty int64) error {
	if !validNow(now) || !validID(borrower) || !validID(sym) || !validQty(qty) {
		return ErrInvalid
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if now < p.now {
		return ErrClockBack
	}
	key := loan.ID(sym)
	bkey := loan.ID(borrower)
	p.advance(now)
	if !p.book.BorrowerHasDebt(key, bkey) {
		return ErrNotFound
	}
	if qty > p.book.Debt(key, bkey) {
		return ErrOverQty
	}
	st := p.sym(key)
	need := qty

	// 先还召回合约：(dl, 合约号) 升序。
	for _, id := range p.book.BorrowerRecallIDs(key, bkey) {
		if need == 0 {
			break
		}
		c, ok := p.book.Get(id)
		if !ok {
			continue
		}
		take := min64(need, c.Qty)
		p.book.ReturnRecall(id, take)
		p.book.AddHandedBack(key, take)
		need -= take
	}
	// 再还普通合约：合约号升序，回到出借人空闲量。
	for _, id := range p.book.BorrowerOrdinaryIDs(key, bkey) {
		if need == 0 {
			break
		}
		c, ok := p.book.Get(id)
		if !ok {
			continue
		}
		take := min64(need, c.Qty)
		p.book.ReturnOrdinary(id, take)
		heap.Push(&st.idle, idleEntry{regSeq: p.book.RegSeq(key, c.Lender), lender: c.Lender})
		need -= take
	}
	return nil
}

// Now 返回当前时钟。
func (p *Pool) Now() int64 {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.now
}

// Price 返回现价；未定价返回 0,false。
func (p *Pool) Price(sym []byte) (int64, bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	st := p.syms[loan.ID(sym)]
	if st == nil || !st.priced {
		return 0, false
	}
	return st.price, true
}

// Contract 按合约号查合约。
func (p *Pool) Contract(id int64) (Contract, bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.book.Get(id)
}

// Payable 返回借入人累计应付金额。
func (p *Pool) Payable(borrower []byte) int64 {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.payable[loan.ID(borrower)]
}

// Buyins 返回买入清单副本。
func (p *Pool) Buyins() []Buyin {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.sched.Buyins()
}
