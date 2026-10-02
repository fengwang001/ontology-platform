package ontology

import (
	"math/rand"
	"sort"
	"testing"
)

type naiveRow struct {
	key string
	ts  int64
	val int64
	seq int64
}

type naiveAggregator struct {
	r, al, cap int64
	wm, seq    int64
	buffer     []naiveRow
	retained   []naiveRow
	late       int64
	supplement int64
}

func newNaive(r, al, cap int64) *naiveAggregator {
	return &naiveAggregator{r: r, al: al, cap: cap, wm: -1}
}

func (n *naiveAggregator) frame(key string, ts int64, selfVal int64) Output {
	sum := selfVal
	cnt := int64(1)
	max := selfVal
	for _, row := range n.retained {
		if row.key == key && ts-n.r <= row.ts && row.ts <= ts {
			sum += row.val
			cnt++
			if row.val > max {
				max = row.val
			}
		}
	}
	return Output{Key: []byte(key), TS: ts, Val: selfVal, Sum: sum, Cnt: cnt, Max: max}
}

func (n *naiveAggregator) retainedFrame(key string, ts int64, selfVal int64) Output {
	sum := int64(0)
	cnt := int64(0)
	max := int64(0)
	hasMax := false
	for _, row := range n.retained {
		if row.key == key && ts-n.r <= row.ts && row.ts <= ts {
			sum += row.val
			cnt++
			if !hasMax || row.val > max {
				max = row.val
				hasMax = true
			}
		}
	}
	return Output{Key: []byte(key), TS: ts, Val: selfVal, Sum: sum, Cnt: cnt, Max: max}
}

func (n *naiveAggregator) insert(key string, ts, val int64) (string, Output) {
	if ts <= n.wm-n.al {
		n.late++
		return "late", Output{}
	}
	if ts <= n.wm {
		n.supplement++
		out := n.frame(key, ts, val)
		n.retained = append(n.retained, naiveRow{key: key, ts: ts, val: val, seq: -1})
		return "supplement", out
	}
	if int64(len(n.buffer)) >= n.cap {
		return "rejected", Output{}
	}
	n.seq++
	n.buffer = append(n.buffer, naiveRow{key: key, ts: ts, val: val, seq: n.seq})
	return "buffered", Output{}
}

func (n *naiveAggregator) advance(w int64) []Output {
	var release []naiveRow
	remaining := n.buffer[:0]
	for _, row := range n.buffer {
		if row.ts <= w {
			release = append(release, row)
		} else {
			remaining = append(remaining, row)
		}
	}
	n.buffer = remaining
	sort.Slice(release, func(i, j int) bool {
		if release[i].ts != release[j].ts {
			return release[i].ts < release[j].ts
		}
		return release[i].seq < release[j].seq
	})

	seen := make(map[string]map[int64]int)
	var groups []Output
	n.retained = append(n.retained, release...)
	for _, row := range release {
		byTS := seen[row.key]
		if byTS == nil {
			byTS = make(map[int64]int)
			seen[row.key] = byTS
		}
		if _, ok := byTS[row.ts]; ok {
			continue
		}
		byTS[row.ts] = len(groups)
		out := n.retainedFrame(row.key, row.ts, row.val)
		groups = append(groups, out)
	}

	outputs := make([]Output, len(release))
	for i, row := range release {
		byTS := seen[row.key]
		out := groups[byTS[row.ts]]
		out.Val = row.val
		outputs[i] = out
	}
	n.wm = w
	kept := n.retained[:0]
	cutoff := w - n.r - n.al
	for _, row := range n.retained {
		if row.ts > cutoff {
			kept = append(kept, row)
		}
	}
	n.retained = kept
	return outputs
}

type simulatedOp struct {
	kind    int
	key     string
	ts, val int64
	w       int64
}

func TestRandomNaiveComparison(t *testing.T) {
	rng := rand.New(rand.NewSource(20261002))
	for trial := 0; trial < 2000; trial++ {
		r := int64(rng.Intn(5))
		al := int64(rng.Intn(4))
		capacity := int64(1 + rng.Intn(5))
		actual, err := NewRangeAggregator(r, al, capacity)
		if err != nil {
			t.Fatal(err)
		}
		naive := newNaive(r, al, capacity)
		t.Logf("trial %d R=%d AL=%d Cap=%d", trial, r, al, capacity)

		ops := 30 + rng.Intn(50)
		for i := 0; i < ops; i++ {
			op := simulatedOp{kind: rng.Intn(5), key: string(rune('a' + rng.Intn(4))), ts: int64(rng.Intn(12)), val: int64(rng.Intn(11) - 5), w: int64(rng.Intn(13))}
			if op.kind < 4 {
				row := Row{Key: []byte(op.key), TS: op.ts, Val: op.val}
				gotOut, gotStatus, gotErr := actual.Insert(row)
				wantStatus, wantOut := naive.insert(op.key, op.ts, op.val)
				t.Logf("insert key=%s ts=%d val=%d => status=%s out=%+v", op.key, op.ts, op.val, gotStatus, gotOut)
				if gotErr != nil {
					gotStatus = "rejected"
				}
				if gotStatus != wantStatus {
					t.Fatalf("trial %d insert status=%s want %s op=%+v", trial, gotStatus, wantStatus, op)
				}
				if wantStatus == "supplement" {
					assertOutputs(t, []Output{gotOut}, []Output{wantOut})
				}
			} else {
				t.Logf("advance w=%d", op.w)
				gotOutputs, gotErr := actual.Advance(op.w)
				if op.w < naive.wm {
					if gotErr != ErrInvalidArgument {
						t.Fatalf("trial %d regression err=%v", trial, gotErr)
					}
					t.Logf("advance rejected because %d < wm=%d", op.w, naive.wm)
					continue
				}
				if gotErr != nil {
					t.Fatal(gotErr)
				}
				wantOutputs := naive.advance(op.w)
				assertOutputs(t, gotOutputs, wantOutputs)
				if actual.Retained() != int64(len(naive.retained)) {
					t.Fatalf("trial %d retained=%d want %d", trial, actual.Retained(), len(naive.retained))
				}
				if actual.wm != naive.wm || actual.LateDropped() != naive.late || actual.Supplemented() != naive.supplement {
					t.Fatalf("counters actual=(wm=%d late=%d supplement=%d) naive=(wm=%d late=%d supplement=%d)", actual.wm, actual.LateDropped(), actual.Supplemented(), naive.wm, naive.late, naive.supplement)
				}
			}
		}
		if actual.frameWork > 4*actual.released {
			t.Fatalf("frameWork=%d released=%d", actual.frameWork, actual.released)
		}
	}
}
