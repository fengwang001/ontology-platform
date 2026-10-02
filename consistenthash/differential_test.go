package consistenthash_test

import (
	"errors"
	"fmt"
	"math/rand"
	"strings"
	"testing"

	"ontology/consistenthash"
)

// errCode maps either a package error or a model error to a shared category.
func errCode(err error) string {
	if err == nil {
		return ""
	}
	var ne naiveErr
	if errors.As(err, &ne) {
		return string(ne)
	}
	switch {
	case errors.Is(err, consistenthash.ErrInvalidKey):
		return string(errNaiveInvalidKey)
	case errors.Is(err, consistenthash.ErrInvalidNode):
		return string(errNaiveInvalidNode)
	case errors.Is(err, consistenthash.ErrNoNode):
		return string(errNaiveNoNode)
	case errors.Is(err, consistenthash.ErrKeyExists):
		return string(errNaiveKeyExists)
	case errors.Is(err, consistenthash.ErrKeyNotFound):
		return string(errNaiveKeyNotFound)
	case errors.Is(err, consistenthash.ErrNodeExists):
		return string(errNaiveNodeExists)
	case errors.Is(err, consistenthash.ErrPointConflict):
		return string(errNaivePointConflict)
	case errors.Is(err, consistenthash.ErrNodeNotFound):
		return string(errNaiveNodeNotFound)
	case errors.Is(err, consistenthash.ErrLastNodeBusy):
		return string(errNaiveLastBusy)
	case errors.Is(err, consistenthash.ErrInvalidLimit):
		return string(errNaiveInvalidLimit)
	default:
		return "unmapped:" + err.Error()
	}
}

type op struct {
	kind   string
	id     int64
	points []uint64
	key    string
	pos    uint64
	limit  int
}

// TestNaiveDifferential2000 replays 2000 random node add/remove, put/delete
// and rebalance streams on both the real ring and the naive step-by-step
// model, comparing errors, owners, loads, seqs and migration lists.
func TestNaiveDifferential2000(t *testing.T) {
	rng := rand.New(rand.NewSource(20261002))

	for iter := 0; iter < 2000; iter++ {
		var logBuf strings.Builder
		logf := func(format string, args ...any) {
			fmt.Fprintf(&logBuf, format+"\n", args...)
		}

		cnum := int64(1 + rng.Intn(6))
		cden := int64(1 + rng.Intn(int(cnum)))
		real, err := consistenthash.New(cnum, cden)
		if err != nil {
			t.Fatalf("iter %d New(%d,%d): %v", iter, cnum, cden, err)
		}
		model := newNaiveModel(cnum, cden, logf)
		logf("=== iter %d  c=%d/%d ===", iter, cnum, cden)

		// Available node ids and currently-used point positions, so that a
		// good fraction of generated ops are valid (with deliberate invalid
		// ops mixed in to exercise rejection paths).
		registered := map[int64]bool{}
		usedPoints := map[uint64]bool{}
		aliveKeys := []string{}
		nextNodeID := int64(1)

		steps := 30 + rng.Intn(60)
		for step := 0; step < steps; step++ {
			var o op
			roll := rng.Intn(100)
			switch {
			case roll < 28: // AddNode
				o.kind = "add"
				if rng.Intn(10) == 0 {
					// Deliberately malformed arguments.
					switch rng.Intn(3) {
					case 0:
						o.id = 0
						o.points = []uint64{1}
					case 1:
						o.id = nextNodeID
						o.points = nil
					default:
						o.id = nextNodeID
						o.points = []uint64{5, 5}
					}
				} else {
					o.id = nextNodeID
					nextNodeID++
					npts := 1 + rng.Intn(4)
					o.points = uniquePoints(rng, npts, usedPoints)
				}
				if rng.Intn(6) == 0 && len(registered) > 0 {
					for id := range registered {
						o.id = id
						break
					}
				}
				gotErr := errCode(real.AddNode(o.id, o.points))
				wantErr := errCode(model.addNode(o.id, o.points))
				logf("step %d AddNode(id=%d points=%v) -> got=%q want=%q",
					step, o.id, o.points, gotErr, wantErr)
				if gotErr != wantErr {
					t.Fatalf("iter %d step %d AddNode error mismatch:\n%s", iter, step, logBuf.String())
				}
				if gotErr == "" {
					registered[o.id] = true
					for _, p := range o.points {
						usedPoints[p] = true
					}
				}
				continue

			case roll < 50: // Put
				o.kind = "put"
				o.key = fmt.Sprintf("k%d-%d", iter, step)
				if rng.Intn(12) == 0 && len(aliveKeys) > 0 {
					o.key = aliveKeys[rng.Intn(len(aliveKeys))] // duplicate
				}
				o.pos = rng.Uint64()
				gotErr := errCode(real.Put(o.key, o.pos))
				wantErr := errCode(model.put(o.key, o.pos))
				logf("step %d Put(key=%q pos=%d) -> got=%q want=%q",
					step, o.key, o.pos, gotErr, wantErr)
				if gotErr != wantErr {
					t.Fatalf("iter %d step %d Put error mismatch:\n%s", iter, step, logBuf.String())
				}
				if gotErr == "" {
					aliveKeys = append(aliveKeys, o.key)
				}
				continue

			case roll < 62: // Delete
				o.kind = "delete"
				if len(aliveKeys) > 0 {
					i := rng.Intn(len(aliveKeys))
					o.key = aliveKeys[i]
					aliveKeys = append(aliveKeys[:i], aliveKeys[i+1:]...)
				} else {
					o.key = "ghost"
				}
				gotErr := errCode(real.Delete(o.key))
				wantErr := errCode(model.del(o.key))
				logf("step %d Delete(key=%q) -> got=%q want=%q",
					step, o.key, gotErr, wantErr)
				if gotErr != wantErr {
					t.Fatalf("iter %d step %d Delete error mismatch:\n%s", iter, step, logBuf.String())
				}
				continue

			case roll < 74: // RemoveNode
				o.kind = "remove"
				if len(registered) > 0 {
					ids := make([]int64, 0, len(registered))
					for id := range registered {
						ids = append(ids, id)
					}
					o.id = ids[rng.Intn(len(ids))]
				} else {
					o.id = 1
				}
				gotErr := errCode(real.RemoveNode(o.id))
				wantErr := errCode(model.removeNode(o.id))
				logf("step %d RemoveNode(id=%d) -> got=%q want=%q",
					step, o.id, gotErr, wantErr)
				if gotErr != wantErr {
					t.Fatalf("iter %d step %d RemoveNode error mismatch:\n%s", iter, step, logBuf.String())
				}
				if gotErr == "" {
					delete(registered, o.id)
					for p := range usedPoints {
						_ = p
					}
					// Rebuild usedPoints from model after removal.
					usedPoints = map[uint64]bool{}
					for _, pe := range model.points {
						usedPoints[pe.pos] = true
					}
				}
				continue

			default: // Rebalance
				o.kind = "rebalance"
				o.limit = rng.Intn(5)
				if rng.Intn(15) == 0 {
					o.limit = 1_000_000_001 // invalid
				}
				gm, ge, gerr := real.Rebalance(o.limit)
				wm, we, werr := model.rebalance(o.limit)
				logf("step %d Rebalance(limit=%d) -> gotErr=%q wantErr=%q gotMigs=%d wantMigs=%d gotExcess=%d wantExcess=%d",
					step, o.limit, errCode(gerr), errCode(werr), len(gm), len(wm), ge, we)
				if errCode(gerr) != errCode(werr) {
					t.Fatalf("iter %d step %d Rebalance error mismatch:\n%s", iter, step, logBuf.String())
				}
				if gerr == nil {
					if ge != we || len(gm) != len(wm) {
						t.Fatalf("iter %d step %d Rebalance result mismatch got=(%d migs,excess=%d) want=(%d,%d):\n%s",
							iter, step, len(gm), ge, len(wm), we, logBuf.String())
					}
					for i := range gm {
						if gm[i].Key != wm[i].key || gm[i].From != wm[i].from || gm[i].To != wm[i].to {
							t.Fatalf("iter %d step %d migration[%d] mismatch got=(%s,%d,%d) want=(%s,%d,%d):\n%s",
								iter, step, i,
								gm[i].Key, gm[i].From, gm[i].To,
								wm[i].key, wm[i].from, wm[i].to, logBuf.String())
						}
					}
				}
				continue
			}
		}

		// Final cross-check: every key owner and every node load.
		if err := diffState(t, iter, real, model, aliveKeys, &logBuf); err != nil {
			t.Fatalf("iter %d state mismatch: %v\n%s", iter, err, logBuf.String())
		}
		// Emit a compact verdict line plus the full input/output trace under -v.
		logf("iter %d: OK (%d steps, final loads=%v)", iter, steps, real.Load())
		t.Logf("\n%s", logBuf.String())
	}
}

func diffState(t *testing.T, iter int, real *consistenthash.Ring, model *naiveModel, keys []string, _ *strings.Builder) error {
	t.Helper()
	realLoad := real.Load()
	sum := 0
	for id := range model.nodes {
		wl := model.load(id)
		if realLoad[id] != wl {
			return fmt.Errorf("node %d load got=%d want=%d (real=%v)", id, realLoad[id], wl, realLoad)
		}
		sum += wl
	}
	for id, l := range realLoad {
		if model.nodes[id] == nil && l != 0 {
			return fmt.Errorf("real has load %d on removed node %d", l, id)
		}
	}
	for _, k := range keys {
		gid, gerr := real.Lookup(k)
		wid, werr := model.lookup(k)
		if errCode(gerr) != errCode(werr) {
			return fmt.Errorf("key %q lookup error %v vs %v", k, gerr, werr)
		}
		if gerr == nil && gid != wid {
			return fmt.Errorf("key %q owner got=%d want=%d", k, gid, wid)
		}
	}
	if sum != len(model.keys) {
		return fmt.Errorf("model load sum %d != key count %d", sum, len(model.keys))
	}
	return nil
}

// uniquePoints draws n points not currently used anywhere, occasionally
// wrapping the small address space to create wrap-around scenarios.
func uniquePoints(rng *rand.Rand, n int, used map[uint64]bool) []uint64 {
	out := make([]uint64, 0, n)
	local := map[uint64]bool{}
	span := uint64(40)
	for len(out) < n {
		var p uint64
		if rng.Intn(3) == 0 {
			p = ^uint64(0) - uint64(rng.Intn(10)) // near the top -> wrap
		} else {
			p = uint64(rng.Int63n(int64(span))) + 1
		}
		if used[p] || local[p] {
			// Expand search space on collision instead of looping forever.
			p = rng.Uint64()
			if used[p] || local[p] {
				continue
			}
		}
		local[p] = true
		out = append(out, p)
	}
	return out
}
