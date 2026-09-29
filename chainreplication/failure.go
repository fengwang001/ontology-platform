package chainreplication

// Fail announces a node failure and reorganises the chain.
//
// All messages sent by the failed node or still in flight touching it are
// discarded first; only then is the chain repaired:
//   - tail failure: predecessor becomes tail and commits all its writes now;
//   - middle failure: predecessor resends exactly the gap after the new
//     successor's max applied seq;
//   - head failure: successor becomes head and unapplied writes are reported
//     uncommitted.
func (c *Coordinator) Fail(id string) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	if _, ok := c.nodes[id]; !ok {
		c.log.Printf("INPUT Fail node=%q -> REJECT unknown-node (state unchanged)", id)
		return ErrUnknownNode
	}
	idx, alive := c.indexOf(id)
	if !alive {
		c.log.Printf("INPUT Fail node=%q -> REJECT already-failed (state unchanged)", id)
		return ErrNodeFailed
	}
	if len(c.chain) == 1 {
		c.log.Printf("INPUT Fail node=%q -> REJECT last-alive-node (state unchanged)", id)
		return ErrLastNodeAlive
	}

	// 1) Discard failed node's sent/in-flight messages.
	c.net.FailNode(id)

	atHead := idx == 0
	atTail := idx == len(c.chain)-1
	var predID, succID string
	if !atHead {
		predID = c.chain[idx-1]
	}
	if !atTail {
		succID = c.chain[idx+1]
	}

	// 2) Remove from alive chain.
	c.chain = append(c.chain[:idx], c.chain[idx+1:]...)
	c.nodes[id].failed = true
	c.log.Printf("INPUT Fail node=%s -> OUTPUT removed; new chain=%v head=%s tail=%s",
		id, c.chain, c.chain[0], c.chain[len(c.chain)-1])

	switch {
	case atTail:
		c.repairTailFailure(predID)
	case atHead:
		c.repairHeadFailure(succID)
	default:
		c.repairMiddleFailure(predID, succID)
	}
	return nil
}

// repairTailFailure: the predecessor becomes tail and immediately commits all
// writes it has applied (its whole pending-confirmation set).
func (c *Coordinator) repairTailFailure(newTailID string) {
	nt := c.nodes[newTailID]
	nt.acked = nt.applied
	c.log.Printf("DECIDE tail-failure: %s becomes tail; commits seqs 1..%d immediately",
		newTailID, nt.applied)

	idx, _ := c.indexOf(newTailID)
	if idx == 0 {
		// Only one survivor: it is head and tail.
		c.resolveHeadAcks(nt)
		return
	}
	pred := c.chain[idx-1]
	c.net.Send(Message{Kind: KindAck, From: nt.id, To: pred, Seq: nt.acked})
}

// repairMiddleFailure: the predecessor resends only writes with seq greater
// than the new successor's max applied seq, and only those it holds.
func (c *Coordinator) repairMiddleFailure(predID, newSuccID string) {
	pred := c.nodes[predID]
	succ := c.nodes[newSuccID]
	from := succ.applied + 1
	to := pred.applied
	c.log.Printf("DECIDE middle-failure: %s bridges to %s; resend gap seqs (%d..%d] i.e. %d..%d",
		predID, newSuccID, succ.applied, pred.applied, from, to)
	for seq := from; seq <= to; seq++ {
		val, ok := pred.held[seq]
		if !ok {
			continue
		}
		c.net.Send(Message{Kind: KindWrite, From: predID, To: newSuccID, Seq: seq, Val: val})
	}
}

// repairHeadFailure: the successor becomes head. Writes the new head has not
// applied never survived and are reported uncommitted; writes it applied
// remain on the surviving chain and commit normally (acks now target it).
func (c *Coordinator) repairHeadFailure(newHeadID string) {
	nh := c.nodes[newHeadID]
	c.log.Printf("DECIDE head-failure: %s becomes head; applied=%d", newHeadID, nh.applied)

	// Orphan values buffered at the new head (a seq whose predecessor hole
	// died with the old head) are uncommitted and dropped.
	for seq := range nh.held {
		if seq > nh.applied {
			delete(nh.held, seq)
		}
	}

	// Everything the new head did not apply (by seq number) is lost: report
	// uncommitted exactly once and record it as a global hole that every
	// survivor's prefix watermark may jump over.
	lostFrom := nh.applied + 1
	lostTo := c.nextSeq - 1
	for seq := lostFrom; seq <= lostTo; seq++ {
		c.lost[seq] = true
		c.reportOutcome(seq, false)
	}
	if lostTo >= lostFrom {
		c.log.Printf("DECIDE head-failure: seqs %d..%d marked lost/uncommitted", lostFrom, lostTo)
		// Every survivor jumps its applied/acked watermarks over the lost
		// range; values those nodes may hold for those seqs are removed.
		// This keeps later (higher-seq) writes applying without a permanent
		// hole, while reads and commits skip the lost numbers.
		for _, n := range c.nodes {
			if n.failed {
				continue
			}
			for seq := lostFrom; seq <= lostTo; seq++ {
				delete(n.held, seq)
			}
			if n.applied < lostTo {
				n.applied = lostTo
			}
			if n.acked < lostTo {
				n.acked = lostTo
			}
		}
	}

	// If the surviving tail already committed further writes whose acks died
	// with the old head, re-issue the tail's cumulative ack so the new head
	// learns them (an acknowledged write is never lost).
	tail := c.nodes[c.chain[len(c.chain)-1]]
	if len(c.chain) > 1 && tail.applied > nh.acked {
		c.log.Printf("DECIDE head-failure: tail %s applied to %d; re-issuing cumulative ack",
			tail.id, tail.applied)
		pred := c.chain[len(c.chain)-2]
		if tail.acked < tail.applied {
			tail.acked = tail.applied
		}
		c.net.Send(Message{Kind: KindAck, From: tail.id, To: pred, Seq: tail.acked})
	}

	// Head-side gap refill: resend held writes beyond the successor's
	// watermark (mirrors the middle-failure rule).
	if len(c.chain) > 1 {
		succ := c.nodes[c.chain[1]]
		c.log.Printf("DECIDE head-failure: %s resends gap after successor applied=%d (own applied=%d)",
			newHeadID, succ.applied, nh.applied)
		for seq := succ.applied + 1; seq <= nh.applied; seq++ {
			val, ok := nh.held[seq]
			if !ok {
				continue
			}
			c.net.Send(Message{Kind: KindWrite, From: newHeadID, To: c.chain[1], Seq: seq, Val: val})
		}
	}

	// Applied writes that are not yet acked to the new head remain pending
	// and resolve when the cumulative ack arrives.
	c.resolveHeadAcks(nh)
}
