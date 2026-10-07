package export

import (
	"fmt"
)

// naiveModel is an independent, deliberately simple judge of the rules. It
// re-derives every decision step by step with linear scans (no reliance on
// the component's data structures):
//
//   - begin: declared start must equal the last confirmed end;
//   - accept: a write is fresh only if no earlier accepted write in THIS
//     attempt had the same ID (linear scan); first-acceptance order is kept;
//   - abandon/deliver failure: nothing is confirmed, the attempt vanishes;
//     the consumer may already have seen some writes but de-dupes by ID;
//   - confirm: every integer position of (start,end] must appear exactly
//     once; then the consumer sees them in sequence order and end advances.
type naiveModel struct {
	confirmed Position
	emitted   []string
	seenID    map[string]bool

	active bool
	start  Position
	end    Position
	order  []Write
	idSeen map[string]bool

	// sealedHistory is what an honest history medium proves: sealed end of
	// every confirmed interval, used to independently judge re-derivation.
	sealedEnds []Position
}

func newNaiveModel() *naiveModel {
	return &naiveModel{seenID: map[string]bool{}, idSeen: map[string]bool{}}
}

func (n *naiveModel) derivedStart(corrupt bool) Position {
	if !corrupt {
		return n.confirmed
	}
	if len(n.sealedEnds) == 0 {
		return 0
	}
	return n.sealedEnds[len(n.sealedEnds)-1]
}

func (n *naiveModel) begin(declared, end Position, corrupt bool) Kind {
	want := n.derivedStart(corrupt)
	if declared != want {
		return KindStartMismatch
	}
	n.active, n.start, n.end = true, declared, end
	n.order = nil
	n.idSeen = map[string]bool{}
	return 0
}

func (n *naiveModel) accept(w Write) (fresh bool, kind Kind) {
	if !n.active || w.Seq <= n.start || w.Seq > n.end {
		return false, KindStartMismatch
	}
	for _, p := range n.order { // deliberately O(n): the naive reference
		if p.ID == w.ID {
			return false, 0
		}
	}
	n.order = append(n.order, w)
	n.idSeen[w.ID] = true
	return true, 0
}

// abandon models an interruption before confirmation: no state commits.
func (n *naiveModel) abandon() {
	n.active = false
	n.order = nil
	n.idSeen = map[string]bool{}
}

// confirm judges coverage the naive way and returns the failure kind.
func (n *naiveModel) confirm(mediumCorrupt bool) Kind {
	if !n.active {
		return KindResourceExhausted
	}
	want := map[Position]string{}
	for s := n.start + 1; s <= n.end; s++ {
		want[s] = writeID(s)
	}
	got := map[Position]string{}
	for _, w := range n.order {
		if _, dup := got[w.Seq]; dup {
			return KindStartMismatch
		}
		got[w.Seq] = w.ID
	}
	if len(got) != len(want) {
		return KindResourceExhausted // incomplete interval: cannot confirm yet
	}
	for s, id := range want {
		if got[s] != id {
			return KindStartMismatch
		}
	}
	if mediumCorrupt {
		return KindResourceExhausted
	}
	// Output keeps first-acceptance order; coverage above only proves that
	// every position of the interval is present exactly once.
	for _, w := range n.order {
		if !n.seenID[w.ID] {
			n.seenID[w.ID] = true
			n.emitted = append(n.emitted, w.ID)
		}
	}
	n.confirmed = n.end
	n.sealedEnds = append(n.sealedEnds, n.end)
	n.active = false
	n.order = nil
	n.idSeen = map[string]bool{}
	return 0
}

func writeID(s Position) string { return fmt.Sprintf("w%d", s) }
