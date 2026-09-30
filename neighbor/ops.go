package neighbor

// Tick only performs expiry processing up to now and advances the clock.
func (c *Cache) Tick(now Time) (Result, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	res, seq, err := c.begin(now, "TICK", "")
	if err != nil {
		return Result{}, err
	}
	c.logf("#%d TICK complete [basis: standalone clock advance]", seq)
	return finish(c, seq, "TICK", &res), nil
}

// Send processes an outbound packet for addr after expiry processing:
//   - no entry: create Incomplete, queue pkt, send the 1st request (next
//     retransmit at now+T);
//   - Incomplete: queue pkt (oldest evicted once the queue exceeds Q);
//   - Reachable, Delay, Probe: deliver pkt immediately;
//   - Stale: deliver pkt and enter Delay (deadline now+Dl).
func (c *Cache) Send(now Time, addr Addr, pkt Packet) (Result, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	res, seq, err := c.begin(now, "SEND", "addr="+string(addr))
	if err != nil {
		return Result{}, err
	}
	if addr == "" {
		c.logf("#%d reject %v [basis: empty neighbor address]", seq, ErrEmptyAddr)
		return res, ErrEmptyAddr
	}
	e, exists := c.entries[addr]
	if !exists {
		if c.cfg.MaxEntries > 0 && len(c.entries) >= c.cfg.MaxEntries {
			c.logf("#%d reject addr=%s %v [basis: entry limit %d reached after expiry]", seq, addr, ErrCacheFull, c.cfg.MaxEntries)
			return res, ErrCacheFull
		}
		e = &entry{state: Incomplete, sent: 1, deadline: now + c.cfg.Retransmit}
		c.entries[addr] = e
		res.Requests = append(res.Requests, addr)
		c.logf("#%d addr=%s no entry -> create INCOMPLETE, send request #1, next retransmit=%d [basis: first send to unresolved neighbor]", seq, addr, e.deadline)
	}
	switch e.state {
	case Incomplete:
		c.enqueue(e, pkt, &res)
		c.logf("#%d addr=%s queue packet=%v (queued=%d) [basis: state %s]", seq, addr, pkt, len(e.queue), e.state)
	case Stale:
		e.state = Delay
		e.deadline = now + c.cfg.DelayTime
		res.Delivered = append(res.Delivered, Delivery{Addr: addr, Link: e.link, Packet: pkt})
		c.logf("#%d addr=%s deliver packet=%v via %s; STALE -> DELAY deadline=%d [basis: send to stale neighbor]", seq, addr, pkt, e.link, e.deadline)
	default: // Reachable, Delay, Probe
		res.Delivered = append(res.Delivered, Delivery{Addr: addr, Link: e.link, Packet: pkt})
		c.logf("#%d addr=%s deliver packet=%v via %s [basis: state %s]", seq, addr, pkt, e.link, e.state)
	}
	return finish(c, seq, "SEND", &res), nil
}

// Advertisement processes a neighbor advertisement carrying link for addr.
// solicited says whether it answers one of our requests; override is the
// advertisement override flag.
func (c *Cache) Advertisement(now Time, addr Addr, link LinkAddr, solicited bool, override bool) (Result, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	kind := "unsolicited"
	if solicited {
		kind = "solicited"
	}
	res, seq, err := c.begin(now, "ADV", "addr="+string(addr)+" link="+string(link)+" "+kind+" override="+boolStr(override))
	if err != nil {
		return Result{}, err
	}
	if addr == "" {
		c.logf("#%d reject %v [basis: empty neighbor address]", seq, ErrEmptyAddr)
		return res, ErrEmptyAddr
	}
	if link == "" {
		c.logf("#%d reject %v [basis: empty link-layer address]", seq, ErrEmptyLinkAddr)
		return res, ErrEmptyLinkAddr
	}
	e := c.entries[addr]
	if e == nil {
		c.logf("#%d reject addr=%s %v [basis: no entry after expiry]", seq, addr, ErrNoEntry)
		return res, ErrNoEntry
	}

	if e.state == Incomplete {
		// Any advertisement completes resolution: record the link-layer
		// address and flush queued packets in enqueue order.
		e.link = link
		queued := e.queue
		e.queue = nil
		for _, pkt := range queued {
			res.Delivered = append(res.Delivered, Delivery{Addr: addr, Link: link, Packet: pkt})
		}
		if solicited {
			e.state = Reachable
			e.deadline = now + c.cfg.ReachableTime
			c.logf("#%d addr=%s INCOMPLETE record link=%s, flush %d queued packet(s) FIFO -> REACHABLE deadline=%d [basis: solicited advertisement]", seq, addr, link, len(queued), e.deadline)
		} else {
			e.state = Stale
			e.deadline = 0
			c.logf("#%d addr=%s INCOMPLETE record link=%s, flush %d queued packet(s) FIFO -> STALE [basis: unsolicited advertisement]", seq, addr, link, len(queued))
		}
		return finish(c, seq, "ADV", &res), nil
	}

	// Reachable, Stale, Delay, Probe.
	from := e.state
	switch {
	case override || link == e.link:
		changed := e.link != link
		e.link = link
		switch {
		case solicited:
			e.state = Reachable
			e.deadline = now + c.cfg.ReachableTime
			c.logf("#%d addr=%s %s record link=%s -> REACHABLE deadline=%d [basis: solicited override-or-same advertisement]", seq, addr, from, link, e.deadline)
		case changed:
			e.state = Stale
			e.deadline = 0
			c.logf("#%d addr=%s %s record link=%s (changed) -> STALE [basis: unsolicited advertisement with new link-layer address]", seq, addr, from, link)
		default:
			c.logf("#%d addr=%s keep state=%s link=%s [basis: unsolicited advertisement with unchanged address]", seq, addr, e.state, link)
		}
	default: // !override && link differs
		if e.state == Reachable {
			e.state = Stale
			e.deadline = 0
			c.logf("#%d addr=%s %s ignore link=%s without override -> STALE [basis: non-override different address invalidates reachability]", seq, addr, from, link)
		} else {
			c.logf("#%d addr=%s ignore link=%s without override, keep state=%s and recorded link=%s [basis: non-override different address in non-reachable state]", seq, addr, link, e.state, e.link)
		}
	}
	return finish(c, seq, "ADV", &res), nil
}

// Confirm records an upper-layer reachability confirmation. Only Delay or
// Probe entries move to Reachable; other states are unchanged.
func (c *Cache) Confirm(now Time, addr Addr) (Result, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	res, seq, err := c.begin(now, "CONFIRM", "addr="+string(addr))
	if err != nil {
		return Result{}, err
	}
	if addr == "" {
		c.logf("#%d reject %v [basis: empty neighbor address]", seq, ErrEmptyAddr)
		return res, ErrEmptyAddr
	}
	e := c.entries[addr]
	if e == nil {
		c.logf("#%d reject addr=%s %v [basis: no entry after expiry]", seq, addr, ErrNoEntry)
		return res, ErrNoEntry
	}
	if e.state == Delay || e.state == Probe {
		e.state = Reachable
		e.deadline = now + c.cfg.ReachableTime
		c.logf("#%d addr=%s %s -> REACHABLE deadline=%d [basis: upper-layer confirmation]", seq, addr, e.state, e.deadline)
	} else {
		c.logf("#%d addr=%s keep state=%s [basis: upper-layer confirmation only affects DELAY/PROBE]", seq, addr, e.state)
	}
	return finish(c, seq, "CONFIRM", &res), nil
}
