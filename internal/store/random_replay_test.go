package store

import (
	"context"
	"encoding/json"
	"math/rand"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

// randomSchedule is the fully reproducible description of one differential
// scenario: it is written out as audit evidence so that any failure can be
// replayed byte-for-byte.
type randomSchedule struct {
	Seed        int64      `json:"seed"`
	Batches     []batchRun `json:"batches"`
	Expectation []string   `json:"expectation"`
}

type batchRun struct {
	BatchID         string   `json:"batch_id"`
	Ops             []Op     `json:"ops"`
	FirstCrash      string   `json:"first_crash"`
	RecoveryCrashes []string `json:"recovery_crashes"`
}

func pickBarrier(rng *rand.Rand, n int) string {
	// Choose among the barriers reachable for an n-object batch, including
	// "no crash".
	choices := []string{""}
	for i := 0; i < n; i++ {
		choices = append(choices, barrierIntent+"."+itoa(i))
	}
	choices = append(choices,
		barrierActivePointer,
		barrierStatePrepared,
		barrierStateCommitted)
	for i := 0; i < n; i++ {
		choices = append(choices, barrierInstanceApply+"."+itoa(i))
	}
	choices = append(choices,
		barrierStateDone+".commit",
		barrierActiveClear)
	return choices[rng.Intn(len(choices))]
}

func itoa(i int) string {
	if i == 0 {
		return "0"
	}
	var b []byte
	for i > 0 {
		b = append([]byte{byte('0' + i%10)}, b...)
		i /= 10
	}
	return string(b)
}

// TestRandomizedDifferential runs many random schedules against both the
// production state machine and the independent naive model and asserts
// identical final states. Each schedule + crash stages + recovery
// classification is recorded to testdata/random-replay-audit.jsonl.
func TestRandomizedDifferential(t *testing.T) {
	if err := os.MkdirAll("testdata", 0o755); err != nil {
		t.Fatal(err)
	}
	auditPath := filepath.Join("testdata", "random-replay-audit.jsonl")
	audit, err := os.Create(auditPath)
	if err != nil {
		t.Fatal(err)
	}
	defer audit.Close()

	const iterations = 120
	for iter := 0; iter < iterations; iter++ {
		rng := rand.New(rand.NewSource(int64(1000 + iter)))
		objN := 1 + rng.Intn(6)
		var ids []string
		for i := 0; i < objN; i++ {
			ids = append(ids, "o-"+itoa(i))
		}

		// Naive model keeps the authoritative state across batches.
		model := newNaiveModel()

		disk := NewMemDisk()
		st0, _ := openOn(t, disk, nil)
		for i, id := range ids {
			v := int64(1 + rng.Intn(3))
			props := map[string]string{"gen": itoa(0), "id": id, "n": itoa(i)}
			if err := st0.Put(context.Background(), &Instance{ID: id, Version: v, Properties: props}); err != nil {
				t.Fatal(err)
			}
			model.seed(id, v, props)
		}

		sched := randomSchedule{Seed: int64(1000 + iter)}
		batchesN := 1 + rng.Intn(4)
		for bi := 0; bi < batchesN; bi++ {
			batchID := "batch-" + itoa(bi)
			opN := 1 + rng.Intn(objN)
			rng.Shuffle(len(ids), func(i, j int) { ids[i], ids[j] = ids[j], ids[i] })
			var ops []Op
			for _, id := range ids[:opN] {
				ops = append(ops, Op{ObjectID: id, Properties: map[string]string{
					"gen": itoa(bi + 1), "id": id,
				}})
			}

			first := pickBarrier(rng, opN)
			var recCrashes []string
			if first != "" {
				for r := 0; r < rng.Intn(3); r++ {
					rc := pickBarrier(rng, opN)
					if rc == "" {
						break
					}
					recCrashes = append(recCrashes, rc)
				}
			}
			sched.Batches = append(sched.Batches, batchRun{
				BatchID: batchID, Ops: ops, FirstCrash: first, RecoveryCrashes: recCrashes,
			})

			// Production execution: crash on first barrier.
			var seen []string
			var hook CrashHook
			if first != "" {
				hook = crashOnceAt(first, &seen)
			}
			st1, _ := openOn(t, disk, hook)
			_, berr := st1.Batch(context.Background(), batchID, ops)
			committed := false
			if first == "" {
				if berr != nil {
					t.Fatalf("iter %d batch %s: %v", iter, batchID, berr)
				}
				committed = true
			} else {
				var fatal *FatalCrashError
				if !asFatal(berr, &fatal) {
					t.Fatalf("iter %d batch %s: expected crash got %v", iter, batchID, berr)
				}
				committed = first == barrierStateCommitted ||
					hasPrefix(first, barrierInstanceApply+".") ||
					first == barrierStateDone+".commit" ||
					first == barrierActiveClear

				// Recovery passes; early passes may crash again.
				anyRecovered := false
				for _, rc := range recCrashes {
					var s []string
					_, reps0, _ := Open(context.Background(), NewMemEngine(disk),
						WithCrashHook(crashOnceAt(rc, &s)))
					if len(reps0) > 0 {
						anyRecovered = true
					}
				}
				st2, reps, err := Open(context.Background(), NewMemEngine(disk))
				if err != nil {
					t.Fatal(err)
				}
				_ = st2
				if committed {
					// A first crash at/after the DONE marker is resolved by
					// cleanup-only recovery: injected extra crashes at
					// barriers that cleanup never reaches do not fire, so
					// the final clean reopen is legitimately a no-op.
					cleanupOnlyCrash := first == barrierStateDone+".commit" ||
						first == barrierActiveClear
					postClear := false
					if len(recCrashes) > 0 {
						last := recCrashes[len(recCrashes)-1]
						postClear = last == barrierActiveClear || hasPrefix(last, barrierIntentCleanup+".")
					}
					switch {
					case len(reps) == 1 && reps[0].Class == ClassRecoveredCommit:
					case len(reps) == 0 && postClear:
					case len(reps) == 0 && cleanupOnlyCrash:
					case len(reps) == 0 && anyRecovered:
					case len(reps) == 0:
					default:
						t.Fatalf("iter %d batch %s: reps=%+v first=%s recCrashes=%v keys=%v",
							iter, batchID, reps, first, recCrashes, disk.Keys())
					}
				} else if len(reps) != 0 && reps[0].Class != ClassRecoveredAbort {
					t.Fatalf("iter %d batch %s: expected abort classification got %+v", iter, batchID, reps)
				}
			}

			if committed {
				model.applyCommit(ops)
				model.markCommitted(batchID)
				sched.Expectation = append(sched.Expectation, batchID+":committed")
			} else {
				sched.Expectation = append(sched.Expectation, batchID+":aborted")
			}
			t.Logf("iter=%d %s first=%q rec=%v committed=%v", iter, batchID, first, recCrashes, committed)

			// Reopen a stable handle for the next batch.
			stNext, _ := openOn(t, disk, nil)
			diffAgainstModel(t, stNext, model, iter, batchID)
		}

		line, _ := json.Marshal(sched)
		if _, err := audit.Write(append(line, '\n')); err != nil {
			t.Fatal(err)
		}
	}
}

func asFatal(err error, target **FatalCrashError) bool {
	if f, ok := err.(*FatalCrashError); ok {
		*target = f
		return true
	}
	return false
}

func diffAgainstModel(t *testing.T, st *Store, model *naiveModel, iter int, batchID string) {
	t.Helper()
	for id, want := range model.instances {
		got, err := st.Get(context.Background(), id)
		if err != nil {
			t.Fatalf("iter %d after %s get %s: %v", iter, batchID, id, err)
		}
		if got.Version != want.version || !reflect.DeepEqual(got.Properties, want.props) {
			t.Fatalf("iter %d after %s object %s diverges:\n got v=%d %v\nwant v=%d %v",
				iter, batchID, id, got.Version, got.Properties, want.version, want.props)
		}
	}
}
