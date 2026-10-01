package ontology

import (
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
)

func requireErrorIs(t *testing.T, got error, want error) {
	t.Helper()
	if !errors.Is(got, want) {
		t.Fatalf("error = %v, want %v", got, want)
	}
}

func TestSpecSequence(t *testing.T) {
	registry := NewRegistry()

	seed, err := registry.Seed("root")
	if err != nil {
		t.Fatal(err)
	}
	if seed != "(1;0)" {
		t.Fatalf("Seed = %q", seed)
	}

	evented, err := registry.Event("root")
	if err != nil {
		t.Fatal(err)
	}
	if evented != "(1;1)" {
		t.Fatalf("Event = %q", evented)
	}

	parent, child, err := registry.Fork("root", "child")
	if err != nil {
		t.Fatal(err)
	}
	if parent != "((1,0);1)" || child != "((0,1);1)" {
		t.Fatalf("Fork = %q, %q", parent, child)
	}

	parent, err = registry.Event("root")
	if err != nil {
		t.Fatal(err)
	}
	child, err = registry.Event("child")
	if err != nil {
		t.Fatal(err)
	}
	if parent != "((1,0);(1,1,0))" {
		t.Fatalf("parent Event = %q", parent)
	}
	if child != "((0,1);(1,0,1))" {
		t.Fatalf("child Event = %q", child)
	}

	joined, err := registry.Join("root", "child")
	if err != nil {
		t.Fatal(err)
	}
	if joined != "(1;2)" {
		t.Fatalf("Join = %q", joined)
	}

	peek, err := registry.Peek("root")
	if err != nil {
		t.Fatal(err)
	}
	if peek != "(0;2)" {
		t.Fatalf("Peek = %q", peek)
	}
}

func TestFillNodeBranches(t *testing.T) {
	generic := fillEvent(
		idPair{left: idPair{left: idOne{}, right: idZero{}}, right: idPair{left: idZero{}, right: idOne{}}},
		eventPair{
			value: 5,
			left:  eventPair{value: 2, left: eventInt{0}, right: eventInt{0}},
			right: eventPair{value: 3, left: eventInt{0}, right: eventInt{0}},
		},
	)
	if generic.String() != "(7,0,1)" {
		t.Fatalf("generic fill = %s", generic)
	}

	leftOne := fillEvent(
		idPair{left: idOne{}, right: idPair{left: idOne{}, right: idZero{}}},
		eventPair{
			value: 5,
			left:  eventPair{value: 2, left: eventInt{0}, right: eventInt{0}},
			right: eventPair{value: 4, left: eventInt{1}, right: eventInt{0}},
		},
	)
	if leftOne.String() != "(9,0,(0,1,0))" {
		t.Fatalf("left-one fill = %s", leftOne)
	}

	rightOne := fillEvent(
		idPair{left: idPair{left: idZero{}, right: idOne{}}, right: idOne{}},
		eventPair{
			value: 5,
			left:  eventPair{value: 4, left: eventInt{0}, right: eventInt{1}},
			right: eventPair{value: 2, left: eventInt{0}, right: eventInt{0}},
		},
	)
	if rightOne.String() != "(9,(0,0,1),0)" {
		t.Fatalf("right-one fill = %s", rightOne)
	}
}

func TestGrowTieAndIntegerCost(t *testing.T) {
	id := idPair{left: idPair{left: idOne{}, right: idZero{}}, right: idPair{left: idZero{}, right: idOne{}}}
	asymmetricNode := eventPair{
		value: 0,
		left:  eventPair{value: 0, left: eventInt{1}, right: eventInt{0}},
		right: eventPair{value: 0, left: eventInt{0}, right: eventInt{1}},
	}

	grown, cost := growEvent(id, asymmetricNode)
	if grown.String() != "(0,(0,1,0),(0,0,2))" || cost != 2 {
		t.Fatalf("tie grow = (%s, %d)", grown, cost)
	}

	singlePenalty := idPair{left: idOne{}, right: idZero{}}
	grown, cost = growEvent(singlePenalty, eventInt{0})
	if grown.String() != "(0,1,0)" || cost != 1000001 {
		t.Fatalf("single integer penalty = (%s, %d)", grown, cost)
	}

	grown, cost = growEvent(id, eventInt{0})
	if grown.String() != "(0,0,(0,0,1))" || cost != 2000002 {
		t.Fatalf("integer grow = (%s, %d)", grown, cost)
	}
}

func TestEventLessOrEqualScalarBound(t *testing.T) {
	node := eventPair{value: 1, left: eventInt{0}, right: eventInt{0}}
	if eventLE(eventInt{1}, eventInt{2}) != true {
		t.Fatal("scalar 1 must be <= scalar 2")
	}
	if eventLE(node, eventInt{0}) {
		t.Fatal("event rooted at 1 must not be <= 0")
	}
}

func TestCanonicalEventPairsHaveAZeroMinimumChild(t *testing.T) {
	id := idPair{left: idPair{left: idOne{}, right: idZero{}}, right: idPair{left: idZero{}, right: idOne{}}}
	for input := 0; input < 20; input++ {
		event, _ := growEvent(id, eventInt{input})
		assertCanonicalEvent(t, event)
	}
}

func assertCanonicalEvent(t *testing.T, event Event) {
	t.Helper()
	switch event := event.(type) {
	case eventInt:
		if event.value < 0 {
			t.Fatalf("negative scalar event: %s", event)
		}
	case eventPair:
		if minEvent(event.left) != 0 && minEvent(event.right) != 0 {
			t.Fatalf("non-canonical event %s", event)
		}
		assertCanonicalEvent(t, event.left)
		assertCanonicalEvent(t, event.right)
	}
}

func TestIndependentSeedsOverlap(t *testing.T) {
	registry := NewRegistry()
	if _, err := registry.Seed("a"); err != nil {
		t.Fatal(err)
	}
	if _, err := registry.Seed("b"); err != nil {
		t.Fatal(err)
	}
	_, err := registry.Join("a", "b")
	requireErrorIs(t, err, ErrIdentityOverlap)
}

func TestCompareResults(t *testing.T) {
	registry := NewRegistry()
	if _, err := registry.Seed("root"); err != nil {
		t.Fatal(err)
	}
	if _, _, err := registry.Fork("root", "equal-a"); err != nil {
		t.Fatal(err)
	}
	if _, _, err := registry.Fork("root", "equal-b"); err != nil {
		t.Fatal(err)
	}
	if _, _, err := registry.Fork("root", "later"); err != nil {
		t.Fatal(err)
	}
	if _, _, err := registry.Fork("root", "earlier"); err != nil {
		t.Fatal(err)
	}
	if _, err := registry.Join("root", "later"); err != nil {
		t.Fatal(err)
	}
	if _, err := registry.Event("root"); err != nil {
		t.Fatal(err)
	}

	cases := []struct {
		first  string
		second string
		want   Comparison
	}{
		{"equal-a", "equal-b", Equal},
		{"earlier", "root", Before},
		{"root", "earlier", After},
	}
	for _, tc := range cases {
		got, err := registry.Compare(tc.first, tc.second)
		if err != nil {
			t.Fatal(err)
		}
		if got != tc.want {
			t.Fatalf("Compare(%s,%s) = %s, want %s", tc.first, tc.second, got, tc.want)
		}
	}

	if _, _, err := registry.Fork("equal-a", "fork-a"); err != nil {
		t.Fatal(err)
	}
	if _, _, err := registry.Fork("root", "fork-b"); err != nil {
		t.Fatal(err)
	}
	if _, err := registry.Event("fork-a"); err != nil {
		t.Fatal(err)
	}
	if _, err := registry.Event("fork-b"); err != nil {
		t.Fatal(err)
	}
	got, err := registry.Compare("fork-a", "fork-b")
	if err != nil {
		t.Fatal(err)
	}
	if got != Concurrent {
		t.Fatalf("Compare concurrent forks = %s", got)
	}
}

func TestValidationAndAtomicFailure(t *testing.T) {
	registry := NewRegistry()
	if _, err := registry.Seed("known"); err != nil {
		t.Fatal(err)
	}

	_, err := registry.Seed("")
	requireErrorIs(t, err, ErrEmptyName)
	_, err = registry.Seed("known")
	requireErrorIs(t, err, ErrReplicaExists)

	_, _, err = registry.Fork("unknown", "known")
	requireErrorIs(t, err, ErrUnknownReplica)
	_, _, err = registry.Fork("known", "known")
	requireErrorIs(t, err, ErrReplicaExists)

	_, err = registry.Join("known", "known")
	requireErrorIs(t, err, ErrSameReplica)
	_, err = registry.Join("unknown", "known")
	requireErrorIs(t, err, ErrUnknownReplica)
	_, err = registry.Join("known", "unknown")
	requireErrorIs(t, err, ErrUnknownReplica)

	_, err = registry.Event("missing")
	requireErrorIs(t, err, ErrUnknownReplica)
	_, err = registry.Peek("missing")
	requireErrorIs(t, err, ErrUnknownReplica)

	if got := fmt.Sprint(registry.replicas); !strings.Contains(got, "known") {
		t.Fatalf("state changed after rejected operations: %s", got)
	}
}

func TestConcurrentEventsAreSerializable(t *testing.T) {
	registry := NewRegistry()
	if _, err := registry.Seed("counter"); err != nil {
		t.Fatal(err)
	}

	const workers = 32
	const iterations = 20
	var wait sync.WaitGroup
	for range workers {
		wait.Add(1)
		go func() {
			defer wait.Done()
			for range iterations {
				if _, err := registry.Event("counter"); err != nil {
					t.Error(err)
					return
				}
				if _, err := registry.Peek("counter"); err != nil {
					t.Error(err)
					return
				}
			}
		}()
	}
	wait.Wait()

	peek, err := registry.Peek("counter")
	if err != nil {
		t.Fatal(err)
	}
	if peek != fmt.Sprintf("(0;%d)", workers*iterations) {
		t.Fatalf("counter = %q", peek)
	}
}
