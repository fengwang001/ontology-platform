package plumtree

import (
	"fmt"
	"reflect"
	"sort"
	"testing"
)

type simEvent struct {
	when int64
	from string
	msg  Message
}

type droppedLink struct {
	id      string
	from    string
	target  string
	msgType MessageType
}

type simulator struct {
	t            *testing.T
	nodes        map[string]*Node
	queue        []simEvent
	delivered    map[string]map[string]string
	dropped      map[droppedLink]bool
	oneShotDrops map[droppedLink]bool

	gossipInputs int
	pruneOutputs int
}

func TestLossyDeterministicSimulation(t *testing.T) {
	ids := []string{"n0", "n1", "n2", "n3", "n4"}
	sim := newSimulator(t, ids, 3, 2)

	sim.dropOnce("m1", "n0", "n1", Gossip)
	sim.drop("m2", "n0", "n2", Gossip)
	sim.drop("m2", "n0", "n3", Gossip)
	sim.drop("m2", "n0", "n4", Gossip)

	sim.broadcast(1, "n0", "m1", "first")
	sim.run(1, 20)
	sim.assertAllDelivered("m1", "first")

	sim.broadcast(21, "n0", "m2", "second")
	sim.run(21, 50)
	sim.assertAllDelivered("m2", "second")

	if sim.gossipInputs == 0 {
		t.Fatal("simulation did not observe a duplicate GOSSIP")
	}
	if sim.pruneOutputs != sim.gossipInputs {
		t.Fatalf("duplicate GOSSIP inputs = %d, PRUNE outputs = %d, want equal", sim.gossipInputs, sim.pruneOutputs)
	}
}

func newSimulator(t *testing.T, ids []string, t1, t2 int64) *simulator {
	t.Helper()
	sim := &simulator{
		t:            t,
		nodes:        make(map[string]*Node, len(ids)),
		delivered:    make(map[string]map[string]string, len(ids)),
		dropped:      map[droppedLink]bool{},
		oneShotDrops: map[droppedLink]bool{},
	}

	for _, id := range ids {
		neighbors := make([]string, 0, len(ids)-1)
		for _, other := range ids {
			if other != id {
				neighbors = append(neighbors, other)
			}
		}
		node, err := NewNode(id, neighbors, t1, t2)
		if err != nil {
			t.Fatal(err)
		}
		sim.nodes[id] = node
		sim.delivered[id] = map[string]string{}
	}
	return sim
}

func (s *simulator) drop(id, from, target string, msgType MessageType) {
	s.dropped[droppedLink{id: id, from: from, target: target, msgType: msgType}] = true
}

func (s *simulator) dropOnce(id, from, target string, msgType MessageType) {
	s.oneShotDrops[droppedLink{id: id, from: from, target: target, msgType: msgType}] = true
}

func (s *simulator) broadcast(when int64, source, id, payload string) {
	s.t.Helper()
	s.t.Logf("输入 Broadcast now=%d source=%s id=%s payload=%q；判定依据：本地源节点仅交付一次并生成初始发送列表", when, source, id, payload)
	out, err := s.nodes[source].Broadcast(when, id, payload)
	if err != nil {
		s.t.Fatal(err)
	}
	s.recordDelivery(source, id, payload)
	s.emit(when, source, out)
}

func (s *simulator) run(start, until int64) {
	s.processEvents(start - 1)
	for now := start; now <= until; now++ {
		s.processEvents(now)
		s.tick(now)
		s.processEvents(now)
	}
	if len(s.queue) != 0 {
		s.t.Fatalf("undelivered events remain: %#v", s.queue)
	}
}

func (s *simulator) processEvents(now int64) {
	for len(s.queue) > 0 && s.queue[0].when <= now {
		event := s.queue[0]
		s.queue = s.queue[1:]

		if reason, dropped := s.dropReason(event); dropped {
			s.t.Logf("输入 %s->%s 类型=%s id=%s now=%d 被丢弃；判定依据：%s", event.from, event.msg.Target, event.msg.Type, event.msg.ID, event.when, reason)
			continue
		}

		s.deliver(event)
	}
}

func (s *simulator) tick(now int64) {
	ids := make([]string, 0, len(s.nodes))
	for id := range s.nodes {
		ids = append(ids, id)
	}
	sort.Strings(ids)

	for _, id := range ids {
		out, err := s.nodes[id].Tick(now)
		if err != nil {
			s.t.Fatal(err)
		}
		if len(out) > 0 {
			s.t.Logf("输入 Tick now=%d node=%s；输出 %s；判定依据：到期 id 按升序处理，每个 id 本轮至多嫁接一个通告者", now, id, formatMessages(out))
			s.emit(now, id, out)
		} else {
			s.t.Logf("输入 Tick now=%d node=%s；输出空；判定依据：没有截止时刻不大于当前时钟的通告", now, id)
		}
	}
}

func (s *simulator) deliver(event simEvent) {
	node := s.nodes[event.msg.Target]
	var out []Message
	var err error

	switch event.msg.Type {
	case Gossip:
		_, seen := s.delivered[event.msg.Target][event.msg.ID]
		out, err = node.OnGossip(event.when, event.from, event.msg.ID, event.msg.Payload)
		if err == nil {
			if seen {
				s.gossipInputs++
				s.t.Logf("输入 GOSSIP now=%d from=%s to=%s id=%s；判定依据：目标已见 id，必须迁移来源为 lazy 且只输出一条 PRUNE", event.when, event.from, event.msg.Target, event.msg.ID)
			} else {
				s.recordDelivery(event.msg.Target, event.msg.ID, event.msg.Payload)
				s.t.Logf("输入 GOSSIP now=%d from=%s to=%s id=%s；判定依据：目标首次见 id，记录交付、清除通告状态并把来源放入 eager", event.when, event.from, event.msg.Target, event.msg.ID)
			}
		}
	case IHave:
		out, err = node.OnIHave(event.when, event.from, event.msg.ID)
		s.t.Logf("输入 IHAVE now=%d from=%s to=%s id=%s；判定依据：未见则按到达顺序追加通告者，已见或重复对端则不重复入列", event.when, event.from, event.msg.Target, event.msg.ID)
	case Prune:
		out, err = node.OnPrune(event.when, event.from)
		s.t.Logf("输入 PRUNE now=%d from=%s to=%s；判定依据：把来源移入 lazy", event.when, event.from, event.msg.Target)
	case Graft:
		out, err = node.OnGraft(event.when, event.from, event.msg.ID)
		s.t.Logf("输入 GRAFT now=%d from=%s to=%s id=%s；判定依据：把来源移入 eager，已见 id 时回传 GOSSIP", event.when, event.from, event.msg.Target, event.msg.ID)
	}
	if err != nil {
		s.t.Fatal(err)
	}

	for _, msg := range out {
		if msg.Type == Prune {
			s.pruneOutputs++
		}
	}
	s.t.Logf("输出 %s->%s now=%d messages=[%s]；判定依据：eager 先于 lazy，同组成员按标识升序", event.msg.Target, "neighbors", event.when, formatMessages(out))
	s.emit(event.when, event.msg.Target, out)
}

func (s *simulator) emit(when int64, from string, messages []Message) {
	for _, msg := range messages {
		s.queue = append(s.queue, simEvent{when: when, from: from, msg: msg})
	}
}

func (s *simulator) dropReason(event simEvent) (string, bool) {
	link := droppedLink{
		id:      event.msg.ID,
		from:    event.from,
		target:  event.msg.Target,
		msgType: event.msg.Type,
	}
	if s.oneShotDrops[link] {
		delete(s.oneShotDrops, link)
		return "匹配一次性注入丢包规则，后续重传允许送达", true
	}
	if s.dropped[link] {
		return "匹配持续注入丢包规则", true
	}
	return "", false
}

func (s *simulator) recordDelivery(target, id, payload string) {
	if _, exists := s.delivered[target][id]; exists {
		s.t.Fatalf("message %s delivered twice to %s", id, target)
	}
	s.delivered[target][id] = payload
}

func (s *simulator) assertAllDelivered(id, payload string) {
	s.t.Helper()
	nodeIDs := make([]string, 0, len(s.nodes))
	for nodeID := range s.nodes {
		nodeIDs = append(nodeIDs, nodeID)
	}
	sort.Strings(nodeIDs)

	for _, nodeID := range nodeIDs {
		got, ok := s.delivered[nodeID][id]
		if !ok {
			s.t.Fatalf("node %s did not receive %s", nodeID, id)
		}
		if got != payload {
			s.t.Fatalf("node %s payload = %q, want %q", nodeID, got, payload)
		}
	}

	got := map[string]string{}
	for _, nodeID := range nodeIDs {
		got[nodeID] = s.delivered[nodeID][id]
	}
	want := map[string]string{}
	for _, nodeID := range nodeIDs {
		want[nodeID] = payload
	}
	s.t.Logf("判定 id=%s 全员交付，交付表=%s；判定依据：5 个节点均恰有一条非空载荷记录", id, formatStringMap(got))
	if !reflect.DeepEqual(got, want) {
		s.t.Fatalf("delivery map = %#v, want %#v", got, want)
	}
}

func formatMessages(messages []Message) string {
	if len(messages) == 0 {
		return ""
	}
	values := make([]string, len(messages))
	for i, msg := range messages {
		if msg.Type == Gossip {
			values[i] = fmt.Sprintf("to=%s %s id=%s payload=%q", msg.Target, msg.Type, msg.ID, msg.Payload)
		} else {
			values[i] = fmt.Sprintf("to=%s %s id=%s", msg.Target, msg.Type, msg.ID)
		}
	}
	result := values[0]
	for _, value := range values[1:] {
		result += ", " + value
	}
	return result
}

func formatStringMap(values map[string]string) string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	result := "{"
	for i, key := range keys {
		if i > 0 {
			result += ", "
		}
		result += key + ":" + values[key]
	}
	return result + "}"
}
