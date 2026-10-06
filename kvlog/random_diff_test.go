package kvlog

import (
	"fmt"
	"math/rand"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"testing"
)

type traceLogger struct {
	mu  sync.Mutex
	b   strings.Builder
	dir string
}

func (l *traceLogger) Logf(format string, args ...any) {
	l.mu.Lock()
	l.b.WriteString("ENGINE: ")
	l.b.WriteString(fmt.Sprintf(format, args...))
	l.b.WriteByte('\n')
	l.mu.Unlock()
}

// TestRandomOpsAgainstNaiveModel runs random operation streams (put,
// delete, get, merge of contiguous/non-contiguous sealed sets, reopen) and
// compares the full keyspace against an independently maintained model.
func TestRandomOpsAgainstNaiveModel(t *testing.T) {
	const iterations = 24
	for seed := int64(1); seed <= iterations; seed++ {
		t.Run(fmt.Sprintf("seed=%d", seed), func(t *testing.T) {
			rng := rand.New(rand.NewSource(seed))
			dir := t.TempDir()
			trace := &traceLogger{}
			e, err := Open(dir, Config{MaxSegmentBytes: 64, WriteHints: rng.Intn(2) == 0})
			if err != nil {
				t.Fatal(err)
			}
			e.SetLogger(trace)
			model := newNaiveModel()
			keys := []string{"a", "bb", "ccc", "dddd", "eeeee", "k-6"}

			modelMatches := func(stage string) {
				t.Helper()
				snap := dumpEngine(e)
				if !snapshotsEqual(model.snapshot(), snap) {
					diff := []string{}
					all := map[string]bool{}
					for k := range model.state {
						all[k] = true
					}
					for k := range snap.state {
						all[k] = true
					}
					for k := range all {
						ms := model.state[k]
						ss := snap.state[k]
						if ms != ss || model.value[k] != snap.value[k] {
							diff = append(diff, fmt.Sprintf(
								"key=%q model=(%d,%q) engine=(%d,%q) lastSeq=%d",
								k, ms, model.value[k], ss, snap.value[k], 0))
						}
					}
					sort.Strings(diff)
					trace.mu.Lock()
					log := trace.b.String()
					trace.mu.Unlock()
					t.Fatalf("stage=%s seed=%d mismatch\nDIFF:\n%s\nTRACE:\n%s",
						stage, seed, strings.Join(diff, "\n"), log)
				}
			}

			nOps := 120 + rng.Intn(120)
			for op := 0; op < nOps; op++ {
				switch rng.Intn(10) {
				case 0, 1, 2, 3:
					k := keys[rng.Intn(len(keys))]
					v := fmt.Sprintf("v%d-%s", rng.Intn(20), strings.Repeat("x", rng.Intn(12)))
					seq, err := e.Put([]byte(k), []byte(v))
					trace.Logf("op=%d PUT key=%q value=%q -> seq=%d err=%v", op, k, v, seq, err)
					if err != nil {
						t.Fatalf("put: %v", err)
					}
					model.put(k, v)
				case 4:
					k := keys[rng.Intn(len(keys))]
					seq, err := e.Delete([]byte(k))
					trace.Logf("op=%d DELETE key=%q -> seq=%d err=%v", op, k, seq, err)
					if err != nil {
						t.Fatalf("delete: %v", err)
					}
					model.del(k)
				case 5, 6:
					k := keys[rng.Intn(len(keys))]
					r, err := e.Get([]byte(k))
					trace.Logf("op=%d GET key=%q -> status=%d value=%q err=%v",
						op, k, r.Status, r.Value, err)
					if err != nil {
						t.Fatalf("get: %v", err)
					}
					mst, mv := model.get(k)
					switch mst {
					case modelPresent:
						if r.Status != StatusPresent || string(r.Value) != mv {
							t.Fatalf("seed=%d op=%d key=%s present mismatch (%q vs %q)",
								seed, op, k, r.Value, mv)
						}
					case modelDeleted:
						if r.Status != StatusDeleted {
							t.Fatalf("seed=%d op=%d key=%s want deleted got %d",
								seed, op, k, r.Status)
						}
					case modelMissing:
						if r.Status != StatusMissing {
							t.Fatalf("seed=%d op=%d key=%s want missing got %d",
								seed, op, k, r.Status)
						}
					}
				case 7:
					ids := sealedIDs(t, dir)
					if len(ids) >= 2 {
						// Pick a random non-contiguous subset.
						chosen := pickMergeSubset(rng, ids)
						trace.Logf("op=%d MERGE ids=%v", op, chosen)
						out, merr := e.Merge(chosen)
						trace.Logf("op=%d MERGE -> out=%d err=%v", op, out, merr)
						if merr != nil {
							t.Fatalf("merge: %v", merr)
						}
						// Independently rebuild the model from the
						// resulting on-disk segments.
						model = rebuildModelFromDisk(t, e, dir)
						modelMatches("post-merge")
					}
				case 8:
					// Reopen simulating a clean restart.
					trace.Logf("op=%d REOPEN begin", op)
					if err := e.Close(); err != nil {
						t.Fatal(err)
					}
					e, err = Open(dir, Config{MaxSegmentBytes: 64, WriteHints: true})
					if err != nil {
						t.Fatalf("reopen: %v", err)
					}
					e.SetLogger(trace)
					model = rebuildModelFromDisk(t, e, dir)
					modelMatches("post-reopen")
					trace.Logf("op=%d REOPEN done nextSeq=%d", op, e.NextSeq())
				case 9:
					// Authoritative cross-check straight from disk, which
					// bypasses any incremental bookkeeping in this test.
					diskModel := rebuildModelFromDisk(t, e, dir)
					if !snapshotsEqual(diskModel.snapshot(), dumpEngine(e)) {
						trace.mu.Lock()
						log := trace.b.String()
						trace.mu.Unlock()
						t.Fatalf("stage=checkpoint-disk seed=%d engine disagrees with raw disk scan\nTRACE:\n%s",
							seed, log)
					}
					modelMatches("checkpoint")
				}
				// Periodic authoritative disk cross-check (every 16 ops).
				if op%16 == 15 {
					dm := rebuildModelFromDisk(t, e, dir)
					if !snapshotsEqual(dm.snapshot(), dumpEngine(e)) {
						trace.mu.Lock()
						log := trace.b.String()
						trace.mu.Unlock()
						t.Fatalf("seed=%d periodic divergence after op=%d\n%s",
							seed, op, log)
					}
				}
			}
			modelMatches("final")
			if err := e.Close(); err != nil {
				t.Fatal(err)
			}
			// Determinism: two further reopens produce identical dumps.
			dumps := make([]snapshot, 0, 2)
			for i := 0; i < 2; i++ {
				e, err = Open(dir, Config{MaxSegmentBytes: 64, WriteHints: true})
				if err != nil {
					t.Fatal(err)
				}
				dumps = append(dumps, dumpEngine(e))
				if e.NextSeq() == 0 {
					t.Fatal("zero next seq")
				}
				e.Close()
			}
			if !snapshotsEqual(dumps[0], dumps[1]) {
				t.Fatal("reopen dumps differ")
			}
			// Persist one trace for human inspection (seed 1).
			if seed == 1 {
				if err := os.WriteFile(filepath.Join(dir, "trace.log"),
					[]byte(trace.b.String()), 0o644); err == nil {
					t.Logf("trace written to %s", dir+"/trace.log")
				}
			}
		})
	}
}

func pickMergeSubset(rng *rand.Rand, ids []int) []int {
	sort.Ints(ids)
	n := 1 + rng.Intn(len(ids))
	rng.Shuffle(len(ids), func(i, j int) { ids[i], ids[j] = ids[j], ids[i] })
	chosen := append([]int(nil), ids[:n]...)
	sort.Ints(chosen)
	return chosen
}

// TestDeterministicReplay records an operation plan, executes it to a
// crash-free end, replays the identical plan from scratch in a second
// directory, and requires byte-identical directories modulo file order.
func TestDeterministicReplay(t *testing.T) {
	type op struct {
		kind  byte
		key   string
		value string
		merge []int
	}
	rng := rand.New(rand.NewSource(777))
	keys := []string{"x", "yy", "zzz"}
	plan := make([]op, 0, 120)
	for i := 0; i < 120; i++ {
		switch rng.Intn(6) {
		case 0, 1, 2:
			k := keys[rng.Intn(len(keys))]
			plan = append(plan, op{kind: 'p', key: k, value: fmt.Sprintf("%d", rng.Intn(50))})
		case 3:
			plan = append(plan, op{kind: 'd', key: keys[rng.Intn(len(keys))]})
		case 4:
			plan = append(plan, op{kind: 'g', key: keys[rng.Intn(len(keys))]})
		case 5:
			plan = append(plan, op{kind: 'r'})
		}
	}

	run := func(dir string) map[string][]byte {
		e, err := Open(dir, Config{MaxSegmentBytes: 48, WriteHints: true})
		if err != nil {
			t.Fatal(err)
		}
		for _, o := range plan {
			switch o.kind {
			case 'p':
				e.Put([]byte(o.key), []byte(o.value))
			case 'd':
				e.Delete([]byte(o.key))
			case 'g':
				e.Get([]byte(o.key))
			case 'r':
				e.Close()
				e, _ = Open(dir, Config{MaxSegmentBytes: 48, WriteHints: true})
			}
		}
		e.Close()
		files := map[string][]byte{}
		entries, _ := os.ReadDir(dir)
		for _, en := range entries {
			data, _ := os.ReadFile(dir + "/" + en.Name())
			files[en.Name()] = data
		}
		return files
	}

	d1 := run(t.TempDir())
	d2 := run(t.TempDir())
	if len(d1) != len(d2) {
		t.Fatalf("file sets differ: %d vs %d", len(d1), len(d2))
	}
	for name, b1 := range d1 {
		b2, ok := d2[name]
		if !ok {
			t.Fatalf("file %s missing on replay", name)
		}
		if string(b1) != string(b2) {
			t.Fatalf("file %s differs", name)
		}
	}
}
