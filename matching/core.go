package matching

import "sort"

func opp(s Side) Side {
	if s == Buy {
		return Sell
	}
	return Buy
}

func validateParams(p OrderParams) error {
	if p.ClientID <= 0 {
		return errf(ErrInvalidParam, "client id must be positive, got %d", p.ClientID)
	}
	if p.Side != Buy && p.Side != Sell {
		return errf(ErrInvalidParam, "invalid side %d", p.Side)
	}
	if p.Price <= 0 {
		return errf(ErrInvalidParam, "price must be positive, got %d", p.Price)
	}
	if p.TotalQty <= 0 {
		return errf(ErrInvalidParam, "total qty must be positive, got %d", p.TotalQty)
	}
	switch p.Type {
	case Limit, Hidden:
		// 普通与隐藏委托没有显示量参数。
	case Iceberg:
		if p.IcebergVisibleQty <= 0 || p.IcebergVisibleQty > p.TotalQty {
			return errf(ErrInvalidParam,
				"iceberg visible qty must be in [1,total], got show=%d total=%d",
				p.IcebergVisibleQty, p.TotalQty)
		}
	default:
		return errf(ErrInvalidParam, "invalid order type %d", p.Type)
	}
	return nil
}

func (e *Engine) levelForInsert(side Side, price int64) *level {
	t := e.tree(side)
	lv := t.get(price)
	if lv == nil {
		lv = &level{price: price}
		t.insert(lv)
	}
	return lv
}

func (e *Engine) tree(side Side) *priceTree {
	if side == Buy {
		return e.bids
	}
	return e.asks
}

// enqueue 把一个已有内部状态的委托按其类型挂入对应价位。
func (e *Engine) enqueue(o *order) {
	o.batch, o.hidden, o.lev = nil, nil, nil
	lv := e.levelForInsert(o.side, o.price)
	o.lev = lv
	switch o.typ {
	case Hidden:
		h := &hiddenNode{order: o}
		o.hidden = h
		lv.pushTailHidden(h)
	default:
		size := o.remaining
		if o.typ == Iceberg {
			size = min(o.showParam, o.remaining)
		}
		b := &batch{order: o, left: size}
		o.batch = b
		lv.pushTailBatch(b)
	}
}

func min(a, b int64) int64 {
	if a < b {
		return a
	}
	return b
}

// crossed 判断以 aggrPrice 发出的 aggrSide 委托是否与价位 price 交叉。
// 买价 >= 卖价即交叉（恰等也成交）。
func crossed(aggrSide Side, aggrPrice, price int64) bool {
	if aggrSide == Buy {
		return aggrPrice >= price
	}
	return aggrPrice <= price
}

// match 让主动委托 aggr 与对手盘逐价位、逐显示批/隐藏委托成交。
func (e *Engine) match(aggr *order) []Fill {
	var fills []Fill
	oppSide := opp(aggr.side)
	t := e.tree(oppSide)

	// 主动方按对手方价格由优到劣逐价位推进。
	for aggr.remaining > 0 {
		var lv *level
		if aggr.side == Buy {
			lv = t.min() // 卖盘最优 = 最低价
		} else {
			lv = t.max() // 买盘最优 = 最高价
		}
		if lv == nil || !crossed(aggr.side, aggr.price, lv.price) {
			break
		}

		// 第一阶段：吃显示队列（含冰山补批），直到队列空。
		for aggr.remaining > 0 {
			b := lv.headBatch()
			if b == nil {
				break
			}
			resting := b.order
			qty := min(aggr.remaining, b.left)
			fills = e.applyFill(aggr, resting, lv.price, qty, fills)

			b.left -= qty
			lv.visSize -= qty

			if b.left == 0 {
				// 整个显示批被吃完：摘队（removeBatch 按 b.left=0 不再改动 visSize）。
				lv.removeBatch(b)
				resting.batch = nil
				if resting.status != StatusCompleted && resting.typ == Iceberg {
					// 仍有冰山储备：立即补批并排到队尾，失去时间优先；
					// 新批在本次吃单后续循环中即可被继续吃到。
					size := min(resting.showParam, resting.remaining)
					nb := &batch{order: resting, left: size}
					resting.batch = nb
					lv.pushTailBatch(nb)
				}
			}
			// 批未吃完（主动委托量不足）时，该批留在队头保持原位。

			// 一次成交只消耗一个显示批，不跨批；下一轮循环再决定吃谁。
		}

		// 第二阶段：显示队列空（全部冰山储备也已耗尽）才吃隐藏委托。
		for aggr.remaining > 0 {
			h := lv.hiddenHead
			if h == nil {
				break
			}
			resting := h.order
			qty := min(aggr.remaining, resting.remaining)
			fills = e.applyFill(aggr, resting, lv.price, qty, fills)

			if resting.status == StatusCompleted {
				lv.removeHidden(h)
				resting.hidden = nil
			}
			// 隐藏委托未吃完时保持在队头，等待后续主动委托继续吃。
		}

		// 无论主动委托是否用完，只要两队列皆空就删除价位节点；
		// 主动委托用完则随后由外层条件退出。
		if lv.empty() {
			t.erase(lv.price)
		}
	}
	return fills
}

// applyFill 记录一笔成交并更新双方账本；成交价为被动方价格。
func (e *Engine) applyFill(aggr, resting *order, price, qty int64, fills []Fill) []Fill {
	for _, o := range [2]*order{aggr, resting} {
		o.filled += qty
		o.remaining -= qty
		if o.remaining == 0 {
			o.status = StatusCompleted
		} else if o.filled > 0 {
			o.status = StatusPartial
		}
	}

	f := Fill{
		Seq:         int64(len(e.fills) + 1),
		Price:       price,
		Qty:         qty,
		Aggressor:   aggr.side,
		AggClientID: aggr.clientID,
	}
	if aggr.side == Buy {
		f.BuyClientID = aggr.clientID
		f.SellClientID = resting.clientID
	} else {
		f.BuyClientID = resting.clientID
		f.SellClientID = aggr.clientID
	}
	e.fills = append(e.fills, f)
	return append(fills, f)
}

// removeFromBook 把委托从其所在价位结构中摘除；价位空时从树中删除。
func (e *Engine) removeFromBook(o *order) {
	lv := o.lev
	if o.batch != nil {
		lv.removeBatch(o.batch)
		o.batch = nil
	}
	if o.hidden != nil {
		lv.removeHidden(o.hidden)
		o.hidden = nil
	}
	o.lev = nil
	if lv != nil && lv.empty() {
		e.tree(o.side).erase(lv.price)
	}
}

func (o *order) snapshot() Order {
	show := o.showParam
	if o.typ == Limit {
		show = o.total
	}
	return Order{
		ClientID:     o.clientID,
		Seq:          o.seq,
		Side:         o.side,
		Price:        o.price,
		Type:         o.typ,
		TotalQty:     o.total,
		FilledQty:    o.filled,
		RemainingQty: o.remaining,
		VisibleQty:   show,
		Status:       o.status,
	}
}

// depthSnapshot 收集一侧全部价位显示量；买盘按价格降序、卖盘按升序（从优到劣）。
func (e *Engine) depthSnapshot(side Side) []DepthAtPrice {
	var out []DepthAtPrice
	e.tree(side).inOrder(func(lv *level) {
		if lv.visSize > 0 {
			out = append(out, DepthAtPrice{Price: lv.price, VisibleQty: lv.visSize})
		}
	})
	if side == Buy {
		sort.Slice(out, func(i, j int) bool { return out[i].Price > out[j].Price })
	}
	return out
}
