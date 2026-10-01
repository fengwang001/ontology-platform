package delta

import (
	"fmt"
	"math/rand"
	"reflect"
	"sync"
	"testing"
)

// naiveReplica is the reference implementation: it keeps the same max-map
// but synchronizes by merging full states.
type naiveReplica struct {
	state map[string]uint64
}

func newNaive() *naiveReplica { return &naiveReplica{state: make(map[string]uint64)} }

func (n *naiveReplica) apply(key string, v uint64) {
	if v > n.state[key] {
		n.state[key] = v
	}
}

func (n *naiveReplica) mergeFull(other map[string]uint64) {
	for k, v := range other {
		if v > n.state[k] {
			n.state[k] = v
		}
	}
}

type message struct {
	from, to string
	a, b     uint64
	group    map[string]uint64
}

func minAckOf(t *testing.T, r *Replica, peers []string) uint64 {
	t.Helper()
	min := ^uint64(0)
	for _, p := range peers {
		v, ok := r.AckOf(p)
		if !ok {
			t.Fatalf("missing ack entry for peer %s", p)
		}
		if v < min {
			min = v
		}
	}
	return min
}

func checkInvariant(t *testing.T, r *Replica, peers []string) {
	t.Helper()
	want := r.Counter() - minAckOf(t, r, peers)
	if got := r.Buffered(); got != int(want) {
		t.Fatalf("buffer invariant violated: |D|=%d, want c-minAck=%d", got, want)
	}
}

// deliver feeds one message into its receiver and routes the returned
// seen cursor back to the sender as an ack.
func deliver(t *testing.T, replicas map[string]*Replica, m message, tag string) {
	t.Helper()
	seen, err := replicas[m.to].Receive(m.from, m.a, m.b, m.group)
	if err != nil {
		t.Fatalf("%s: Receive(%s,%d,%d) on %s failed: %v", tag, m.from, m.a, m.b, m.to, err)
	}
	if err := replicas[m.from].Ack(m.to, seen); err != nil {
		t.Fatalf("%s: Ack(%s,%d) on %s failed: %v", tag, m.to, seen, m.from, err)
	}
	t.Logf("%s 输入 Receive(%s,[%d,%d),%v)@%s -> 输出 seen=%d; Ack(%s,%d)@%s (判定依据: a<=seen<b 合并, b<=seen 陈旧)",
		tag, m.from, m.a, m.b, m.group, m.to, seen, m.to, seen, m.from)
}

func TestThreeReplicaConvergence(t *testing.T) {
	rng := rand.New(rand.NewSource(20261001))
	ids := []string{"A", "B", "C"}
	peersOf := map[string][]string{"A": {"B", "C"}, "B": {"A", "C"}, "C": {"A", "B"}}

	replicas := map[string]*Replica{}
	naive := map[string]*naiveReplica{}
	for _, id := range ids {
		replicas[id] = mustReplica(t, id, peersOf[id], 1<<12)
		naive[id] = newNaive()
	}

	keys := []string{"k0", "k1", "k2", "k3", "k4"}
	var queue []message
	const steps = 600

	for step := 0; step < steps; step++ {
		switch rng.Intn(3) {
		case 0: // local apply on a random replica
			id := ids[rng.Intn(len(ids))]
			key := keys[rng.Intn(len(keys))]
			v := uint64(rng.Intn(50))
			ok, err := replicas[id].Apply(key, v)
			if err != nil {
				t.Fatalf("step %d: Apply failed: %v", step, err)
			}
			naive[id].apply(key, v)
			t.Logf("step %d 输入 Apply(%s,%d)@%s -> 输出 inflated=%v (判定依据: v>S[key] 才膨胀占序号)", step, key, v, id, ok)
		case 1: // send DeltaTo with random loss and duplication
			from := ids[rng.Intn(len(ids))]
			to := peersOf[from][rng.Intn(2)]
			a, b, g, err := replicas[from].DeltaTo(to)
			if err != nil {
				t.Fatalf("step %d: DeltaTo failed: %v", step, err)
			}
			if a == b {
				continue
			}
			if rng.Intn(10) < 2 {
				t.Logf("step %d 输入 DeltaTo(%s)@%s -> 输出 [%d,%d) 丢包 (判定依据: 丢失后由后续 DeltaTo 重发)", step, to, from, a, b)
				continue
			}
			copies := 1
			if rng.Intn(10) < 3 {
				copies = 2
			}
			for i := 0; i < copies; i++ {
				queue = append(queue, message{from: from, to: to, a: a, b: b, group: g})
			}
			t.Logf("step %d 输入 DeltaTo(%s)@%s -> 输出 [%d,%d)%v x%d 入队 (判定依据: 区间 [ack,c) 可重复投递)", step, to, from, a, b, g, copies)
		case 2: // deliver a random queued message (arbitrary reorder)
			if len(queue) == 0 {
				continue
			}
			i := rng.Intn(len(queue))
			m := queue[i]
			queue = append(queue[:i], queue[i+1:]...)
			deliver(t, replicas, m, fmt.Sprintf("step %d", step))
		}
		// Naive reference: full-state merge of every pair at each step.
		for _, i := range ids {
			for _, j := range ids {
				if i != j {
					naive[i].mergeFull(naive[j].state)
				}
			}
		}
	}

	// Flush: redeliver everything still queued, then keep sending until all
	// acks reach the sender counters.
	for _, m := range queue {
		deliver(t, replicas, m, "flush-queue")
	}
	queue = nil
	for round := 0; ; round++ {
		progress := false
		for _, from := range ids {
			for _, to := range peersOf[from] {
				a, b, g, err := replicas[from].DeltaTo(to)
				if err != nil {
					t.Fatalf("flush: DeltaTo failed: %v", err)
				}
				if a == b {
					continue
				}
				progress = true
				deliver(t, replicas, message{from: from, to: to, a: a, b: b, group: g}, fmt.Sprintf("flush-r%d", round))
			}
		}
		if !progress {
			break
		}
	}

	for _, id := range ids {
		checkInvariant(t, replicas[id], peersOf[id])
		if got := replicas[id].Buffered(); got != 0 {
			t.Fatalf("replica %s: buffer not fully reclaimed, |D|=%d", id, got)
		}
		want := naive[id].state
		got := replicas[id].Snapshot()
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("replica %s diverged from naive full-state merge:\n got %v\nwant %v", id, got, want)
		}
		t.Logf("判定 副本 %s 最终状态 %v 与朴素全状态合并一致, |D|==c-minAck==0", id, got)
	}
	for _, i := range ids {
		for _, j := range ids {
			if !reflect.DeepEqual(replicas[i].Snapshot(), replicas[j].Snapshot()) {
				t.Fatalf("replicas %s and %s diverged: %v vs %v", i, j, replicas[i].Snapshot(), replicas[j].Snapshot())
			}
		}
	}
	t.Logf("判定 三副本经丢包/重复/乱序后逐键相同, 收敛达成")
}

// TestConcurrentLinearizable hammers one replica from many goroutines and
// checks the buffer invariant and convergence afterwards. Run with -race.
func TestConcurrentLinearizable(t *testing.T) {
	a := mustReplica(t, "A", []string{"B"}, 1<<10)
	b := mustReplica(t, "B", []string{"A"}, 1<<10)

	var wg sync.WaitGroup
	for w := 0; w < 8; w++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			for i := 0; i < 200; i++ {
				key := fmt.Sprintf("k%d", (w+i)%7)
				if _, err := a.Apply(key, uint64(w*1000+i)); err != nil {
					t.Errorf("apply: %v", err)
					return
				}
				fa, fb, g, err := a.DeltaTo("B")
				if err != nil {
					t.Errorf("deltaTo: %v", err)
					return
				}
				if fa == fb {
					continue
				}
				seen, err := b.Receive("A", fa, fb, g)
				if err != nil {
					continue // causal gap under concurrent interleaving is expected
				}
				if err := a.Ack("B", seen); err != nil {
					t.Errorf("ack: %v", err)
					return
				}
			}
		}(w)
	}
	wg.Wait()

	// Drain whatever is left.
	for {
		fa, fb, g, err := a.DeltaTo("B")
		if err != nil {
			t.Fatal(err)
		}
		if fa == fb {
			break
		}
		seen, err := b.Receive("A", fa, fb, g)
		if err != nil {
			t.Fatal(err)
		}
		if err := a.Ack("B", seen); err != nil {
			t.Fatal(err)
		}
	}

	checkInvariant(t, a, []string{"B"})
	if got := a.Buffered(); got != 0 {
		t.Fatalf("buffer not reclaimed: |D|=%d", got)
	}
	if !reflect.DeepEqual(a.Snapshot(), b.Snapshot()) {
		t.Fatalf("concurrent run diverged: %v vs %v", a.Snapshot(), b.Snapshot())
	}
	t.Logf("判定 并发交错等价于某串行顺序: 两副本状态一致 %v, |D|==c-minAck==0", a.Snapshot())
}
