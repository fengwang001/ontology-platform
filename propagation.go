package ontology

import "math/bits"

func (n *Node) piggybackCap() int {
	return n.lambda * bits.Len(uint(len(n.members)))
}

func (n *Node) Outgoing() (OutgoingMessage, error) {
	n.mu.Lock()
	defer n.mu.Unlock()

	if n.exited {
		err := reject("Outgoing", RejectNodeExited, n.self)
		n.logf("input=Outgoing reject reason=%s", err)
		return OutgoingMessage{}, err
	}

	candidates := make([]*queuedUpdate, 0, len(n.pending))
	for _, member := range n.members {
		if queued := n.pending[member]; queued != nil && queued.sent < n.piggybackCap() {
			candidates = append(candidates, queued)
		}
	}
	sortQueued(candidates)

	limit := n.batchSize
	if len(candidates) < limit {
		limit = len(candidates)
	}
	updates := make([]Message, 0, limit)
	for _, queued := range candidates[:limit] {
		queued.sent++
		updates = append(updates, queued.update)
		if queued.sent >= n.piggybackCap() {
			delete(n.pending, queued.update.Member)
		}
	}

	n.logf("input=Outgoing cap=%d B=%d output=%v", n.piggybackCap(), n.batchSize, updates)
	return OutgoingMessage{Updates: updates}, nil
}

func sortQueued(items []*queuedUpdate) {
	for i := 1; i < len(items); i++ {
		for j := i; j > 0 && queuedLess(items[j], items[j-1]); j-- {
			items[j], items[j-1] = items[j-1], items[j]
		}
	}
}

func queuedLess(left, right *queuedUpdate) bool {
	if left.sent != right.sent {
		return left.sent < right.sent
	}
	return left.update.Member < right.update.Member
}
