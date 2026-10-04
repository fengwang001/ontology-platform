package wave

import (
	"sort"
)

type take struct {
	loc string
	qty int64
}

// allocLine 按三步规则分配一行需求，返回各库位取货（库位不重复）、
// 考察计数与仍缺数量；取货即时计入 slot.reserved（影响后续步的可用量），
// 但尚未写入 reserve 账本（由调用方决定提交或回滚）。
func (c *Coordinator) allocLine(sku string, q int64) ([]take, Probed, int64) {
	takes := []take{}
	var pr Probed
	commit := func(loc string, qty int64) {
		if qty <= 0 {
			return
		}
		_ = c.store.Reserve(loc, qty)
		if n := len(takes); n > 0 && takes[n-1].loc == loc {
			takes[n-1].qty += qty
		} else {
			takes = append(takes, take{loc, qty})
		}
	}
	avail := func(loc string) int64 {
		cur, _ := c.store.Get(loc)
		if cur.Locked {
			return 0
		}
		return cur.OnHand - cur.Reserved
	}

	p, ok := c.store.Pallet(sku)
	if !ok {
		return takes, pr, q
	}
	locs := c.store.Locs(sku)
	need := q

	// (1) 整托：编号字节序，凑满 k 托即停。
	k := need / p
	gotPallets := int64(0)
	for _, b := range locs.Bulk {
		if gotPallets == k {
			break
		}
		pr.BulkPallet++
		a := avail(b.Loc)
		caps := a / p
		if caps <= 0 {
			continue
		}
		pallets := caps
		if rest := k - gotPallets; pallets > rest {
			pallets = rest
		}
		qty := pallets * p
		commit(b.Loc, qty)
		gotPallets += pallets
	}
	remaining := q - gotPallets*p

	// (2) 拆零：Pick 位编号字节序。
	if remaining > 0 {
		for _, pk := range locs.Pick {
			pr.Pick++
			a := avail(pk.Loc)
			if a <= 0 {
				continue
			}
			give := a
			if give > remaining {
				give = remaining
			}
			commit(pk.Loc, give)
			remaining -= give
			if remaining == 0 {
				break
			}
		}
	}

	// (3) 回补：Bulk 位按此刻可用量升序、并列编号字节序；0 跳过。
	if remaining > 0 {
		type bavail struct {
			loc string
			a   int64
		}
		cands := make([]bavail, 0, len(locs.Bulk))
		for _, b := range locs.Bulk {
			if a := avail(b.Loc); a > 0 {
				cands = append(cands, bavail{b.Loc, a})
			}
		}
		sort.Slice(cands, func(i, j int) bool {
			if cands[i].a != cands[j].a {
				return cands[i].a < cands[j].a
			}
			return cands[i].loc < cands[j].loc
		})
		for _, cd := range cands {
			pr.BulkTail++
			give := cd.a
			if give > remaining {
				give = remaining
			}
			commit(cd.loc, give)
			remaining -= give
			if remaining == 0 {
				break
			}
		}
	}

	return takes, pr, remaining
}

func (c *Coordinator) rollback(takes []take) {
	for _, t := range takes {
		c.store.Release(t.loc, t.qty)
	}
}

// allocateOrder 整单全有或全无分配。
func (c *Coordinator) allocateOrder(o *order) (map[string][]Alloc, int64, Probed) {
	allTakes := []take{}
	detail := map[string][]Alloc{}
	var last Probed
	for _, ln := range o.lines {
		takes, pr, missing := c.allocLine(ln.SKU, ln.Qty)
		last = pr
		allTakes = append(allTakes, takes...)
		if missing > 0 {
			c.rollback(allTakes)
			return nil, missing, last
		}
		rows := make([]Alloc, 0, len(takes))
		for _, t := range takes {
			rows = append(rows, Alloc{Loc: t.loc, Qty: t.qty})
		}
		sort.Slice(rows, func(i, j int) bool { return rows[i].Loc < rows[j].Loc })
		detail[ln.SKU] = rows
	}
	for _, t := range allTakes {
		c.ledger.Add(o.id, t.loc, t.qty)
	}
	return detail, 0, last
}

// Release 整批校验后按 priority 降序、订单号升序逐单分配。
func (c *Coordinator) Release(orderIDs []string) ([]OrderResult, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if len(orderIDs) < 1 || len(orderIDs) > 1000 {
		return nil, ErrInvalidArg
	}
	seen := map[string]bool{}
	var firstErr error
	for _, id := range orderIDs {
		if !validBytes1(id) || seen[id] {
			firstErr = ErrInvalidArg
			break
		}
		seen[id] = true
		o, ok := c.orders[id]
		if !ok {
			firstErr = ErrNotFound
			break
		}
		if o.state != StatusNew && o.state != StatusBackorder {
			firstErr = ErrBadState
			break
		}
	}
	if firstErr != nil {
		return nil, firstErr
	}

	ids := append([]string(nil), orderIDs...)
	sort.Slice(ids, func(i, j int) bool {
		a, b := c.orders[ids[i]], c.orders[ids[j]]
		if a.priority != b.priority {
			return a.priority > b.priority
		}
		return a.id < b.id
	})

	results := make([]OrderResult, 0, len(ids))
	for _, id := range ids {
		o := c.orders[id]
		detail, missing, pr := c.allocateOrder(o)
		c.probed = pr
		res := OrderResult{Order: id}
		if missing > 0 {
			o.state = StatusBackorder
			res.State = StatusBackorder
			res.Err = ErrQtyMismatch
			results = append(results, res)
			continue
		}
		o.state = StatusAllocated
		res.State = StatusAllocated
		res.Lines = detail
		results = append(results, res)
	}
	return results, nil
}

// Pick 按整条预占记录拣货，并据残余预占/缺口刷新订单状态。
func (c *Coordinator) Pick(orderID, loc string, qty int64) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	if !validBytes1(orderID) || !validBytes1(loc) || qty < 1 {
		return ErrInvalidArg
	}
	o, ok := c.orders[orderID]
	if !ok {
		return ErrNotFound
	}
	if _, ok := c.store.Get(loc); !ok {
		return ErrNotFound
	}
	recQty, ok := c.ledger.Qty(orderID, loc)
	if !ok {
		return ErrNotFound
	}
	if qty != recQty {
		return ErrQtyMismatch
	}
	if err := c.store.Pick(loc, qty); err != nil {
		return err
	}
	c.ledger.Delete(orderID, loc)
	c.refreshState(o)
	return nil
}

type deficit struct {
	order *order
	qty   int64
}

// ShortPick 短拣：实拣 found、锁定库位、删除其上全部预占，随后重分配。
func (c *Coordinator) ShortPick(orderID, loc string, found int64) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	if !validBytes1(orderID) || !validBytes1(loc) || found < 0 {
		return ErrInvalidArg
	}
	o, ok := c.orders[orderID]
	if !ok {
		return ErrNotFound
	}
	cur, ok := c.store.Get(loc)
	if !ok {
		return ErrNotFound
	}
	recQty, ok := c.ledger.Qty(orderID, loc)
	if !ok {
		return ErrNotFound
	}
	if found >= recQty {
		return ErrQtyMismatch
	}

	sku := cur.SKU
	unpicked := recQty
	_ = c.store.Adjust(loc, found) // onHand -= found
	c.store.Release(loc, unpicked) // reserved -= 未拣量
	c.ledger.Delete(orderID, loc)
	affected := c.ledger.DropLoc(loc) // 删除该库位上其他订单的全部预占
	for _, r := range affected {
		c.store.Release(r.Loc, r.Qty)
	}
	_ = c.store.Lock(loc)

	queue := []deficit{{order: o, qty: unpicked - found}}
	for _, r := range affected {
		queue = append(queue, deficit{order: c.orders[r.Order], qty: r.Qty})
	}
	sort.SliceStable(queue[1:], func(i, j int) bool {
		a, b := queue[1+i].order, queue[1+j].order
		if a.priority != b.priority {
			return a.priority > b.priority
		}
		return a.id < b.id
	})

	touched := map[string]*order{}
	for _, d := range queue {
		takes, pr, missing := c.allocLine(sku, d.qty)
		c.probed = pr
		for _, t := range takes {
			c.ledger.Add(d.order.id, t.loc, t.qty)
		}
		if missing > 0 {
			d.order.short += missing
		}
		touched[d.order.id] = d.order
	}
	for _, d := range touched {
		c.refreshState(d)
	}
	return nil
}

// refreshState 在订单无剩余预占时据缺口置 Done/Short；有残余则保持可拣状态。
func (c *Coordinator) refreshState(o *order) {
	if c.ledger.OrderTotal(o.id) == 0 {
		if o.short == 0 {
			o.state = StatusDone
		} else {
			o.state = StatusShort
		}
		return
	}
	if o.state != StatusAllocated {
		o.state = StatusAllocated
	}
}
