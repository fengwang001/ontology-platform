package export

import (
	"context"
	"fmt"
	"math/rand"
	"os"
	"strings"
	"testing"
)

// journal records every input, output and judgement basis of one random run,
// so any divergence is replayable and auditable.
type journal struct{ b strings.Builder }

func (j *journal) logf(format string, args ...any) {
	fmt.Fprintf(&j.b, format+"\n", args...)
}

// TestRandomizedDifferential drives one link through hundreds of randomly
// constructed interruption, retry and (occasionally) checkpoint-damage
// sequences, and compares every decision and the final consumer stream
// against the step-by-step naive model.
func TestRandomizedDifferential(t *testing.T) {
	const runs = 300
	for seed := int64(1); seed <= runs; seed++ {
		j := &journal{}
		j.logf("seed=%d", seed)
		rng := rand.New(rand.NewSource(seed))

		cp, h := NewMemCheckpoint(), NewMemHistory()
		m := NewManager(cp, h)
		sink := newRecSink()
		ref := newNaiveModel()

		var total Position = 30 + Position(rng.Intn(40))
		var cur Position
		var c *Cycle
		attempts := 0
		for cur < total {
			end := cur + 1 + Position(rng.Intn(6))
			if end > total {
				end = total
			}
			corrupt := rng.Intn(8) == 0
			if corrupt {
				cp.Corrupt["L"] = true
			} else {
				delete(cp.Corrupt, "L")
			}
			j.logf("begin declared=%d end=%d checkpointCorrupt=%v", cur, end, corrupt)

			wantKind := ref.begin(cur, end, corrupt)
			var err error
			c, err = m.Begin(context.Background(), "L", Range{cur, end})
			if k := errKind(err); k != wantKind {
				j.logf("BEGIN MISMATCH component=%s model=%s err=%v", k, wantKind, err)
				t.Fatalf("seed %d begin divergence\n%s", seed, j.b.String())
			}
			if wantKind == KindStartMismatch {
				// Only a repaired medium / honest declared start can proceed.
				cp.Repair("L", ref.confirmed)
				j.logf("medium repaired at %d; retrying begin", ref.confirmed)
				continue
			}

			// The source feeds each distinct write of the interval in log
			// order; random retries of already-accepted writes are inserted
			// between later first submissions. This is the meaningful stress
			// of the position rule: identical first-acceptance order on every
			// replay, retries landing anywhere leave no trace.
			nSubmit := 1 + rng.Intn(int(end-cur))
			feed := cur + Position(nSubmit)
			type sub struct {
				s     Position
				retry bool
			}
			var subs []sub
			for s := cur + 1; s <= feed; s++ {
				subs = append(subs, sub{s: s})
				if s > cur+1 && rng.Intn(2) == 0 {
					subs = append(subs, sub{s: cur + 1 + Position(rng.Intn(int(s-cur-1))), retry: true})
				}
			}
			for _, sb := range subs {
				s := sb.s
				tries := 1
				if sb.retry {
					tries += rng.Intn(3)
				}
				for k := 0; k < tries; k++ {
					w := Write{s, writeID(s)}
					freshC, errC := c.Accept(context.Background(), w)
					freshM, kindM := ref.accept(w)
					if errKind(errC) != kindM || (errC == nil && freshC != freshM) {
						j.logf("ACCEPT MISMATCH w=%v component(fresh=%v,err=%v) model(fresh=%v,kind=%s)",
							w, freshC, errC, freshM, kindM)
						t.Fatalf("seed %d accept divergence\n%s", seed, j.b.String())
					}
					j.logf("accept seq=%d id=%s fresh=%v retry=%v", s, w.ID, freshC, sb.retry)
				}
			}

			complete := int(end-cur) == len(c.dedup.Merged())
			attempts++
			die := !complete || rng.Intn(3) == 0
			if die {
				// Interrupt before confirmation (optionally after a partial
				// deliver that exercises sink idempotency).
				if rng.Intn(2) == 0 {
					ws := c.dedup.Merged()
					if len(ws) > 0 {
						victim := ws[rng.Intn(len(ws))].ID
						sink.failAt[victim] = true
					}
					_, errD := c.Deliver(context.Background(), sink)
					j.logf("deliver interrupted err=%v", errD)
				} else {
					j.logf("abandon before deliver")
				}
				c.Abandon()
				ref.abandon()
				cp.Repair("L", ref.confirmed) // medium usable again next begin
				continue
			}

			// Full interval present: deliver, then maybe the checkpoint save
			// hits the damaged medium; model mirrors both outcomes.
			if _, err := c.Deliver(context.Background(), sink); err != nil {
				j.logf("deliver failed: %v", err)
				c.Abandon()
				ref.abandon()
				cp.Repair("L", ref.confirmed)
				continue
			}
			wantConfirm := ref.confirm(corrupt)
			err = c.Confirm()
			if k := errKind(err); k != wantConfirm {
				j.logf("CONFIRM MISMATCH component=%s model=%s", k, wantConfirm)
				t.Fatalf("seed %d confirm divergence\n%s", seed, j.b.String())
			}
			if wantConfirm == 0 {
				j.logf("confirmed end=%d", end)
				cur = end
			} else {
				j.logf("confirm failed kind=%s; restarting", wantConfirm)
				c.Abandon()
				ref.abandon()
				cp.Repair("L", ref.confirmed)
			}
		}

		got := sink.Emitted()
		if len(got) != len(ref.emitted) {
			j.logf("FINAL MISMATCH component=%v model=%v", ids(got), ref.emitted)
			t.Fatalf("seed %d length divergence\n%s", seed, j.b.String())
		}
		for i := range ref.emitted {
			if got[i].ID != ref.emitted[i] {
				j.logf("FINAL MISMATCH at %d component=%s model=%s", i, got[i].ID, ref.emitted[i])
				t.Fatalf("seed %d stream divergence\n%s", seed, j.b.String())
			}
		}
		t.Logf("seed %d converged with %d attempts, %d writes", seed, attempts, len(got))
	}
}

// Dump a sample journal to a file so reviewers can inspect recorded inputs,
// outputs and judgement bases without reproducing a failure.
func TestWriteSampleJournal(t *testing.T) {
	if os.Getenv("EXPORT_JOURNAL") == "" {
		t.Skip("set EXPORT_JOURNAL=1 to write a sample run journal")
	}
	j := &journal{}
	j.logf("sample run")
	j.logf("begin declared=0 end=3 checkpointCorrupt=false")
	j.logf("accept seq=1 id=w1 fresh=true")
	j.logf("accept seq=1 id=w1 fresh=false (retry, no position change)")
	j.logf("accept seq=2 id=w2 fresh=true")
	j.logf("accept seq=3 id=w3 fresh=true")
	j.logf("confirmed end=3 output=[w1 w2 w3]")
	if err := os.WriteFile("testdata/sample-journal.log", []byte(j.b.String()), 0o644); err != nil {
		t.Fatal(err)
	}
}
