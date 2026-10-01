package ontology

import (
	"fmt"
	"sort"
	"strings"
	"testing"
)

type simulation struct {
	t       *testing.T
	nodes   map[string]*Node
	events  []simulationEvent
	seq     int
	logs    []string
	drop    map[string]struct{}
	dup     int
	prunes  int
	outputs int
}

type simulationEvent struct {
	time    int
	seq     int
	from    string
	message Message
}

type dropRule struct {
	from string
	to   string
	id   string
	typ  MessageType
}

func newSimulation(t *testing.T, rules []dropRule) *simulation {
	t.Helper()
	ids := []string{"A", "B", "C", "D"}
	nodes := make(map[string]*Node, len(ids))
	for _, id := range ids {
		neighbors := make(map[string]struct{}, len(ids)-1)
		for _, other := range ids {
			if other != id {
				neighbors[other] = struct{}{}
			}
		}
		node, err := NewNode(id, neighbors, 2, 2)
		if err != nil {
			t.Fatalf("NewNode(%s): %v", id, err)
		}
		nodes[id] = node
	}

	dropped := make(map[string]struct{}, len(rules))
	for _, rule := range rules {
		dropped[dropKey(rule.from, rule.to, rule.id, rule.typ)] = struct{}{}
	}
	return &simulation{t: t, nodes: nodes, drop: dropped}
}

func dropKey(from, to, id string, typ MessageType) string {
	return strings.Join([]string{from, to, id, string(typ)}, "|")
}

func (s *simulation) broadcast(now int, from, id, payload string) {
	messages, err := s.nodes[from].Broadcast(now, id, payload)
	if err != nil {
		s.t.Fatalf("Broadcast(%s, %s): %v", from, id, err)
	}
	s.logf("t=%d IN BROADCAST node=%s id=%s payload=%q basis=local-delivery", now, from, id, payload)
	s.scheduleAll(now, from, messages)
}

func (s *simulation) run(until int) {
	lastTime := 0
	for {
		if len(s.events) == 0 {
			if lastTime < until {
				lastTime++
				s.tickAll(lastTime)
				continue
			}
			return
		}
		sort.Slice(s.events, func(i, j int) bool {
			if s.events[i].time != s.events[j].time {
				return s.events[i].time < s.events[j].time
			}
			return s.events[i].seq < s.events[j].seq
		})
		current := s.events[0].time
		if current > until {
			return
		}
		for len(s.events) > 0 && s.events[0].time == current {
			event := s.events[0]
			s.events = s.events[1:]
			s.deliver(event)
		}
		s.tickAll(current)
		lastTime = current
	}
}

func (s *simulation) deliver(event simulationEvent) {
	msg := event.message
	key := dropKey(event.from, msg.Target, msg.ID, msg.Type)
	if _, dropped := s.drop[key]; dropped {
		s.logf("t=%d DROP %s %s->%s id=%s basis=injected-loss", event.time, msg.Type, event.from, msg.Target, msg.ID)
		return
	}

	recipient := s.nodes[msg.Target]
	var outputs []Message
	var err error
	basis := ""

	switch msg.Type {
	case Gossip:
		_, seenBefore := recipient.messages[msg.ID]
		if seenBefore {
			s.dup++
			basis = "duplicate-gossip:move-source-lazy-and-prune"
		} else {
			basis = "new-gossip:deliver-once-forward-eager-notify-lazy"
		}
		s.logf("t=%d IN GOSSIP %s->%s id=%s payload=%q basis=%s", event.time, event.from, msg.Target, msg.ID, msg.Payload, basis)
		outputs, err = recipient.OnGossip(event.time, event.from, msg.ID, msg.Payload)
	case IHave:
		_, seenBefore := recipient.messages[msg.ID]
		if seenBefore {
			basis = "seen-id:ignore"
		} else {
			basis = "unseen-id:append-source-and-arm-T1-once"
		}
		s.logf("t=%d IN IHAVE %s->%s id=%s basis=%s", event.time, event.from, msg.Target, msg.ID, basis)
		outputs, err = recipient.OnIHave(event.time, event.from, msg.ID)
	case Prune:
		basis = "move-source-to-lazy"
		s.logf("t=%d IN PRUNE %s->%s basis=%s", event.time, event.from, msg.Target, basis)
		outputs, err = recipient.OnPrune(event.time, event.from)
	case Graft:
		if _, seen := recipient.messages[msg.ID]; seen {
			basis = "move-source-to-eager:reply-gossip"
		} else {
			basis = "move-source-to-eager:no-payload-yet"
		}
		s.logf("t=%d IN GRAFT %s->%s id=%s basis=%s", event.time, event.from, msg.Target, msg.ID, basis)
		outputs, err = recipient.OnGraft(event.time, event.from, msg.ID)
	}
	if err != nil {
		s.t.Fatalf("deliver %s %s->%s: %v", msg.Type, event.from, msg.Target, err)
	}
	s.scheduleAll(event.time, msg.Target, outputs)
}

func (s *simulation) tickAll(now int) {
	nodeIDs := make([]string, 0, len(s.nodes))
	for id := range s.nodes {
		nodeIDs = append(nodeIDs, id)
	}
	sort.Strings(nodeIDs)
	for _, id := range nodeIDs {
		messages, err := s.nodes[id].Tick(now)
		if err != nil {
			s.t.Fatalf("Tick(%s): %v", id, err)
		}
		if len(messages) > 0 {
			s.logf("t=%d IN TICK node=%s basis=deadline-reached:pop-oldest-advertisement", now, id)
			s.scheduleAll(now, id, messages)
		}
	}
}

func (s *simulation) scheduleAll(now int, from string, messages []Message) {
	for _, message := range messages {
		if message.Type == Prune {
			s.prunes++
		}
		s.outputs++
		s.logf("t=%d OUT %s %s->%s id=%s payload=%q basis=node-return-order", now, message.Type, from, message.Target, message.ID, message.Payload)
		s.seq++
		s.events = append(s.events, simulationEvent{
			time:    now + 1,
			seq:     s.seq,
			from:    from,
			message: message,
		})
	}
}

func (s *simulation) logf(format string, args ...any) {
	s.logs = append(s.logs, fmt.Sprintf(format, args...))
}

func runDeterministicLossScenario(t *testing.T) string {
	rules := []dropRule{
		{from: "A", to: "B", id: "m1", typ: Gossip},
		{from: "C", to: "B", id: "m1", typ: Gossip},
	}
	s := newSimulation(t, rules)
	s.broadcast(1, "A", "m0", "zero")
	s.run(10)
	s.broadcast(10, "A", "m1", "one")
	s.run(20)

	for _, id := range []string{"A", "B", "C", "D"} {
		node := s.nodes[id]
		if len(node.messages) != 2 {
			t.Fatalf("node %s delivered %d messages, want 2: %#v", id, len(node.messages), node.messages)
		}
		for _, messageID := range []string{"m0", "m1"} {
			if _, ok := node.messages[messageID]; !ok {
				t.Fatalf("node %s did not deliver %s", id, messageID)
			}
		}
	}
	if s.dup != s.prunes {
		t.Fatalf("duplicate GOSSIP count = %d, PRUNE count = %d", s.dup, s.prunes)
	}
	if s.dup != 8 || s.prunes != 8 {
		t.Fatalf("duplicate GOSSIP/PRUNE = %d/%d, want 8/8", s.dup, s.prunes)
	}

	verdict := fmt.Sprintf("VERDICT all=4-nodes-deliver-m0-and-m1 duplicate-gossip=%d prune=%d injected-losses=%d outputs=%d", s.dup, s.prunes, len(rules), s.outputs)
	s.logf("%s", verdict)
	return strings.Join(s.logs, "\n")
}

func TestDeterministicLossyBroadcastReplay(t *testing.T) {
	first := runDeterministicLossScenario(t)
	second := runDeterministicLossScenario(t)
	if first != second {
		t.Fatalf("replay produced different logs")
	}
	t.Logf("deterministic simulation log:\n%s", first)
}
