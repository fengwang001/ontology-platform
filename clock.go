package ontology

import "time"

func (n *Node) AdvanceClock(elapsed time.Duration) (int, error) {
	n.mu.Lock()
	defer n.mu.Unlock()

	if n.exited {
		err := reject("AdvanceClock", RejectNodeExited, n.self)
		n.logf("input=AdvanceClock(%s) reject reason=%s", elapsed, err)
		return 0, err
	}
	if elapsed < 0 {
		err := reject("AdvanceClock", RejectInvalidParameter, "elapsed must not be negative")
		n.logf("input=AdvanceClock(%s) reject reason=%s", elapsed, err)
		return 0, err
	}

	n.now += elapsed
	upgraded := 0
	for _, member := range n.members {
		entry := n.views[member]
		started, suspected := n.suspectAt[member]
		if entry.Status != Suspect || !suspected {
			continue
		}
		if n.now-started < n.timeout {
			continue
		}

		dead := Message{Member: member, Status: Dead, Incarnation: entry.Incarnation}
		n.views[member] = ViewEntry{Status: Dead, Incarnation: entry.Incarnation}
		delete(n.suspectAt, member)
		n.queueAccepted(dead, n.now)
		upgraded++
		n.logf("input=AdvanceClock now=%s current=%v output=%v decision=suspect reached timeout exactly or later", n.now, entry, n.views[member])

		if member == n.self {
			n.exited = true
			n.logf("input=AdvanceClock now=%s output=self exits reason=own suspect timed out", n.now)
			break
		}
	}
	n.logf("input=AdvanceClock(%s) output=now:%s upgraded:%d", elapsed, n.now, upgraded)
	return upgraded, nil
}
