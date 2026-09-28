package ontology

import (
	"sort"
	"strconv"
)

// Relation 是两个事件之间的关系判定结果。
type Relation int

const (
	// Concurrent 表示两事件无因果关系（向量时钟不可比）。
	Concurrent Relation = iota
	// Before 表示 a 因果先于 b（a→b）。
	Before
	// After 表示 a 因果后于 b（b→a）。
	After
	// Equal 表示引用同一个事件。
	Equal
)

// String 返回关系的可读名称。
func (r Relation) String() string {
	switch r {
	case Before:
		return "before"
	case After:
		return "after"
	case Equal:
		return "equal"
	case Concurrent:
		return "concurrent"
	default:
		return "unknown"
	}
}

// TotalOrder 返回全部事件按 (时间戳升序, 节点编号升序) 的全序排列副本。
// 结果只取决于各事件的 (Clock, Node)，与事件被接受的先后无关。
// 同一节点上的事件时间戳严格递增，因此不同事件的 (Clock, Node) 不会完全相同。
func (s *System) TotalOrder() []Event {
	s.mu.RLock()
	events := cloneEvents(s.events)
	s.mu.RUnlock()

	sort.SliceStable(events, func(i, j int) bool {
		if events[i].Clock != events[j].Clock {
			return events[i].Clock < events[j].Clock
		}
		return events[i].Node < events[j].Node
	})
	return events
}

// Compare 判定两个事件之间的因果关系（happens-before）。
//
// 判定依据是每个事件携带的向量时钟，它在接收时逐分量合并发送方时钟，
// 因而恰好编码了三类因果边的传递闭包：
//   - 同一节点上序号小的事件先于序号大的事件（线程内先后）；
//   - 消息的发送事件先于其接收事件（消息边）；
//   - 上述关系的传递闭包。
//
// 判定规则：Va 各分量均 <= Vb 且不全相等 ⇔ a 先于 b；不可比则并发。
// 引用不存在的事件返回 ErrUnknownEvent。
func (s *System) Compare(a, b EventRef) (Relation, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	ea, ok := s.lookupLocked(a)
	if !ok {
		return Concurrent, &ClockError{Kind: ErrUnknownEvent, Op: "compare", Node: a.Node,
			Detail: "first event with seq " + strconv.Itoa(a.Seq) + " not found"}
	}
	eb, ok := s.lookupLocked(b)
	if !ok {
		return Concurrent, &ClockError{Kind: ErrUnknownEvent, Op: "compare", Node: b.Node,
			Detail: "second event with seq " + strconv.Itoa(b.Seq) + " not found"}
	}
	return compareVectors(ea, eb), nil
}

// HappensBefore 判定 a 是否因果先于 b。引用不存在的事件返回错误。
func (s *System) HappensBefore(a, b EventRef) (bool, error) {
	r, err := s.Compare(a, b)
	if err != nil {
		return false, err
	}
	return r == Before, nil
}

// AreConcurrent 判定两个事件是否并发（互无因果关系，且不是同一事件）。
func (s *System) AreConcurrent(a, b EventRef) (bool, error) {
	r, err := s.Compare(a, b)
	if err != nil {
		return false, err
	}
	return r == Concurrent, nil
}

// IsCausalConsistent 校验当前系统状态。它检查：
//  1. 同一节点上事件的 Lamport 时间戳严格递增；
//  2. 每条已接收消息的接收时间戳严格大于发送时间戳，且等于
//     max(接收前本地时钟, 发送时间戳)+1；
//  3. 全序是因果偏序的一个一致扩展：凡 a→b，全序中 a 必在 b 之前。
//
// 一致时返回 ("", true)，否则返回首个不一致的描述与 false。
func (s *System) IsCausalConsistent() (string, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	// 检查 1：同节点时间戳严格递增。
	for n, es := range s.nodeEvents {
		for i := 1; i < len(es); i++ {
			if es[i].Clock <= es[i-1].Clock {
				return "clock not strictly increasing on node " + strconv.Itoa(n) +
					" between seq " + strconv.Itoa(es[i-1].Seq) + " and " +
					strconv.Itoa(es[i].Seq), false
			}
		}
	}

	// 检查 2：接收时间戳规则 recv = max(prevLocal, sendClock) + 1。
	for id, msg := range s.messages {
		if !msg.received {
			continue
		}
		recv := s.nodeEvents[msg.receiver][msg.recvSeq-1]
		prevLocal := 0
		if msg.recvSeq >= 2 {
			prevLocal = s.nodeEvents[msg.receiver][msg.recvSeq-2].Clock
		}
		want := msg.sendClock
		if prevLocal > want {
			want = prevLocal
		}
		want++
		if recv.Clock != want {
			return "receive timestamp mismatch for message " + id +
				": got " + strconv.Itoa(recv.Clock) + ", want max(local=" +
				strconv.Itoa(prevLocal) + ",send=" + strconv.Itoa(msg.sendClock) +
				")+1=" + strconv.Itoa(want), false
		}
	}

	// 检查 3：全序与因果偏序一致。
	ordered := cloneEvents(s.events)
	sort.SliceStable(ordered, func(i, j int) bool {
		if ordered[i].Clock != ordered[j].Clock {
			return ordered[i].Clock < ordered[j].Clock
		}
		return ordered[i].Node < ordered[j].Node
	})
	pos := make(map[EventRef]int, len(ordered))
	for i, e := range ordered {
		pos[EventRef{Node: e.Node, Seq: e.Seq}] = i
	}
	ra := EventRef{}
	rb := EventRef{}
	for _, ea := range ordered {
		for _, eb := range ordered {
			if compareVectors(ea, eb) == Before {
				ra.Node, ra.Seq = ea.Node, ea.Seq
				rb.Node, rb.Seq = eb.Node, eb.Seq
				if pos[ra] >= pos[rb] {
					return "total order violates happens-before: (" +
						strconv.Itoa(ea.Node) + "," + strconv.Itoa(ea.Seq) +
						") before (" + strconv.Itoa(eb.Node) + "," +
						strconv.Itoa(eb.Seq) + ")", false
				}
			}
		}
	}
	return "", true
}

// compareVectors 直接基于两个事件的向量时钟判定关系，不再加锁。
func compareVectors(ea, eb Event) Relation {
	if ea.Node == eb.Node && ea.Seq == eb.Seq {
		return Equal
	}
	aLEb, aGEb := true, true
	for i := range ea.Vector {
		if ea.Vector[i] > eb.Vector[i] {
			aLEb = false
		}
		if ea.Vector[i] < eb.Vector[i] {
			aGEb = false
		}
	}
	switch {
	case aLEb && !aGEb:
		return Before
	case aGEb && !aLEb:
		return After
	default:
		// 两个不同事件向量完全相等在本实现中不可能出现（对角分量区分节点序列），
		// 落到此分支按并发处理仍是安全的。
		return Concurrent
	}
}
