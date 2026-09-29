package ontology

import (
	"bytes"
	"errors"
	"testing"
	"time"
)

func testNode(t *testing.T, lambda, batchSize int) (*Node, *bytes.Buffer) {
	t.Helper()
	var logs bytes.Buffer
	node, err := NewNode("a", []string{"a", "b", "c"}, lambda, batchSize, 10, &logs)
	if err != nil {
		t.Fatalf("NewNode() error = %v", err)
	}
	return node, &logs
}

func TestMergePriority(t *testing.T) {
	tests := []struct {
		name     string
		current  ViewEntry
		incoming ViewEntry
		want     bool
	}{
		{
			name:     "suspect same incarnation beats alive",
			current:  ViewEntry{Status: Alive, Incarnation: 5},
			incoming: ViewEntry{Status: Suspect, Incarnation: 5},
			want:     true,
		},
		{
			name:     "alive same incarnation does not beat suspect",
			current:  ViewEntry{Status: Suspect, Incarnation: 5},
			incoming: ViewEntry{Status: Alive, Incarnation: 5},
			want:     false,
		},
		{
			name:     "alive greater incarnation refutes suspect",
			current:  ViewEntry{Status: Suspect, Incarnation: 5},
			incoming: ViewEntry{Status: Alive, Incarnation: 6},
			want:     true,
		},
		{
			name:     "dead cannot be replaced",
			current:  ViewEntry{Status: Dead, Incarnation: 1},
			incoming: ViewEntry{Status: Alive, Incarnation: 99},
			want:     false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, reason := shouldReplace(tt.current, tt.incoming)
			if got != tt.want {
				t.Fatalf("shouldReplace() = %t, reason %q; want %t", got, reason, tt.want)
			}
		})
	}
}

func TestRejectionsDoNotMutateState(t *testing.T) {
	node, _ := testNode(t, 1, 2)
	before := node.View()

	_, err := node.Receive(Message{Member: "b", Status: Suspect, Incarnation: -1})
	assertReject(t, err, RejectNegativeIncarnation)
	_, err = node.Receive(Message{Member: "missing", Status: Suspect, Incarnation: 1})
	assertReject(t, err, RejectUnknownMember)
	_, err = node.Receive(
		Message{Member: "b", Status: Suspect, Incarnation: 2},
		Message{Member: "c", Status: Status("broken"), Incarnation: 2},
	)
	assertReject(t, err, RejectInvalidParameter)

	if got := node.View(); !equalViews(got, before) {
		t.Fatalf("rejected operation changed view: got %v want %v", got, before)
	}

	_, err = NewNode("a", []string{"a"}, 0, 2, time.Second, nil)
	assertReject(t, err, RejectInvalidParameter)
	_, err = NewNode("a", []string{"a"}, 1, 0, time.Second, nil)
	assertReject(t, err, RejectInvalidParameter)
}

func assertReject(t *testing.T, err error, reason RejectReason) {
	t.Helper()
	var rejected *RejectError
	if !errors.As(err, &rejected) || rejected.Reason != reason {
		t.Fatalf("error = %v; want reject reason %s", err, reason)
	}
}

func equalViews(left, right map[string]ViewEntry) bool {
	if len(left) != len(right) {
		return false
	}
	for member, entry := range left {
		if right[member] != entry {
			return false
		}
	}
	return true
}
