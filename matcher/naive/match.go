package naive

func (e *Engine) match(taker *orderView) []Trade {
	var out []Trade
	opp := opposite(taker.side)
	rem := func() int64 { return taker.total - taker.filled }

	for rem() > 0 {
		price, ok := e.bestPrice(opp)
		if !ok || !crosses(taker.side, taker.price, price) {
			break
		}
		l := e.levels[opp][price]

		// Visible batches first; exactly one batch per trade; a replenished
		// iceberg batch re-enters at the tail and stays reachable this call.
		for rem() > 0 && len(l.visible) > 0 {
			q := rem()
			if l.visible[0].Size < q {
				q = l.visible[0].Size
			}
			makerID := l.visible[0].ID
			maker := e.orders[makerID]
			maker.filled += q
			taker.filled += q
			out = append(out, e.emit(taker.id, makerID, taker.side, l.price, q))

			if q < l.visible[0].Size {
				l.visible[0].Size -= q
				continue
			}
			l.visible = l.visible[1:]
			mr := maker.total - maker.filled
			if mr > 0 && maker.kind == Iceberg {
				fresh := maker.displaySize
				if mr < fresh {
					fresh = mr
				}
				l.reserve[maker.id] -= fresh
				if l.reserve[maker.id] == 0 {
					delete(l.reserve, maker.id)
				}
				l.visible = append(l.visible, Slot{
					// Replenishment changes queue position but does not
					// create a new accepted order, so it consumes no new
					// sequence number; tail position is encoded by slice
					// append order directly.
					ID: maker.id, Seq: maker.seq, Size: fresh,
				})
			}
		}

		// Hidden only once every visible batch and all reserve is gone.
		for rem() > 0 && len(l.visible) == 0 && len(l.reserve) == 0 && len(l.hidden) > 0 {
			q := rem()
			if l.hidden[0].Size < q {
				q = l.hidden[0].Size
			}
			makerID := l.hidden[0].ID
			maker := e.orders[makerID]
			maker.filled += q
			taker.filled += q
			out = append(out, e.emit(taker.id, makerID, taker.side, l.price, q))
			if q < l.hidden[0].Size {
				l.hidden[0].Size -= q
			} else {
				l.hidden = l.hidden[1:]
			}
		}

		if l.isEmpty() {
			delete(e.levels[opp], l.price)
		}
	}
	return out
}

func (e *Engine) rest(o *orderView) {
	l := e.lvl(o.side, o.price)
	rem := o.total - o.filled
	switch o.kind {
	case Plain:
		l.visible = append(l.visible, Slot{ID: o.id, Seq: o.seq, Size: rem})
	case Iceberg:
		fresh := o.displaySize
		if rem < fresh {
			fresh = rem
		}
		l.visible = append(l.visible, Slot{ID: o.id, Seq: o.seq, Size: fresh})
		if rem-fresh > 0 {
			l.reserve[o.id] += rem - fresh
		}
	case Hidden:
		l.hidden = append(l.hidden, hiddenEntry{ID: o.id, Seq: o.seq, Size: rem})
	}
}
