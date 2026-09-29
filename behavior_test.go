package ontology

import (
	"strings"
	"sync"
	"testing"
	"time"
)

func TestSelfHealAndDeadIrreversibility(t *testing.T) {
	node, logs := testNode(t, 2, 4)

	decision, err := node.Receive(Message{Member: "a", Status: Suspect, Incarnation: 5})
	if err != nil {
		t.Fatalf("Receive(suspect self) error = %v", err)
	}
	if !decision.SelfHealed {
		t.Fatalf("SelfHealed = false; want true")
	}
	if got := node.View()["a"]; got != (ViewEntry{Status: Alive, Incarnation: 6}) {
		t.Fatalf("self view = %+v; want alive 6", got)
	}

	decision, err = node.Receive(Message{Member: "b", Status: Suspect, Incarnation: 5}, Message{Member: "b", Status: Alive, Incarnation: 6})
	if err != nil {
		t.Fatalf("Receive(heal other) error = %v", err)
	}
	if got := node.View()["b"]; got != (ViewEntry{Status: Alive, Incarnation: 6}) {
		t.Fatalf("b view = %+v; want alive 6", got)
	}
	if decision.AcceptedCount != 2 || decision.ReplacedCount != 2 {
		t.Fatalf("counts = accepted %d replaced %d; want 2 and 2", decision.AcceptedCount, decision.ReplacedCount)
	}

	if _, err := node.Receive(Message{Member: "b", Status: Dead, Incarnation: 6}); err != nil {
		t.Fatalf("Receive(dead) error = %v", err)
	}
	if _, err := node.Receive(Message{Member: "b", Status: Alive, Incarnation: 100}); err != nil {
		t.Fatalf("Receive(after dead) error = %v", err)
	}
	if got := node.View()["b"]; got != (ViewEntry{Status: Dead, Incarnation: 6}) {
		t.Fatalf("b view = %+v; dead must remain dead 6", got)
	}

	if _, err := node.Receive(Message{Member: "a", Status: Dead, Incarnation: 6}); err != nil {
		t.Fatalf("Receive(dead self) error = %v", err)
	}
	if !node.Exited() {
		t.Fatal("node should exit after own confirmed failure")
	}
	_, err = node.Receive(Message{Member: "c", Status: Suspect, Incarnation: 1})
	assertReject(t, err, RejectNodeExited)
	_, err = node.AdvanceClock(time.Nanosecond)
	assertReject(t, err, RejectNodeExited)
	_, err = node.Outgoing()
	assertReject(t, err, RejectNodeExited)

	logText := logs.String()
	for _, want := range []string{"incarnation incremented", "alive with greater incarnation refutes suspect", "confirmed failure", "irreversible"} {
		if !strings.Contains(logText, want) {
			t.Fatalf("log %q does not contain %q", logText, want)
		}
	}
}

func TestSuspectTimeoutExactlyAtLimit(t *testing.T) {
	node, _ := testNode(t, 2, 4)
	if _, err := node.Receive(Message{Member: "b", Status: Suspect, Incarnation: 3}); err != nil {
		t.Fatalf("Receive() error = %v", err)
	}

	upgraded, err := node.AdvanceClock(9)
	if err != nil {
		t.Fatalf("AdvanceClock(9) error = %v", err)
	}
	if upgraded != 0 || node.View()["b"].Status != Suspect {
		t.Fatalf("after one tick before timeout: upgraded %d view %+v", upgraded, node.View()["b"])
	}

	upgraded, err = node.AdvanceClock(1)
	if err != nil {
		t.Fatalf("AdvanceClock(1) error = %v", err)
	}
	if upgraded != 1 {
		t.Fatalf("upgraded = %d; want 1", upgraded)
	}
	if got := node.View()["b"]; got != (ViewEntry{Status: Dead, Incarnation: 3}) {
		t.Fatalf("b = %+v; want dead 3", got)
	}
}

func TestPiggybackLimitAndReplacementResetsCount(t *testing.T) {
	node, _ := testNode(t, 1, 1)

	if _, err := node.Receive(Message{Member: "b", Status: Suspect, Incarnation: 1}); err != nil {
		t.Fatalf("Receive(b) error = %v", err)
	}
	if _, err := node.Receive(Message{Member: "c", Status: Suspect, Incarnation: 1}); err != nil {
		t.Fatalf("Receive(c) error = %v", err)
	}

	first, err := node.Outgoing()
	if err != nil {
		t.Fatalf("Outgoing() error = %v", err)
	}
	if len(first.Updates) != 1 || first.Updates[0].Member != "b" {
		t.Fatalf("first outgoing = %+v; want only b", first.Updates)
	}

	if _, err := node.Receive(Message{Member: "b", Status: Suspect, Incarnation: 2}); err != nil {
		t.Fatalf("Receive(b replacement) error = %v", err)
	}
	second, err := node.Outgoing()
	if err != nil {
		t.Fatalf("Outgoing() error = %v", err)
	}
	if len(second.Updates) != 1 || second.Updates[0].Member != "b" || second.Updates[0].Incarnation != 2 {
		t.Fatalf("second outgoing = %+v; want replacement b:2 first", second.Updates)
	}

	third, err := node.Outgoing()
	if err != nil {
		t.Fatalf("Outgoing() error = %v", err)
	}
	if len(third.Updates) != 1 || third.Updates[0].Member != "c" || third.Updates[0].Incarnation != 1 {
		t.Fatalf("third outgoing = %+v; want unsent c before b's second copy", third.Updates)
	}

	fourth, err := node.Outgoing()
	if err != nil {
		t.Fatalf("Outgoing() error = %v", err)
	}
	if len(fourth.Updates) != 1 || fourth.Updates[0].Member != "b" || fourth.Updates[0].Incarnation != 2 {
		t.Fatalf("fourth outgoing = %+v; want replacement b:2 second time", fourth.Updates)
	}
	fifth, err := node.Outgoing()
	if err != nil {
		t.Fatalf("Outgoing() error = %v", err)
	}
	if len(fifth.Updates) != 1 || fifth.Updates[0].Member != "c" {
		t.Fatalf("fifth outgoing = %+v; want c second time", fifth.Updates)
	}

	sixth, err := node.Outgoing()
	if err != nil {
		t.Fatalf("Outgoing() error = %v", err)
	}
	if len(sixth.Updates) != 0 {
		t.Fatalf("sixth outgoing = %+v; want empty after cap", sixth.Updates)
	}
}

func TestOrderAndDuplicatesConverge(t *testing.T) {
	messages := []Message{
		{Member: "b", Status: Alive, Incarnation: 2},
		{Member: "b", Status: Suspect, Incarnation: 2},
		{Member: "b", Status: Alive, Incarnation: 6},
		{Member: "c", Status: Suspect, Incarnation: 4},
		{Member: "c", Status: Suspect, Incarnation: 3},
	}
	orders := [][]int{
		{0, 1, 2, 3, 4},
		{4, 3, 2, 1, 0},
		{2, 0, 4, 1, 3},
	}

	var baseline map[string]ViewEntry
	for index, order := range orders {
		node, _ := testNode(t, 2, 4)
		var shuffled []Message
		for _, messageIndex := range order {
			shuffled = append(shuffled, messages[messageIndex], messages[messageIndex])
		}
		if _, err := node.Receive(shuffled...); err != nil {
			t.Fatalf("order %d Receive() error = %v", index, err)
		}
		got := node.View()
		if baseline == nil {
			baseline = got
			continue
		}
		if !equalViews(got, baseline) {
			t.Fatalf("order %d view = %+v; want %+v", index, got, baseline)
		}
	}

	if baseline["b"] != (ViewEntry{Status: Alive, Incarnation: 6}) {
		t.Fatalf("b = %+v; want alive 6", baseline["b"])
	}
	if baseline["c"] != (ViewEntry{Status: Suspect, Incarnation: 4}) {
		t.Fatalf("c = %+v; want suspect 4", baseline["c"])
	}
}

func TestSelfDeadBatchIsOrderIndependent(t *testing.T) {
	messages := []Message{
		{Member: "c", Status: Suspect, Incarnation: 7},
		{Member: "a", Status: Dead, Incarnation: 6},
	}

	first, _ := testNode(t, 2, 4)
	if _, err := first.Receive(messages...); err != nil {
		t.Fatalf("Receive(dead first order) error = %v", err)
	}
	second, _ := testNode(t, 2, 4)
	if _, err := second.Receive(messages[1], messages[0]); err != nil {
		t.Fatalf("Receive(dead last order) error = %v", err)
	}

	if !equalViews(first.View(), second.View()) {
		t.Fatalf("views differ: %+v vs %+v", first.View(), second.View())
	}
	if !first.Exited() || !second.Exited() {
		t.Fatal("both nodes should exit")
	}
	if got := first.View()["c"]; got != (ViewEntry{Status: Suspect, Incarnation: 7}) {
		t.Fatalf("c = %+v; want suspect 7 from same batch", got)
	}
}

func TestConcurrentAccess(t *testing.T) {
	node, _ := testNode(t, 4, 2)
	var wg sync.WaitGroup
	for range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for range 20 {
				_, _ = node.Receive(Message{Member: "b", Status: Suspect, Incarnation: 2})
				_ = node.View()
				_, _ = node.Outgoing()
				_, _ = node.AdvanceClock(time.Nanosecond)
			}
		}()
	}
	wg.Wait()
}

func TestRepeatedInputAndClockSequenceIsDeterministic(t *testing.T) {
	run := func() (map[string]ViewEntry, [][]Message) {
		node, _ := testNode(t, 1, 2)
		steps := []func(){
			func() {
				_, err := node.Receive(Message{Member: "b", Status: Suspect, Incarnation: 2})
				if err != nil {
					t.Fatal(err)
				}
			},
			func() {
				_, err := node.AdvanceClock(5)
				if err != nil {
					t.Fatal(err)
				}
			},
			func() {
				_, err := node.Receive(Message{Member: "c", Status: Alive, Incarnation: 4})
				if err != nil {
					t.Fatal(err)
				}
			},
			func() {
				_, err := node.AdvanceClock(5)
				if err != nil {
					t.Fatal(err)
				}
			},
		}
		for _, step := range steps {
			step()
		}

		var outgoing [][]Message
		for range 4 {
			message, err := node.Outgoing()
			if err != nil {
				t.Fatal(err)
			}
			outgoing = append(outgoing, message.Updates)
		}
		return node.View(), outgoing
	}

	firstView, firstOutgoing := run()
	secondView, secondOutgoing := run()
	if !equalViews(firstView, secondView) {
		t.Fatalf("views differ: %+v vs %+v", firstView, secondView)
	}
	if !equalMessageBatches(firstOutgoing, secondOutgoing) {
		t.Fatalf("outgoing differs: %+v vs %+v", firstOutgoing, secondOutgoing)
	}
}

func equalMessageBatches(left, right [][]Message) bool {
	if len(left) != len(right) {
		return false
	}
	for i := range left {
		if len(left[i]) != len(right[i]) {
			return false
		}
		for j := range left[i] {
			if left[i][j] != right[i][j] {
				return false
			}
		}
	}
	return true
}
