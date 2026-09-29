package scheduler

import (
	"fmt"
	"log"
	"math/rand"
	"reflect"
	"testing"
)

// flatStarts collapses all start events produced by one scripted run into a
// single time-ordered sequence (time asc, then emission order).
type flatStart struct {
	time   Tick
	jobID  string
	reason StartReason
}

type scriptedOp struct {
	kind   string // "submit", "advance", "finish"
	id     string
	nodes  int
	dur    Tick
	target Tick
}

// buildScript generates one deterministic workload script from a seed.
func buildScript(seed int64, n, ticks, numJobs int) []scriptedOp {
	rng := rand.New(rand.NewSource(seed))
	var ops []scriptedOp
	seq := 0
	for tt := Tick(1); tt <= Tick(ticks); tt++ {
		for k := rng.Intn(4); k > 0 && seq < numJobs; k-- {
			seq++
			ops = append(ops, scriptedOp{
				kind:  "submit",
				id:    fmt.Sprintf("J%04d", seq),
				nodes: 1 + rng.Intn(n),
				dur:   Tick(1 + rng.Intn(8)),
			})
		}
		ops = append(ops, scriptedOp{kind: "advance", target: tt})
		// Deterministic early-finish policy: at fixed time points, finish
		// the lexicographically smallest running id. The same rule replayed
		// selects the same victim.
		if rng.Intn(3) == 0 {
			ops = append(ops, scriptedOp{kind: "finish"})
		}
	}
	return ops
}

func runScript(n int, ops []scriptedOp) []flatStart {
	var buf logBuffer
	s, err := New(n, log.New(&buf, "", 0))
	if err != nil {
		panic(err)
	}
	var out []flatStart
	for _, op := range ops {
		switch op.kind {
		case "submit":
			starts, err := s.Submit(Job{ID: op.id, Nodes: op.nodes, Duration: op.dur})
			if err != nil {
				panic(fmt.Sprintf("submit %s: %v", op.id, err))
			}
			for _, ev := range starts {
				out = append(out, flatStart{ev.Time, ev.JobID, ev.Reason})
			}
		case "advance":
			_, starts, err := s.Advance(op.target)
			if err != nil {
				panic(fmt.Sprintf("advance %d: %v", op.target, err))
			}
			for _, ev := range starts {
				out = append(out, flatStart{ev.Time, ev.JobID, ev.Reason})
			}
		case "finish":
			snap := s.Query()
			if len(snap.RunningIDs) == 0 {
				continue
			}
			victim := snap.RunningIDs[0] // already sorted lexicographically
			_, starts, err := s.Finish(victim)
			if err != nil {
				panic(fmt.Sprintf("finish %s: %v", victim, err))
			}
			for _, ev := range starts {
				out = append(out, flatStart{ev.Time, ev.JobID, ev.Reason})
			}
		}
	}
	return out
}

// TestReplayDeterminism replays identical Submit/Advance/Finish sequences and
// requires identical start sequences.
func TestReplayDeterminism(t *testing.T) {
	for _, seed := range []int64{11, 22, 33} {
		script := buildScript(seed, 6, 50, 120)
		first := runScript(6, script)
		second := runScript(6, script)
		if !reflect.DeepEqual(first, second) {
			t.Fatalf("seed=%d: replayed start sequences differ:\n%v\nvs\n%v",
				seed, first, second)
		}
	}
}
