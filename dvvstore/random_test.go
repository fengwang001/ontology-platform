package dvvstore

import (
	"math/rand"
	"reflect"
	"testing"
)

const (
	randomReplicas = 3
	randomKeys     = 2
	randomSteps    = 300
	randomCap      = 3
)

var randomNodes = []string{"A", "B", "C"}
var randomValues = []string{"", "v1", "v2", "w"}

func randomKey(rng *rand.Rand) string {
	return "k" + string(rune('1'+rng.Intn(randomKeys)))
}

// randomContext builds a valid context from the reference replica's seen:
// each counter is uniform in [0, seen], so behind/ahead/full contexts all
// appear while ahead contexts are never generated.
func randomContext(rng *rand.Rand, refs []*refStore, replica int, key string) map[string]int64 {
	ctx := map[string]int64{}
	st := refs[replica].keys[key]
	for _, node := range randomNodes {
		var seen int64
		if st != nil {
			seen = st.seen[node]
		}
		if seen > 0 {
			ctx[node] = rng.Int63n(seen + 1)
		}
	}
	return ctx
}

func compareReplica(t *testing.T, real []*Store, refs []*refStore, step int) {
	t.Helper()
	keySet := map[string]bool{}
	for _, r := range refs {
		for key := range r.keySet() {
			keySet[key] = true
		}
	}
	for i := range real {
		for key := range keySet {
			got := real[i].Get(key)
			want := refs[i].snapshot(key)
			if !reflect.DeepEqual(got, want) {
				t.Fatalf("step %d replica %d key %q mismatch:\n got  %#v\n want %#v", step, i, key, got, want)
			}
		}
		// Keys absent from the reference must also be absent in reality.
		if got := real[i].Get("definitely-missing"); len(got.Siblings) != 0 || len(got.Context) != 0 {
			t.Fatalf("step %d replica %d unexpectedly contains missing key", step, i)
		}
	}
}

// TestRandomSchedulesAgainstNaiveModel drives three copies of the real Store
// and three copies of the naive reference model with identical schedules:
// random Puts (with random valid contexts) and random pairwise Merges.
func TestRandomSchedulesAgainstNaiveModel(t *testing.T) {
	for seed := int64(1); seed <= 20; seed++ {
		t.Run("seed", func(t *testing.T) {
			rng := rand.New(rand.NewSource(seed))
			reals := make([]*Store, randomReplicas)
			refs := make([]*refStore, randomReplicas)
			for i := range reals {
				reals[i] = mustNew(t, randomCap)
				refs[i] = newRefStore(randomCap)
			}

			for step := 0; step < randomSteps; step++ {
				if rng.Intn(2) == 0 {
					replica := rng.Intn(randomReplicas)
					key := randomKey(rng)
					id := randomNodes[rng.Intn(len(randomNodes))]
					ctx := randomContext(rng, refs, replica, key)
					value := randomValues[rng.Intn(len(randomValues))]

					gotErr := reals[replica].Put(key, id, ctx, value)
					wantErr := refs[replica].put(key, id, ctx, value)
					if !reflect.DeepEqual(errorString(gotErr), errorString(wantErr)) {
						t.Fatalf("seed %d step %d Put errors differ: got %v want %v; input key=%q id=%q ctx=%v value=%q",
							seed, step, gotErr, wantErr, key, id, ctx, value)
					}
					t.Logf("seed %d step %d input: Put(replica=%d,key=%q,id=%q,ctx=%v,value=%q) output err=%v judgment: real error must match naive model",
						seed, step, replica, key, id, ctx, value, gotErr)
				} else {
					from := rng.Intn(randomReplicas)
					to := rng.Intn(randomReplicas)
					reals[to].Merge(reals[from])
					refs[to].merge(refs[from])
					t.Logf("seed %d step %d input: Merge(%d <- %d) judgment: reconcile dots against other side's same-dot presence and seen vector",
						seed, step, to, from)
				}
				compareReplica(t, reals, refs, step)
			}

			t.Logf("seed %d final: all %d replicas matched naive model after every step", seed, randomSteps)
			assertMergeLaws(t, reals, refs, seed)
		})
	}
}

func errorString(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}

// fullState is the field-by-field observable state of a whole store.
func fullState(s *Store) map[string]Snapshot {
	out := map[string]Snapshot{}
	for _, key := range collectKeys(s) {
		out[key] = s.Get(key)
	}
	return out
}

func collectKeys(s *Store) []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	keys := make([]string, 0, len(s.keys))
	for key := range s.keys {
		keys = append(keys, key)
	}
	return keys
}

// cloneRealStore deep-copies internal state; test-only.
func cloneRealStore(t *testing.T, src *Store) *Store {
	t.Helper()
	dst := mustNew(t, src.cap)
	src.mu.Lock()
	defer src.mu.Unlock()
	for key, st := range src.keys {
		dst.keys[key] = &keyState{
			siblings: cloneSiblings(st.siblings),
			seen:     cloneSeen(st.seen),
		}
	}
	return dst
}

func cloneRefStore(src *refStore) *refStore {
	dst := newRefStore(src.cap)
	for key, st := range src.keys {
		dst.keys[key] = &refKeyState{
			siblings: append([]refSibling(nil), st.siblings...),
			seen:     cloneRefSeen(st.seen),
		}
	}
	return dst
}

func refFullState(r *refStore) map[string]Snapshot {
	out := map[string]Snapshot{}
	for key := range r.keySet() {
		out[key] = r.snapshot(key)
	}
	return out
}

// assertMergeLaws verifies commutativity, associativity, idempotence and
// order/repetition independence for the converged three-replica state, and
// that the real result equals the naive model result field by field.
func assertMergeLaws(t *testing.T, reals []*Store, refs []*refStore, seed int64) {
	t.Helper()

	// Baseline: fold 0 <- 1 <- 2 in a fixed order, repeated a second time
	// (idempotence).
	base := cloneRealStore(t, reals[0])
	base.Merge(reals[1])
	base.Merge(reals[2])
	base.Merge(reals[1])
	base.Merge(reals[2])

	refBase := cloneRefStore(refs[0])
	refBase.merge(refs[1])
	refBase.merge(refs[2])
	refBase.merge(refs[1])
	refBase.merge(refs[2])

	want := refFullState(refBase)
	if got := fullState(base); !reflect.DeepEqual(got, want) {
		t.Fatalf("seed %d: fixed-order merge differs from naive model\n got %#v\n want %#v", seed, got, want)
	}

	// Many random permutations, including self-merges and repeated merges,
	// must all reach the identical field-by-field state.
	rng := rand.New(rand.NewSource(seed*1000 + 7))
	for trial := 0; trial < 30; trial++ {
		copies := [randomReplicas]*Store{}
		for i := range reals {
			copies[i] = cloneRealStore(t, reals[i])
		}
		order := rng.Perm(randomReplicas)
		acc := cloneRealStore(t, copies[order[0]])
		for _, idx := range order[1:] {
			acc.Merge(copies[idx])
		}
		// Repeated merges (including self-merge of copies) must be idempotent.
		for op := 0; op < 9; op++ {
			acc.Merge(copies[rng.Intn(randomReplicas)])
		}
		if got := fullState(acc); !reflect.DeepEqual(got, want) {
			t.Fatalf("seed %d trial %d: random merge order not order-independent\n got %#v\n want %#v", seed, trial, got, want)
		}
	}
	t.Logf("seed %d: merge commutative/associative/idempotent across random orders and repetitions; matches naive model", seed)
}
