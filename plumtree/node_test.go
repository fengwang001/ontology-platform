package plumtree

import (
	"errors"
	"reflect"
	"sync"
	"testing"
)

func TestBroadcastOrdering(t *testing.T) {
	node, err := NewNode("a", []string{"c", "b", "d"}, 3, 2)
	if err != nil {
		t.Fatal(err)
	}

	if _, err := node.OnPrune(2, "d"); err != nil {
		t.Fatal(err)
	}
	got, err := node.Broadcast(3, "m1", "payload")
	if err != nil {
		t.Fatal(err)
	}

	want := []Message{
		{Target: "b", Type: Gossip, ID: "m1", Payload: "payload"},
		{Target: "c", Type: Gossip, ID: "m1", Payload: "payload"},
		{Target: "d", Type: IHave, ID: "m1"},
	}
	assertMessages(t, got, want)
}

func TestDuplicateGossipPrunesAndMovesSenderLazy(t *testing.T) {
	node := newTestNode(t)

	if _, err := node.Broadcast(1, "m1", "payload"); err != nil {
		t.Fatal(err)
	}
	got, err := node.OnGossip(2, "b", "m1", "payload")
	if err != nil {
		t.Fatal(err)
	}

	assertMessages(t, got, []Message{{Target: "b", Type: Prune}})
	if _, eager := node.eager["b"]; eager {
		t.Fatal("duplicate gossip sender remained eager")
	}
	if _, lazy := node.lazy["b"]; !lazy {
		t.Fatal("duplicate gossip sender did not become lazy")
	}
}

func TestFirstGossipFromLazyReturnsSenderEagerAndClearsTimer(t *testing.T) {
	node := newTestNode(t)

	if _, err := node.OnIHave(1, "b", "m1"); err != nil {
		t.Fatal(err)
	}
	if _, err := node.OnPrune(2, "b"); err != nil {
		t.Fatal(err)
	}
	got, err := node.OnGossip(4, "b", "m1", "payload")
	if err != nil {
		t.Fatal(err)
	}

	assertMessages(t, got, []Message{
		{Target: "c", Type: Gossip, ID: "m1", Payload: "payload"},
		{Target: "d", Type: Gossip, ID: "m1", Payload: "payload"},
	})
	if _, eager := node.eager["b"]; !eager {
		t.Fatal("gossip sender did not return to eager")
	}
	if _, lazy := node.lazy["b"]; lazy {
		t.Fatal("gossip sender remained lazy")
	}
	if _, hasDeadline := node.deadlines["m1"]; hasDeadline {
		t.Fatal("gossip did not clear the advisory timer")
	}
	if advisers := node.advisories["m1"]; len(advisers) != 0 {
		t.Fatalf("gossip did not clear advisory list: %#v", advisers)
	}
}

func TestTickDeadlineBoundary(t *testing.T) {
	node := newTestNode(t)

	if _, err := node.OnIHave(10, "b", "m1"); err != nil {
		t.Fatal(err)
	}

	got, err := node.Tick(12)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 0 {
		t.Fatalf("tick one tick before deadline emitted %#v", got)
	}

	got, err = node.Tick(13)
	if err != nil {
		t.Fatal(err)
	}
	assertMessages(t, got, []Message{{Target: "b", Type: Graft, ID: "m1"}})
}

func TestAdvisoryOrderAndDuplicates(t *testing.T) {
	node := newTestNode(t)

	for _, adviser := range []string{"d", "b", "d", "c", "b"} {
		if _, err := node.OnIHave(1, adviser, "m1"); err != nil {
			t.Fatal(err)
		}
	}

	got, err := node.Tick(4)
	if err != nil {
		t.Fatal(err)
	}
	assertMessages(t, got, []Message{{Target: "d", Type: Graft, ID: "m1"}})
	if deadline := node.deadlines["m1"]; deadline != 6 {
		t.Fatalf("retry deadline = %d, want 6", deadline)
	}

	got, err = node.Tick(20)
	if err != nil {
		t.Fatal(err)
	}
	assertMessages(t, got, []Message{{Target: "b", Type: Graft, ID: "m1"}})
	if deadline := node.deadlines["m1"]; deadline != 22 {
		t.Fatalf("retry deadline after one processing = %d, want 22", deadline)
	}

	got, err = node.Tick(22)
	if err != nil {
		t.Fatal(err)
	}
	assertMessages(t, got, []Message{{Target: "c", Type: Graft, ID: "m1"}})
	if _, hasDeadline := node.deadlines["m1"]; hasDeadline {
		t.Fatal("deadline remained after advisory list drained")
	}
	if _, hasAdvisers := node.advisories["m1"]; hasAdvisers {
		t.Fatal("advisory list remained after draining")
	}

	if _, err := node.OnIHave(101, "d", "m1"); err != nil {
		t.Fatal(err)
	}
	if deadline := node.deadlines["m1"]; deadline != 104 {
		t.Fatalf("new first-wait deadline = %d, want 104", deadline)
	}
}

func TestGraftResponseDependsOnKnownMessage(t *testing.T) {
	node := newTestNode(t)
	node.eager = map[string]struct{}{"d": {}}
	node.lazy = map[string]struct{}{"b": {}, "c": {}}

	got, err := node.OnGraft(1, "b", "missing")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 0 {
		t.Fatalf("GRAFT for unknown id emitted %#v", got)
	}
	if _, eager := node.eager["b"]; !eager {
		t.Fatal("unknown-id graft did not move sender eager")
	}

	node.seen["m1"] = "payload"
	got, err = node.OnGraft(2, "c", "m1")
	if err != nil {
		t.Fatal(err)
	}
	assertMessages(t, got, []Message{{Target: "c", Type: Gossip, ID: "m1", Payload: "payload"}})
	if _, eager := node.eager["c"]; !eager {
		t.Fatal("known-id graft did not move sender eager")
	}
}

func TestTickGraftMovesAdviserEager(t *testing.T) {
	node := newTestNode(t)

	if _, err := node.OnIHave(1, "b", "m1"); err != nil {
		t.Fatal(err)
	}
	if _, err := node.OnPrune(2, "b"); err != nil {
		t.Fatal(err)
	}
	got, err := node.Tick(4)
	if err != nil {
		t.Fatal(err)
	}

	assertMessages(t, got, []Message{{Target: "b", Type: Graft, ID: "m1"}})
	if _, lazy := node.lazy["b"]; lazy {
		t.Fatal("adviser remained lazy after graft")
	}
	if _, eager := node.eager["b"]; !eager {
		t.Fatal("adviser did not become eager after graft")
	}
}

func TestRejectionAtomicAndPriority(t *testing.T) {
	tests := []struct {
		name string
		call func(*Node) error
		want error
	}{
		{
			name: "broadcast clock before empty id",
			call: func(n *Node) error {
				_, err := n.Broadcast(0, "", "payload")
				return err
			},
			want: ErrClockBackwards,
		},
		{
			name: "gossip empty id before unknown from",
			call: func(n *Node) error {
				_, err := n.OnGossip(2, "x", "", "payload")
				return err
			},
			want: ErrEmptyMessageID,
		},
		{
			name: "gossip unknown from before duplicate id",
			call: func(n *Node) error {
				_, err := n.OnGossip(2, "x", "m1", "payload")
				return err
			},
			want: ErrUnknownNeighbor,
		},
		{
			name: "ihave clock before empty id",
			call: func(n *Node) error {
				_, err := n.OnIHave(0, "x", "")
				return err
			},
			want: ErrClockBackwards,
		},
		{
			name: "prune clock before unknown from",
			call: func(n *Node) error {
				_, err := n.OnPrune(0, "x")
				return err
			},
			want: ErrClockBackwards,
		},
		{
			name: "graft empty id before unknown from",
			call: func(n *Node) error {
				_, err := n.OnGraft(2, "x", "")
				return err
			},
			want: ErrEmptyMessageID,
		},
		{
			name: "tick clock backwards",
			call: func(n *Node) error {
				_, err := n.Tick(0)
				return err
			},
			want: ErrClockBackwards,
		},
		{
			name: "duplicate broadcast",
			call: func(n *Node) error {
				_, err := n.Broadcast(2, "m1", "other")
				return err
			},
			want: ErrMessageAlreadySeen,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			node := newTestNode(t)
			if _, err := node.Broadcast(1, "m1", "payload"); err != nil {
				t.Fatal(err)
			}

			if err := tt.call(node); !errors.Is(err, tt.want) {
				t.Fatalf("error = %v, want %v", err, tt.want)
			}

			wantSeen := map[string]string{"m1": "payload"}
			if !reflect.DeepEqual(node.seen, wantSeen) {
				t.Fatalf("seen changed after rejection: %#v", node.seen)
			}
			if node.lastNow != 1 || !node.haveClockEntry {
				t.Fatalf("clock changed after rejection: now=%d present=%v", node.lastNow, node.haveClockEntry)
			}
			if len(node.advisories) != 0 || len(node.deadlines) != 0 {
				t.Fatal("timers changed after rejection")
			}

			got, err := node.Broadcast(2, "m2", "still works")
			if err != nil {
				t.Fatal(err)
			}
			if len(got) != 3 {
				t.Fatalf("post-rejection broadcast emitted %d messages", len(got))
			}
		})
	}
}

func TestInvalidConstructors(t *testing.T) {
	tests := []struct {
		name      string
		self      string
		neighbors []string
		t1        int64
		t2        int64
		want      error
	}{
		{"empty self", "", []string{"b"}, 1, 1, ErrEmptyNodeID},
		{"no neighbors", "a", nil, 1, 1, ErrNoNeighbors},
		{"duplicate neighbor", "a", []string{"b", "b"}, 1, 1, ErrDuplicateNeighbor},
		{"empty neighbor", "a", []string{"b", ""}, 1, 1, ErrEmptyNeighborID},
		{"self neighbor", "a", []string{"a"}, 1, 1, ErrNeighborIsSelf},
		{"zero t1", "a", []string{"b"}, 0, 1, ErrInvalidT1},
		{"negative t2", "a", []string{"b"}, 1, -1, ErrInvalidT2},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := NewNode(tt.self, tt.neighbors, tt.t1, tt.t2)
			if !errors.Is(err, tt.want) {
				t.Fatalf("error = %v, want %v", err, tt.want)
			}
		})
	}
}

func TestConcurrentCallsSerialize(t *testing.T) {
	node, err := NewNode("a", []string{"b", "c", "d"}, 5, 2)
	if err != nil {
		t.Fatal(err)
	}

	const goroutines = 32
	var wg sync.WaitGroup
	for i := range goroutines {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			id := []string{"b", "c", "d"}[i%3]
			_, _ = node.OnIHave(1, id, "m1")
		}(i)
	}
	wg.Wait()
	if _, err := node.Tick(2); err != nil {
		t.Fatal(err)
	}

	got := node.advisories["m1"]
	want := map[string]int{"b": 1, "c": 1, "d": 1}
	counts := map[string]int{}
	for _, adviser := range got {
		counts[adviser]++
	}
	if !reflect.DeepEqual(counts, want) {
		t.Fatalf("advisories = %#v, want each neighbor exactly once (%#v)", got, want)
	}
}

func TestSameCallSequenceReplaysIdenticalMessages(t *testing.T) {
	runSequence := func() []Message {
		node, err := NewNode("a", []string{"d", "b", "c"}, 3, 2)
		if err != nil {
			t.Fatal(err)
		}

		record := func(out []Message, err error) []Message {
			t.Helper()
			if err != nil {
				t.Fatal(err)
			}
			return out
		}

		messages := []Message{}
		messages = append(messages, record(node.Broadcast(1, "m1", "payload"))...)
		messages = append(messages, record(node.OnPrune(2, "d"))...)
		messages = append(messages, record(node.OnIHave(3, "b", "m2"))...)
		messages = append(messages, record(node.OnIHave(3, "b", "m2"))...)
		messages = append(messages, record(node.OnIHave(3, "c", "m2"))...)
		messages = append(messages, record(node.Tick(6))...)
		messages = append(messages, record(node.Tick(8))...)
		messages = append(messages, record(node.OnGraft(9, "b", "m2"))...)
		return messages
	}

	first := runSequence()
	second := runSequence()
	if !reflect.DeepEqual(first, second) {
		t.Fatalf("replayed messages differ:\nfirst=%#v\nsecond=%#v", first, second)
	}

	want := []Message{
		{Target: "b", Type: Gossip, ID: "m1", Payload: "payload"},
		{Target: "c", Type: Gossip, ID: "m1", Payload: "payload"},
		{Target: "d", Type: Gossip, ID: "m1", Payload: "payload"},
		{Target: "b", Type: Graft, ID: "m2"},
		{Target: "c", Type: Graft, ID: "m2"},
	}
	assertMessages(t, second, want)
}

func newTestNode(t *testing.T) *Node {
	t.Helper()
	node, err := NewNode("a", []string{"b", "c", "d"}, 3, 2)
	if err != nil {
		t.Fatal(err)
	}
	return node
}

func assertMessages(t *testing.T, got, want []Message) {
	t.Helper()
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("messages = %#v, want %#v", got, want)
	}
}
