package ontology

import (
	"errors"
	"testing"
)

func neighborSet(values ...string) map[string]struct{} {
	result := make(map[string]struct{}, len(values))
	for _, value := range values {
		result[value] = struct{}{}
	}
	return result
}

func mustNode(t *testing.T, self string, neighbors map[string]struct{}, t1, t2 int) *Node {
	t.Helper()
	node, err := NewNode(self, neighbors, t1, t2)
	if err != nil {
		t.Fatalf("NewNode() error = %v", err)
	}
	return node
}

func assertMessages(t *testing.T, got, want []Message) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("messages length = %d, want %d: %#v", len(got), len(want), got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("message[%d] = %#v, want %#v; all: %#v", i, got[i], want[i], got)
		}
	}
}

func assertSet(t *testing.T, got map[string]struct{}, want ...string) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("set size = %d, want %d: %v", len(got), len(want), got)
	}
	for _, value := range want {
		if _, ok := got[value]; !ok {
			t.Fatalf("set %v missing %q", got, value)
		}
	}
}

func TestConstructorValidationAndInitialState(t *testing.T) {
	tests := []struct {
		name      string
		self      string
		neighbors map[string]struct{}
		t1        int
		t2        int
		want      error
	}{
		{name: "empty self", self: "", neighbors: neighborSet("b"), t1: 1, t2: 1, want: ErrEmptySelf},
		{name: "empty neighbors", self: "a", neighbors: map[string]struct{}{}, t1: 1, t2: 1, want: ErrNoNeighbors},
		{name: "empty neighbor", self: "a", neighbors: neighborSet("", "b"), t1: 1, t2: 1, want: ErrEmptyNeighbor},
		{name: "self neighbor", self: "a", neighbors: neighborSet("a", "b"), t1: 1, t2: 1, want: ErrNeighborIsSelf},
		{name: "zero t1", self: "a", neighbors: neighborSet("b"), t1: 0, t2: 1, want: ErrInvalidT1},
		{name: "negative t2", self: "a", neighbors: neighborSet("b"), t1: 1, t2: -1, want: ErrInvalidT2},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := NewNode(tt.self, tt.neighbors, tt.t1, tt.t2)
			if !errors.Is(err, tt.want) {
				t.Fatalf("error = %v, want %v", err, tt.want)
			}
		})
	}

	neighbors := neighborSet("c", "b")
	node := mustNode(t, "a", neighbors, 3, 4)
	delete(neighbors, "c")
	neighbors["x"] = struct{}{}
	assertSet(t, node.eager, "b", "c")
	assertSet(t, node.lazy)
	if _, ok := node.neighbors["x"]; ok {
		t.Fatalf("constructor aliased input neighbors: %#v", node.neighbors)
	}
}

func TestBroadcastOrdersGossipThenIHave(t *testing.T) {
	node := mustNode(t, "a", neighborSet("b", "c", "d"), 2, 3)
	if _, err := node.OnPrune(1, "c"); err != nil {
		t.Fatal(err)
	}
	if _, err := node.OnPrune(1, "d"); err != nil {
		t.Fatal(err)
	}

	got, err := node.Broadcast(2, "m1", "payload")
	if err != nil {
		t.Fatal(err)
	}
	assertMessages(t, got, []Message{
		{Target: "b", Type: Gossip, ID: "m1", Payload: "payload"},
		{Target: "c", Type: IHave, ID: "m1"},
		{Target: "d", Type: IHave, ID: "m1"},
	})
}

func TestDuplicateGossipPrunesAndMovesSourceLazy(t *testing.T) {
	node := mustNode(t, "a", neighborSet("b", "c"), 2, 3)
	if _, err := node.OnGossip(1, "b", "m1", "one"); err != nil {
		t.Fatal(err)
	}

	got, err := node.OnGossip(2, "b", "m1", "one")
	if err != nil {
		t.Fatal(err)
	}
	assertMessages(t, got, []Message{{Target: "b", Type: Prune}})
	assertSet(t, node.eager, "c")
	assertSet(t, node.lazy, "b")

	got, err = node.OnGossip(3, "b", "m1", "one")
	if err != nil {
		t.Fatal(err)
	}
	assertMessages(t, got, []Message{{Target: "b", Type: Prune}})
	assertSet(t, node.eager, "c")
	assertSet(t, node.lazy, "b")
}

func TestFirstGossipFromLazyReturnsSourceToEagerAndClearsAdvertisement(t *testing.T) {
	node := mustNode(t, "a", neighborSet("b", "c"), 5, 2)
	if _, err := node.OnPrune(1, "b"); err != nil {
		t.Fatal(err)
	}
	if _, err := node.OnIHave(2, "b", "m1"); err != nil {
		t.Fatal(err)
	}

	got, err := node.OnGossip(3, "b", "m1", "payload")
	if err != nil {
		t.Fatal(err)
	}
	assertMessages(t, got, []Message{
		{Target: "c", Type: Gossip, ID: "m1", Payload: "payload"},
	})
	assertSet(t, node.eager, "b", "c")
	assertSet(t, node.lazy)
	if _, pending := node.pending["m1"]; pending {
		t.Fatalf("pending advertisement survived: %#v", node.pending["m1"])
	}

	got, err = node.Broadcast(4, "m2", "two")
	if err != nil {
		t.Fatal(err)
	}
	assertMessages(t, got, []Message{
		{Target: "b", Type: Gossip, ID: "m2", Payload: "two"},
		{Target: "c", Type: Gossip, ID: "m2", Payload: "two"},
	})
}

func TestIHaveQueueOrderAndTickDeadline(t *testing.T) {
	node := mustNode(t, "a", neighborSet("b", "c", "d"), 5, 2)
	if _, err := node.OnPrune(0, "b"); err != nil {
		t.Fatal(err)
	}
	if _, err := node.OnPrune(0, "d"); err != nil {
		t.Fatal(err)
	}
	for _, from := range []string{"c", "b", "c"} {
		if _, err := node.OnIHave(0, from, "m1"); err != nil {
			t.Fatal(err)
		}
	}

	got, err := node.Tick(5)
	if err != nil {
		t.Fatal(err)
	}
	assertMessages(t, got, []Message{{Target: "c", Type: Graft, ID: "m1"}})
	assertSet(t, node.eager, "c")
	assertSet(t, node.lazy, "b", "d")
	if node.pending["m1"].deadline != 7 {
		t.Fatalf("deadline = %d, want 7", node.pending["m1"].deadline)
	}
	if len(node.pending["m1"].sources) != 1 || node.pending["m1"].sources[0] != "b" {
		t.Fatalf("sources = %#v", node.pending["m1"].sources)
	}

	got, err = node.Tick(6)
	if err != nil {
		t.Fatal(err)
	}
	assertMessages(t, got, nil)

	got, err = node.Tick(7)
	if err != nil {
		t.Fatal(err)
	}
	assertMessages(t, got, []Message{{Target: "b", Type: Graft, ID: "m1"}})
	if _, pending := node.pending["m1"]; pending {
		t.Fatalf("pending advertisement survived after empty queue")
	}
}

func TestTickOffByOneDoesNotTrigger(t *testing.T) {
	node := mustNode(t, "a", neighborSet("b"), 5, 2)
	if _, err := node.OnIHave(10, "b", "m1"); err != nil {
		t.Fatal(err)
	}
	got, err := node.Tick(14)
	if err != nil {
		t.Fatal(err)
	}
	assertMessages(t, got, nil)
	if node.pending["m1"].deadline != 15 {
		t.Fatalf("deadline changed to %d", node.pending["m1"].deadline)
	}
}

func TestGossipClearsTimer(t *testing.T) {
	node := mustNode(t, "a", neighborSet("b", "c"), 5, 2)
	if _, err := node.OnIHave(1, "b", "m1"); err != nil {
		t.Fatal(err)
	}
	if _, err := node.OnGossip(2, "c", "m1", "payload"); err != nil {
		t.Fatal(err)
	}
	got, err := node.Tick(100)
	if err != nil {
		t.Fatal(err)
	}
	assertMessages(t, got, nil)
}

func TestGraftSeenAndUnseenMessages(t *testing.T) {
	seen := mustNode(t, "a", neighborSet("b"), 2, 3)
	if _, err := seen.OnPrune(1, "b"); err != nil {
		t.Fatal(err)
	}
	if _, err := seen.Broadcast(2, "m1", "payload"); err != nil {
		t.Fatal(err)
	}
	got, err := seen.OnGraft(3, "b", "m1")
	if err != nil {
		t.Fatal(err)
	}
	assertMessages(t, got, []Message{{Target: "b", Type: Gossip, ID: "m1", Payload: "payload"}})
	assertSet(t, seen.eager, "b")
	assertSet(t, seen.lazy)

	unseen := mustNode(t, "a", neighborSet("b"), 2, 3)
	if _, err := unseen.OnPrune(1, "b"); err != nil {
		t.Fatal(err)
	}
	got, err = unseen.OnGraft(2, "b", "m1")
	if err != nil {
		t.Fatal(err)
	}
	assertMessages(t, got, []Message{})
	assertSet(t, unseen.eager, "b")
	assertSet(t, unseen.lazy)
}

func TestOneTickProcessesEachIDAtMostOnce(t *testing.T) {
	node := mustNode(t, "a", neighborSet("b", "c"), 5, 2)
	if _, err := node.OnIHave(0, "b", "m1"); err != nil {
		t.Fatal(err)
	}
	if _, err := node.OnIHave(1, "c", "m1"); err != nil {
		t.Fatal(err)
	}

	got, err := node.Tick(100)
	if err != nil {
		t.Fatal(err)
	}
	assertMessages(t, got, []Message{{Target: "b", Type: Graft, ID: "m1"}})
	if node.pending["m1"].deadline != 102 {
		t.Fatalf("deadline = %d, want 102", node.pending["m1"].deadline)
	}

	got, err = node.Tick(101)
	if err != nil {
		t.Fatal(err)
	}
	assertMessages(t, got, nil)
}

func TestTickProcessesIDsInAscendingOrder(t *testing.T) {
	node := mustNode(t, "a", neighborSet("b", "c"), 5, 2)
	if _, err := node.OnIHave(0, "b", "m2"); err != nil {
		t.Fatal(err)
	}
	if _, err := node.OnIHave(0, "c", "m1"); err != nil {
		t.Fatal(err)
	}
	got, err := node.Tick(5)
	if err != nil {
		t.Fatal(err)
	}
	assertMessages(t, got, []Message{
		{Target: "c", Type: Graft, ID: "m1"},
		{Target: "b", Type: Graft, ID: "m2"},
	})
}
