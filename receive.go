package ontology

import "time"

func (n *Node) Receive(messages ...Message) (Decision, error) {
	n.mu.Lock()
	defer n.mu.Unlock()

	if n.exited {
		err := reject("Receive", RejectNodeExited, n.self)
		n.logf("input=%v reject reason=%s", messages, err)
		return Decision{}, err
	}
	for _, message := range messages {
		if message.Incarnation < 0 {
			err := reject("Receive", RejectNegativeIncarnation, message.Member)
			n.logf("input=%v reject reason=%s", messages, err)
			return Decision{}, err
		}
		if !n.known[message.Member] {
			err := reject("Receive", RejectUnknownMember, message.Member)
			n.logf("input=%v reject reason=%s", messages, err)
			return Decision{}, err
		}
		if !validStatus(message.Status) {
			err := reject("Receive", RejectInvalidParameter, "invalid status")
			n.logf("input=%v reject reason=%s", messages, err)
			return Decision{}, err
		}
	}
	if len(messages) == 0 {
		n.logf("input=[] output=no-op reason=empty batch")
		return Decision{Reason: "empty batch", PiggybackCap: n.piggybackCap()}, nil
	}

	decision := Decision{
		Results:      make([]MessageDecision, 0, len(messages)),
		PiggybackCap: n.piggybackCap(),
	}
	selfDeadIncarnation := -1
	for _, message := range messages {
		result := MessageDecision{Message: message}
		current := n.views[message.Member]

		if message.Member == n.self && message.Status == Dead {
			if message.Incarnation > selfDeadIncarnation {
				selfDeadIncarnation = message.Incarnation
			}
			result.Accepted = true
			if current.Status != Dead || message.Incarnation > current.Incarnation {
				result.Replaced = true
			}
			result.Reason = "confirmed failure for self; node exits after batch validation and merge"
			decision.Accepted = true
			decision.AcceptedCount++
			if result.Replaced {
				decision.ReplacedCount++
			}
			decision.NodeExited = true
			decision.Results = append(decision.Results, result)
			decision.Reason = result.Reason
			n.logf("input=%v current=%v decision=%s", message, current, result.Reason)
			continue
		}

		incoming := ViewEntry{Status: message.Status, Incarnation: message.Incarnation}
		if message.Member == n.self && message.Status == Suspect && message.Incarnation >= current.Incarnation {
			healed := ViewEntry{Status: Alive, Incarnation: message.Incarnation + 1}
			n.views[n.self] = healed
			delete(n.suspectAt, n.self)
			n.queueAccepted(Message{Member: n.self, Status: Alive, Incarnation: healed.Incarnation}, n.now)
			result.Accepted = true
			result.Replaced = true
			result.Reason = "self received suspect at current or later incarnation; issued alive with incarnation incremented"
			decision.Accepted = true
			decision.AcceptedCount++
			decision.ReplacedCount++
			decision.SelfHealed = true
			decision.Results = append(decision.Results, result)
			decision.Reason = result.Reason
			n.logf("input=%v current=%v output=%v decision=%s", message, current, healed, result.Reason)
			continue
		}

		replaces, reason := shouldReplace(current, incoming)
		result.Reason = reason
		if replaces {
			n.views[message.Member] = incoming
			if incoming.Status == Suspect {
				n.suspectAt[message.Member] = n.now
			} else {
				delete(n.suspectAt, message.Member)
			}
			n.queueAccepted(message, n.now)
			result.Accepted = true
			result.Replaced = true
			decision.Accepted = true
			decision.AcceptedCount++
			decision.ReplacedCount++
			decision.Reason = reason
		} else {
			result.Discarded = true
			decision.DiscardedCount++
		}
		decision.Results = append(decision.Results, result)
		n.logf("input=%v current=%v output=%v accepted=%t decision=%s", message, current, n.views[message.Member], replaces, reason)
	}

	if selfDeadIncarnation >= 0 {
		dead := Message{Member: n.self, Status: Dead, Incarnation: selfDeadIncarnation}
		n.views[n.self] = ViewEntry{Status: Dead, Incarnation: selfDeadIncarnation}
		delete(n.suspectAt, n.self)
		n.queueAccepted(dead, n.now)
		n.exited = true
		n.logf("input=self-dead-batch output=%v decision=node exits after processing full batch", dead)
	}

	return decision, nil
}

func (n *Node) queueAccepted(update Message, at time.Duration) {
	existing := n.pending[update.Member]
	if existing != nil && existing.update == update {
		return
	}
	n.pending[update.Member] = &queuedUpdate{update: update, firstSeen: at}
}
