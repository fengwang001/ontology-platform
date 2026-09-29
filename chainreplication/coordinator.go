package chainreplication

// Write assigns the next consecutive sequence at the head and forwards it.
// The returned handle is resolved exactly once: true means committed,
// false means uncommitted (the write can no longer reach a tail).
func (c *Coordinator) Write(headID, value string) (*WriteHandle, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	head := c.chain[0]
	n, ok := c.alive[headID]
	if !ok {
		return nil, c.reject(RejectUnknownNode, "Write", headID, "no such node in chain")
	}
	if n.failed {
		return nil, c.reject(RejectNodeAlreadyFailed, "Write", headID, "node has failed")
	}
	if headID != head {
		return nil, c.reject(RejectNotHead, "Write", headID, "writes are accepted only by head "+head)
	}

	seq := c.nextSeq
	c.nextSeq++
	out := make(chan bool, 1)
	c.ch[seq] = out
	h := &WriteHandle{seq: seq, outcome: out}

	msg := Message{Type: MsgWrite, Seq: seq, Value: value, Epoch: c.epoch, From: "client", To: head}
	c.logger.Logf("INPUT Write node=%s value=%q -> OUTPUT assigned seq=%d; DECISION head=%s accepts and forwards",
		headID, value, seq, head)
	c.applyWrite(n, &msg)
	return h, nil
}

// Read is served only by the current tail. ok=false means no committed value.
func (c *Coordinator) Read(tailID string) (value string, seq int, ok bool, err error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	tail := c.chain[len(c.chain)-1]
	n, exists := c.alive[tailID]
	if !exists {
		return "", 0, false, c.reject(RejectUnknownNode, "Read", tailID, "no such node in chain")
	}
	if n.failed {
		return "", 0, false, c.reject(RejectNodeAlreadyFailed, "Read", tailID, "node has failed")
	}
	if tailID != tail {
		return "", 0, false, c.reject(RejectNotTail, "Read", tailID, "reads are served only by tail "+tail)
	}
	value, ok = n.values[n.applied]
	c.logger.Logf("INPUT Read node=%s -> OUTPUT seq=%d value=%q ok=%t; DECISION tail exposes only committed (tail-applied) writes",
		tailID, n.applied, value, ok)
	return value, n.applied, ok, nil
}

// Fail removes a node, drops all of its messages and reconfigures the chain.
func (c *Coordinator) Fail(id string) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	n, ok := c.alive[id]
	if !ok {
		return c.reject(RejectUnknownNode, "Fail", id, "no such node in chain")
	}
	if n.failed {
		return c.reject(RejectNodeAlreadyFailed, "Fail", id, "node already failed")
	}
	if len(c.chain) == 1 {
		return c.reject(RejectLastSurvivor, "Fail", id, "refusing to fail the last surviving node")
	}

	idx := indexOf(c.chain, id)
	n.failed = true
	c.chain = append(c.chain[:idx], c.chain[idx+1:]...)
	c.epoch++
	epoch := c.epoch
	c.net.DropNode(id)
	c.logger.Logf("INPUT Fail node=%s position=%d -> OUTPUT epoch=%d chain=%v; DECISION discard every message involving the failed node",
		id, idx, epoch, c.chain)

	switch {
	case idx == len(c.chain):
		newTail := c.alive[c.chain[len(c.chain)-1]]
		c.logger.Logf("DECISION tail failed -> node=%s becomes tail and immediately commits its pending writes", newTail.id)
		c.commitPending(newTail, epoch)
	case idx == 0:
		newHead := c.alive[c.chain[0]]
		c.logger.Logf("DECISION head failed -> node=%s becomes head; writes it never applied are reported uncommitted", newHead.id)
		c.headFailResolve(newHead, epoch)
	default:
		pred := c.alive[c.chain[idx-1]]
		succ := c.alive[c.chain[idx]]
		c.logger.Logf("DECISION middle node failed -> predecessor=%s retransmits seq in (%d,%d] (successor=%s applied max)",
			pred.id, succ.applied, pred.applied, succ.id)
		c.retransmitGap(pred, succ, epoch)
	}
	return nil
}

func (c *Coordinator) reject(r RejectReason, op, id, detail string) error {
	err := &RejectError{r, op + " " + id + ": " + detail}
	c.logger.Logf("INPUT %s node=%s -> OUTPUT REJECTED reason=%s; DECISION operation changes no state", op, id, r)
	return err
}

// Deliver is the network injection entry point. Stale-epoch messages,
// messages touching failed nodes and duplicate writes are dropped.
func (c *Coordinator) Deliver(m Message) {
	c.mu.Lock()
	defer c.mu.Unlock()

	to, okTo := c.alive[m.To]
	if !okTo || to.failed || indexOf(c.chain, m.To) < 0 {
		c.logger.Logf("INPUT Deliver %s seq=%d to=%s -> OUTPUT dropped; DECISION recipient missing or failed",
			msgTypeName(m.Type), m.Seq, m.To)
		return
	}
	if m.From != "client" {
		from, okFrom := c.alive[m.From]
		if !okFrom || from.failed || indexOf(c.chain, m.From) < 0 {
			c.logger.Logf("INPUT Deliver %s seq=%d from=%s -> OUTPUT dropped; DECISION sender missing or failed",
				msgTypeName(m.Type), m.Seq, m.From)
			return
		}
	}
	c.logger.Logf("INPUT Deliver %s seq=%d from=%s to=%s epoch=%d",
		msgTypeName(m.Type), m.Seq, m.From, m.To, m.Epoch)
	switch m.Type {
	case MsgWrite:
		c.applyWrite(to, &m)
	case MsgAck:
		c.applyAck(c.alive[m.From], to, m.Seq, m.Epoch)
	}
}

// applyWrite applies a write to a node, buffering out-of-order messages
// until sequence gaps are filled; duplicates are discarded.
func (c *Coordinator) applyWrite(n *node, m *Message) {
	if m.Seq <= n.applied {
		c.logger.Logf("DECISION node=%s discards duplicate seq=%d (applied max=%d)", n.id, m.Seq, n.applied)
		// A duplicate from a downstream neighbour can carry an ack boundary that
		// was interrupted by a failure; echo the current boundary upstream.
		if m.From != "client" && downstream(c.chain, n.id, m.From) {
			c.sendAck(m.From, n.id, n.applied, m.Epoch)
		}
		return
	}
	if _, held := n.buffer[m.Seq]; held {
		c.logger.Logf("DECISION node=%s discards duplicated in-flight seq=%d", n.id, m.Seq)
		return
	}
	n.buffer[m.Seq] = m
	for {
		next := n.applied + 1
		msg, ok := n.buffer[next]
		if !ok {
			c.logger.Logf("DECISION node=%s buffered seq=%d, waiting for gap seq=%d; buffered count=%d",
				n.id, m.Seq, next, len(n.buffer))
			return
		}
		delete(n.buffer, next)
		n.applied = next
		n.values[next] = msg.Value
		n.pending[next] = true

		if n.id == c.chain[len(c.chain)-1] {
			delete(n.pending, next)
			c.logger.Logf("DECISION tail=%s applies seq=%d -> COMMITTED; ack now travels upstream", n.id, next)
			if len(c.chain) > 1 {
				c.sendAck(n.id, c.chain[len(c.chain)-2], next, msg.Epoch)
			} else {
				c.resolve(next, true)
			}
			continue
		}
		c.forward(n, msg)
	}
}

func (c *Coordinator) forward(n *node, m *Message) {
	nextID := c.chain[indexOf(c.chain, n.id)+1]
	out := *m
	out.From = n.id
	out.To = nextID
	c.logger.Logf("DECISION node=%s forwards seq=%d to node=%s", n.id, m.Seq, nextID)
	c.net.Send(out)
}

func (c *Coordinator) sendAck(from, to string, seq, epoch int) {
	c.logger.Logf("DECISION node=%s emits ack seq=%d to node=%s", from, seq, to)
	c.net.Send(Message{Type: MsgAck, Seq: seq, Epoch: epoch, From: from, To: to})
}

// applyAck clears pending writes not above seq and propagates the ack
// upstream; at the head the corresponding write outcome is committed.
func (c *Coordinator) applyAck(from, to *node, seq, epoch int) {
	cleared := 0
	for s := range to.pending {
		if s <= seq {
			delete(to.pending, s)
			cleared++
		}
	}
	c.logger.Logf("DECISION node=%s receives ack seq=%d from downstream=%s; clears %d pending (applied max=%d)",
		to.id, seq, from.id, cleared, to.applied)

	if to.id == c.chain[0] {
		c.resolve(seq, true)
		return
	}
	idx := indexOf(c.chain, to.id)
	c.sendAck(to.id, c.chain[idx-1], seq, epoch)
}

// commitPending is used when a node becomes the new tail: every applied but
// unacknowledged write commits immediately; acks propagate upstream.
func (c *Coordinator) commitPending(n *node, epoch int) {
	if len(n.pending) == 0 {
		c.logger.Logf("DECISION new tail=%s has no pending write to commit", n.id)
		return
	}
	max := 0
	for s := range n.pending {
		if s > max {
			max = s
		}
	}
	for s := range n.pending {
		if s <= max {
			delete(n.pending, s)
		}
	}
	c.logger.Logf("DECISION new tail=%s commits every pending write through seq=%d", n.id, max)
	if len(c.chain) > 1 {
		idx := indexOf(c.chain, n.id)
		c.sendAck(n.id, c.chain[idx-1], max, epoch)
	} else {
		c.resolve(max, true)
	}
}

// retransmitGap re-sends from predecessor to the new successor exactly the
// writes the successor has not applied but the predecessor has.
func (c *Coordinator) retransmitGap(pred, succ *node, epoch int) {
	if pred.applied <= succ.applied {
		c.logger.Logf("DECISION predecessor=%s holds no missing write for successor=%s (applied %d<=%d); heal ack boundary instead",
			pred.id, succ.id, pred.applied, succ.applied)
		c.sendAck(succ.id, pred.id, succ.applied, epoch)
		return
	}
	for s := succ.applied + 1; s <= pred.applied; s++ {
		value, ok := pred.values[s]
		if !ok {
			continue
		}
		c.logger.Logf("DECISION predecessor=%s retransmits missing seq=%d to new successor=%s", pred.id, s, succ.id)
		c.net.Send(Message{Type: MsgWrite, Seq: s, Value: value, Epoch: epoch, From: pred.id, To: succ.id})
	}
}

// headFailResolve handles a new head after the old one fails. Writes the new
// head never applied can never reach a tail, so they are uncommitted; stale
// buffered writes are discarded and sequence allocation continues with no
// gap above the new head's applied prefix.
func (c *Coordinator) headFailResolve(newHead *node, epoch int) {
	applied := newHead.applied
	// Stale buffered writes beyond the new head's applied prefix can never
	// commit; purge them from every surviving node so later writes reusing a
	// sequence cannot be confused with them.
	for _, id := range c.chain {
		nd := c.alive[id]
		for s := range nd.buffer {
			if s > applied {
				delete(nd.buffer, s)
			}
		}
	}
	for seq, ch := range c.ch {
		// Writes the new head applied already live on it and downstream nodes,
		// so they still commit via the ordinary tail ack path. Only writes the
		// new head never applied can no longer reach a tail.
		if seq > applied {
			c.fire(seq, ch, false)
		}
	}
	c.nextSeq = applied + 1
	if newHead.id == c.chain[len(c.chain)-1] {
		c.commitPending(newHead, epoch)
	}
}

// resolve reports all unresolved outcomes up to seq; it never double-fires
// because each channel is deleted when resolved.
func (c *Coordinator) resolve(seq int, committed bool) {
	for s, ch := range c.ch {
		if s <= seq {
			c.fire(s, ch, committed)
		}
	}
}

func (c *Coordinator) fire(seq int, ch chan bool, committed bool) {
	ch <- committed
	close(ch)
	delete(c.ch, seq)
	c.logger.Logf("OUTPUT write seq=%d result=%s; DECISION delivered exactly once",
		seq, map[bool]string{true: "COMMITTED", false: "UNCOMMITTED"}[committed])
}
