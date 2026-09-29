package chainreplication

import (
	"io"
	"math/rand"
	"testing"
)

type opKind int

const (
	opWrite opKind = iota
	opDeliverAt
	opDeliverAll
	opFail
	opRead
)

type op struct {
	kind opKind
	idx  int
	id   string
	val  string
}

type snapshot struct {
	outcomes map[int]bool // seq -> committed
	chain    []string
	reads    [][]int // committed seqs seen by each read
}

// runScript executes a fixed operation/delivery script and records results.
func runScript(script []op) snapshot {
	rec := newRecorder()
	net := NewQueueNetwork(nil)
	c, _ := NewCoordinator([]string{"H", "M", "N", "T"}, net,
		WithLogWriter(io.Discard), WithResultCallback(rec.callback))
	net.Bind(c.Deliver)

	snap := snapshot{outcomes: map[int]bool{}}
	for _, o := range script {
		switch o.kind {
		case opWrite:
			_, _ = c.Write(o.id, o.val)
		case opDeliverAt:
			pending := net.Pending()
			if o.idx < len(pending) {
				net.Deliver(o.idx)
			}
		case opDeliverAll:
			deliverAll(c, net)
		case opFail:
			_ = c.Fail(o.id)
		case opRead:
			entries, _ := c.Read(o.id)
			seqs := make([]int, len(entries))
			for i, e := range entries {
				seqs[i] = e.Seq
			}
			snap.reads = append(snap.reads, seqs)
		}
	}
	for seq, committed := range rec.commits {
		snap.outcomes[seq] = committed
	}
	snap.chain = c.Chain()
	return snap
}

// TestReplayDeterminism generates a random but fixed script involving
// writes, arbitrary-index (reordered/duplicate) deliveries, failures and
// reads, then replays the SAME script twice and demands identical results.
func TestReplayDeterminism(t *testing.T) {
	rng := rand.New(rand.NewSource(20260930))
	alive := []string{"H", "M", "N", "T"}
	failOrder := []string{"N", "M", "H"} // middle, then another middle, then head
	failPtr := 0

	script := []op{}
	for i := 0; i < 400; i++ {
		switch rng.Intn(10) {
		case 0, 1, 2, 3, 4:
			script = append(script, op{kind: opWrite, id: alive[0], val: "v"})
		case 5, 6, 7:
			script = append(script, op{kind: opDeliverAt, idx: rng.Intn(6)})
		case 8:
			script = append(script, op{kind: opDeliverAll})
		case 9:
			if failPtr < len(failOrder) && len(alive) > 1 {
				id := failOrder[failPtr]
				failPtr++
				script = append(script, op{kind: opFail, id: id})
				next := make([]string, 0, len(alive))
				for _, x := range alive {
					if x != id {
						next = append(next, x)
					}
				}
				alive = next
			} else {
				script = append(script, op{kind: opRead, id: alive[len(alive)-1]})
			}
		}
	}
	script = append(script, op{kind: opDeliverAll})

	first := runScript(script)
	second := runScript(script)

	if len(first.outcomes) != len(second.outcomes) {
		t.Fatalf("outcome counts differ: %d vs %d", len(first.outcomes), len(second.outcomes))
	}
	for seq, committed := range first.outcomes {
		got, ok := second.outcomes[seq]
		if !ok || got != committed {
			t.Fatalf("seq %d differs on replay: first committed=%v second committed=%v seen=%v",
				seq, committed, got, ok)
		}
	}
	if len(first.chain) != len(second.chain) {
		t.Fatalf("final chain differs: %v vs %v", first.chain, second.chain)
	}
	for i := range first.chain {
		if first.chain[i] != second.chain[i] {
			t.Fatalf("final chain order differs: %v vs %v", first.chain, second.chain)
		}
	}
	if len(first.reads) != len(second.reads) {
		t.Fatalf("read count differs: %d vs %d", len(first.reads), len(second.reads))
	}
	for i := range first.reads {
		if len(first.reads[i]) != len(second.reads[i]) {
			t.Fatalf("read %d differs: %v vs %v", i, first.reads[i], second.reads[i])
		}
		for j := range first.reads[i] {
			if first.reads[i][j] != second.reads[i][j] {
				t.Fatalf("read %d differs: %v vs %v", i, first.reads[i], second.reads[i])
			}
		}
	}
	t.Logf("replay matched: %d outcomes, %d reads, chain=%v",
		len(first.outcomes), len(first.reads), first.chain)
}
