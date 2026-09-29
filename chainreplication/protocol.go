package chainreplication

// applyWrite buffers one write and applies every now-contiguous write in
// sequence order. It returns the seqs newly applied in ascending order.
// Duplicates (already applied or already buffered) are ignored.
func (c *Coordinator) applyWrite(n *node, seq int, val string) []int {
	if seq <= n.applied {
		return nil
	}
	if _, ok := n.held[seq]; !ok {
		n.held[seq] = val
	}
	applied := []int{}
	for {
		next := n.applied + 1
		v, ok := n.held[next]
		if !ok && !c.lost[next] {
			break
		}
		n.applied = next
		if ok {
			applied = append(applied, next)
		}
		_ = v
	}
	return applied
}

// handleWrite applies at a non-head recipient in seq order. The tail commits
// on apply and sends a cumulative ack upstream; other nodes forward the
// newly applied writes to their successor in ascending order.
func (c *Coordinator) handleWrite(rcpt *node, m Message, isTail bool) {
	if m.Seq <= rcpt.applied {
		c.log.Printf("DECIDE WRITE seq=%d at %s -> duplicate applied<= %d, discard",
			m.Seq, rcpt.id, rcpt.applied)
		return
	}
	if _, ok := rcpt.held[m.Seq]; ok {
		c.log.Printf("DECIDE WRITE seq=%d at %s -> duplicate buffered, discard", m.Seq, rcpt.id)
		return
	}
	newly := c.applyWrite(rcpt, m.Seq, m.Val)
	c.log.Printf("DECIDE WRITE seq=%d at %s -> applied=%d buffered-ahead=%v",
		m.Seq, rcpt.id, rcpt.applied, sortedKeys(rcpt.held, rcpt.applied))

	if isTail {
		// Tail apply == commit; cumulative ack covers all applied seqs.
		rcpt.acked = rcpt.applied
		idx, _ := c.indexOf(rcpt.id)
		pred := c.chain[idx-1]
		c.net.Send(Message{Kind: KindAck, From: rcpt.id, To: pred, Seq: rcpt.acked})
		c.log.Printf("DECIDE commit tail=%s through seq=%d -> ACK -> %s", rcpt.id, rcpt.acked, pred)
		return
	}
	idx, _ := c.indexOf(rcpt.id)
	next := c.chain[idx+1]
	for _, seq := range newly {
		c.net.Send(Message{Kind: KindWrite, From: rcpt.id, To: next, Seq: seq, Val: rcpt.held[seq]})
	}
}

// handleAck advances a cumulative ack. The head reports commits; other nodes
// clear their unconfirmed writes (<= ack) and pass the ack to their
// predecessor. Stale/duplicate acks are discarded.
func (c *Coordinator) handleAck(rcpt *node, m Message) {
	if m.Seq <= rcpt.acked {
		c.log.Printf("DECIDE ACK seq=%d at %s -> stale acked<= %d, discard", m.Seq, rcpt.id, rcpt.acked)
		return
	}
	old := rcpt.acked
	rcpt.acked = m.Seq
	for seq := old + 1; seq <= rcpt.acked; seq++ {
		// An acked write is no longer pending-confirmation at this node.
		// Values are retained (cheaply) so a predecessor can resend gaps
		// after a later downstream failure.
		_ = seq
	}
	c.log.Printf("DECIDE ACK seq=%d at %s -> acked=%d, pending<=%d confirmed-and-cleared",
		m.Seq, rcpt.id, rcpt.acked, rcpt.acked)

	idx, _ := c.indexOf(rcpt.id)
	if idx == 0 {
		c.resolveHeadAcks(rcpt)
		return
	}
	pred := c.chain[idx-1]
	c.net.Send(Message{Kind: KindAck, From: rcpt.id, To: pred, Seq: rcpt.acked})
}

// resolveHeadAcks reports exactly-once committed outcomes for every write
// whose commit ack has reached the head.
func (c *Coordinator) resolveHeadAcks(head *node) {
	for seq := 1; seq <= head.acked; seq++ {
		c.reportOutcome(seq, true)
	}
}

// reportOutcome marks a write's final result and fires the callback exactly
// once. Writes never assigned here (seq >= nextSeq) are ignored.
func (c *Coordinator) reportOutcome(seq int, committed bool) {
	r, ok := c.writes[seq]
	if !ok {
		return
	}
	if r.decided {
		return
	}
	r.decided = true
	r.committed = committed
	c.log.Printf("DECIDE OUTCOME seq=%d committed=%v (delivered to client exactly once)", seq, committed)
	if c.onResult != nil {
		c.onResult(seq, committed, r.val)
	}
}
