package ontology

import (
	"fmt"
	"math/rand"
	"testing"
)

// simEvent is one datagram (delta message or acknowledgement) in the
// lossy, reordering network between two replicas.
type simEvent struct {
	kind     string // "delta" or "ack"
	from, to string
	a, b     int
	group    DeltaGroup
	n        int // ack sequence when kind == "ack"
}

type naiveReplica struct {
	id    string
	state State
}

func mergeState(dst, src State) {
	for k, v := range src {
		if v > dst[k] {
			dst[k] = v
		}
	}
}

func cloneState(s State) State {
	out := make(State, len(s))
	for k, v := range s {
		out[k] = v
	}
	return out
}

var topologyIDs = []string{"r0", "r1", "r2"}

func topologyOthers() map[string][]string {
	return map[string][]string{
		"r0": {"r1", "r2"},
		"r1": {"r0", "r2"},
		"r2": {"r0", "r1"},
	}
}

// chaosNetwork drives three delta replicas over a lossy / duplicating /
// reordering network, alongside three naive replicas that perform a full
// pointwise-max state merge per delivery. Both flavours consume the same
// sequence of accepted local writes.
type chaosNetwork struct {
	t        *testing.T
	seed     int64
	rng      *rand.Rand
	others   map[string][]string
	replicas map[string]*Replica
	naive    map[string]*naiveReplica
	queue    []simEvent
	step     int
}

func newChaosNetwork(t *testing.T, seed int64) *chaosNetwork {
	t.Helper()
	others := topologyOthers()
	const cap = 6
	net := &chaosNetwork{
		t:        t,
		seed:     seed,
		rng:      rand.New(rand.NewSource(seed)),
		others:   others,
		replicas: map[string]*Replica{},
		naive:    map[string]*naiveReplica{},
	}
	for _, id := range topologyIDs {
		r, err := New(id, others[id], cap)
		if err != nil {
			t.Fatalf("New(%s): %v", id, err)
		}
		net.replicas[id] = r
		net.naive[id] = &naiveReplica{id: id, state: State{}}
	}
	t.Logf("===== seed=%d：三副本随机丢包/重复/乱序开始 =====", seed)
	return net
}

func (n *chaosNetwork) logf(format string, args ...any) {
	prefix := fmt.Sprintf("seed=%d step=%d ", n.seed, n.step)
	n.t.Logf(prefix+format, args...)
}

// applyLocal performs a write on both the delta replica and its naive
// control. It reports whether the delta replica accepted an inflation;
// only accepted writes are mirrored into the naive control, so a buffer-full
// rejection never desynchronises the comparison.
func (n *chaosNetwork) applyLocal(who, key string, v uint64) bool {
	r := n.replicas[who]
	inflated, err := r.Apply(key, v)
	if err != nil {
		if err == ErrBufferFull {
			n.logf("Apply: %s[%s]=%d 膨胀但缓冲已满 -> ErrBufferFull（整体拒绝，朴素侧同样不接受该写入）",
				who, key, v)
			return false
		}
		n.t.Fatalf("Apply(%s,%s,%d) unexpected error: %v", who, key, v, err)
	}
	if inflated {
		// The delta state inflated, so the naive max map must inflate too.
		if v > n.naive[who].state[key] {
			n.naive[who].state[key] = v
		}
		n.logf("Apply: %s[%s]=%d 膨胀=true（占序号 %d）", who, key, v, r.Cursor()-1)
		return true
	} else {
		n.logf("Apply: %s[%s]=%d 膨胀=false（不占序号、不发增量）", who, key, v)
	}
	return false
}

// enqueueDelta asks the source for the pending interval toward dst and
// queues an independent copy of the group.
func (n *chaosNetwork) enqueueDelta(from, to string) {
	msg, err := n.replicas[from].DeltaTo(to)
	if err != nil {
		n.t.Fatalf("DeltaTo: %v", err)
	}
	if msg.A == msg.B {
		return
	}
	g := make(DeltaGroup, len(msg.Group))
	for k, v := range msg.Group {
		g[k] = v
	}
	n.queue = append(n.queue, simEvent{
		kind: "delta", from: from, to: to, a: msg.A, b: msg.B, group: g,
	})
}

// enqueueAcks queues one acknowledgement per known peer up to seen.
func (n *chaosNetwork) enqueueAcks() {
	for _, id := range topologyIDs {
		r := n.replicas[id]
		for _, p := range n.others[id] {
			s, err := r.SeenOf(p)
			if err != nil {
				n.t.Fatalf("SeenOf: %v", err)
			}
			if s > 0 {
				n.queue = append(n.queue, simEvent{kind: "ack", from: id, to: p, n: s})
			}
		}
	}
}

// deliver processes one datagram. Delta gaps are expected under loss and
// leave the event's effect absent; the next retransmit closes the gap.
func (n *chaosNetwork) deliver(ev simEvent) {
	dst := n.replicas[ev.to]
	switch ev.kind {
	case "delta":
		seen, err := dst.Receive(ev.from, ev.a, ev.b, ev.group)
		switch {
		case err == nil:
			n.logf("收包 %s->%s [%d,%d): 接受/陈旧，seen=%d", ev.from, ev.to, ev.a, ev.b, seen)
		case err == ErrGap:
			n.logf("收包 %s->%s [%d,%d): 缺口拒绝 seen=%d（等待重传补齐）",
				ev.from, ev.to, ev.a, ev.b, seen)
		default:
			n.t.Fatalf("unexpected Receive error: %v", err)
		}
	case "ack":
		if err := dst.Ack(ev.from, ev.n); err != nil {
			n.t.Fatalf("Ack(%s,%d): %v", ev.from, ev.n, err)
		}
		n.logf("确认 %s->%s n=%d", ev.from, ev.to, ev.n)
	}
}

// drainShuffled shuffles the queued datagrams and gives each one a random
// fate: ~20% lost, ~20% duplicated, ~10% delayed, the rest delivered once.
func (n *chaosNetwork) drainShuffled() {
	n.rng.Shuffle(len(n.queue), func(i, j int) { n.queue[i], n.queue[j] = n.queue[j], n.queue[i] })
	remaining := n.queue[:0]
	delivered := 0
	for _, ev := range n.queue {
		switch n.rng.Intn(10) {
		case 0, 1:
			n.logf("丢包 %s->%s kind=%s [%d,%d)", ev.from, ev.to, ev.kind, ev.a, ev.b)
			continue
		case 2, 3:
			n.deliver(ev)
			n.deliver(ev) // duplicate redelivery
			delivered++
		case 4:
			remaining = append(remaining, ev) // delayed, stays queued
		default:
			n.deliver(ev)
			delivered++
		}
	}
	n.queue = remaining
}

// checkInvariant verifies |D| == c - min(ack) for every replica.
func (n *chaosNetwork) checkInvariant() {
	for _, id := range topologyIDs {
		r := n.replicas[id]
		minAck := r.Cursor()
		for _, p := range n.others[id] {
			a, _ := r.AckOf(p)
			if a < minAck {
				minAck = a
			}
		}
		wantLen := r.Cursor() - minAck
		if got := r.BufferLen(); got != wantLen {
			n.t.Fatalf("%s invariant violated: |D|=%d but c-minAck=%d", id, got, wantLen)
		}
	}
}

// runChaos performs interleaved local writes, retransmits, acks and lossy
// delivery. The naive control gossips full states with the same loss rate.
func (n *chaosNetwork) runChaos(rounds int) {
	writeSeq := 0
	for ; n.step < rounds; n.step++ {
		// Local write; sometimes repeat an existing key with a smaller or
		// equal value to exercise the non-inflation path under traffic.
		if n.rng.Intn(2) == 0 {
			who := topologyIDs[n.rng.Intn(3)]
			var key string
			var v uint64
			if writeSeq > 0 && n.rng.Intn(3) == 0 {
				key = fmt.Sprintf("k%d", n.rng.Intn(writeSeq))
				v = uint64(1 + n.rng.Intn(8))
			} else {
				key = fmt.Sprintf("k%d", writeSeq)
				v = uint64(1 + n.rng.Intn(20))
				writeSeq++
			}
			n.applyLocal(who, key, v)
		}

		// Retransmit pending intervals for random directed pairs.
		for _, from := range topologyIDs {
			for _, to := range n.others[from] {
				if n.rng.Intn(2) == 0 {
					n.enqueueDelta(from, to)
				}
			}
		}

		// Acks are deliberately sparse (and lossy): with a small Cap this
		// stalls the slowest-acknowledged sender with ErrBufferFull until
		// retransmits + acks reclaim space, exercising back-pressure too.
		if n.rng.Intn(6) == 0 {
			n.enqueueAcks()
		}

		n.drainShuffled()

		// Naive control: a random replica merges its full state into peers.
		if n.rng.Intn(2) == 0 {
			from := topologyIDs[n.rng.Intn(3)]
			full := cloneState(n.naive[from].state)
			for _, to := range n.others[from] {
				if n.rng.Intn(10) < 2 {
					continue
				}
				mergeState(n.naive[to].state, full)
			}
		}

		n.checkInvariant()
	}
}

// flush reliably retransmits pending intervals (arbitrary order, with
// duplicates and overlaps) and drives acknowledgements until every replica's
// seen of each peer reaches that peer's cursor. The naive control performs
// reliable full-state anti-entropy in the same rounds.
func (n *chaosNetwork) flush() {
	for guard := 0; guard < 10000; guard++ {
		for _, from := range topologyIDs {
			for _, to := range n.others[from] {
				n.enqueueDelta(from, to)
			}
		}
		n.enqueueAcks()

		n.rng.Shuffle(len(n.queue), func(i, j int) { n.queue[i], n.queue[j] = n.queue[j], n.queue[i] })
		remaining := n.queue[:0]
		progressed := false
		for _, ev := range n.queue {
			switch ev.kind {
			case "delta":
				before, _ := n.replicas[ev.to].SeenOf(ev.from)
				seen, err := n.replicas[ev.to].Receive(ev.from, ev.a, ev.b, ev.group)
				if err == ErrGap {
					remaining = append(remaining, ev) // still missing prefix
					continue
				}
				if err != nil {
					n.t.Fatalf("flush receive: %v", err)
				}
				if seen > before {
					progressed = true
				}
			case "ack":
				before, _ := n.replicas[ev.to].AckOf(ev.from)
				if err := n.replicas[ev.to].Ack(ev.from, ev.n); err != nil {
					n.t.Fatalf("flush ack: %v", err)
				}
				after, _ := n.replicas[ev.to].AckOf(ev.from)
				if after > before {
					progressed = true
				}
			}
		}
		n.queue = remaining

		// Naive reliable anti-entropy over all directed pairs.
		for _, from := range topologyIDs {
			for _, to := range n.others[from] {
				mergeState(n.naive[to].state, cloneState(n.naive[from].state))
			}
		}

		n.checkInvariant()

		done := true
		for _, id := range topologyIDs {
			for _, p := range n.others[id] {
				seen, _ := n.replicas[id].SeenOf(p)
				if seen != n.replicas[p].Cursor() {
					done = false
				}
			}
		}
		if done && !progressed && len(n.queue) == 0 {
			return
		}
	}
	n.t.Fatalf("flush did not converge for seed=%d", n.seed)
}

// verify compares every delta replica against the other delta replicas,
// its naive full-state control and the pointwise-max ground truth.
func (n *chaosNetwork) verify() {
	want := State{}
	for _, id := range topologyIDs {
		mergeState(want, n.naive[id].state)
	}
	ref := n.replicas[topologyIDs[0]].Snapshot()
	for _, id := range topologyIDs {
		got := n.replicas[id].Snapshot()
		naiveGot := cloneState(n.naive[id].state)
		judge(n.t, fmt.Sprintf("converge/seed-%d/%s-vs-r0", n.seed, id),
			"最终 S（增量复制）", fmt.Sprint(got), fmt.Sprint(ref),
			"任意乱序/重复/丢失后重发，所有增量送达后各副本逐键相同")
		judge(n.t, fmt.Sprintf("oracle/seed-%d/%s", n.seed, id),
			"增量副本 S 对照每步全状态合并的朴素副本",
			fmt.Sprint(got), fmt.Sprint(naiveGot),
			"增量因果合并结果必须与朴素全状态合并一致")
		judge(n.t, fmt.Sprintf("groundtruth/seed-%d/%s", n.seed, id),
			"最终 S 对照所有本地写入的逐键最大上界",
			fmt.Sprint(got), fmt.Sprint(want),
			"最大值映射收敛值等于全部已接受写入的逐键最大")
		judge(n.t, fmt.Sprintf("reclaimed/seed-%d/%s", n.seed, id),
			"全部确认后的 |D|", n.replicas[id].BufferLen(), 0,
			"minAck==c 时 |D| == c-minAck == 0")
	}
	n.t.Logf("===== seed=%d 收敛并与朴素复制对照一致 =====", n.seed)
}

func TestThreeReplicaChaosConvergence(t *testing.T) {
	for _, seed := range []int64{1, 2, 7, 42, 99, 2026} {
		n := newChaosNetwork(t, seed)
		n.runChaos(40)
		n.flush()
		n.verify()
	}
}
