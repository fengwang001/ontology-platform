package intervalindex

import (
	"fmt"
	"math/rand"
	"strings"
	"testing"
)

// errLabel renders the rejection reason in a human-distinguishable way for
// the differential log.
func errLabel(err error) string {
	switch err {
	case nil:
		return "ok"
	case ErrNonPositiveID:
		return "rejected:non-positive-id"
	case ErrEmptyInterval:
		return "rejected:lo>=hi"
	case ErrDuplicateID:
		return "rejected:duplicate-id"
	case ErrIDNotFound:
		return "rejected:id-not-found"
	case ErrInvalidQuery:
		return "rejected:a>=b"
	default:
		return "rejected:unknown"
	}
}

// TestRandomDifferential replays 2000 randomized operations against both the
// augmented index and the naive scan, comparing every result. Each operation
// logs its input, both outputs and the half-open predicate used to judge it.
func TestRandomDifferential(t *testing.T) {
	const total = 2000
	rng := rand.New(rand.NewSource(20261001))

	idx := New()
	ref := newNaive()

	// Live ids let removals hit often; the pool is small so duplicate inserts
	// and half of re-insert attempts also occur naturally.
	live := map[int64]bool{}

	var log strings.Builder
	fmt.Fprintf(&log, "seed=%d operations=%d\n", 20261001, total)

	check := func(step int, detail string, got []int64, gotErr, wantErr error, want []int64) {
		t.Helper()
		if errLabel(gotErr) != errLabel(wantErr) ||
			(wantErr == nil && !equalIDs(got, want)) {
			t.Fatalf("step %d %s: augmented=%v/%s naive=%v/%s\nlog:\n%s",
				step, detail, got, errLabel(gotErr), want, errLabel(wantErr), log.String())
		}
	}

	for step := 1; step <= total; step++ {
		switch rng.Intn(5) {
		case 0, 1: // insert
			var id int64
			if len(live) > 0 && rng.Intn(3) == 0 {
				for candidate := range live {
					id = candidate
					break
				}
			} else {
				id = int64(rng.Intn(60) + 1)
			}
			lo := int64(rng.Intn(40) - 10)
			hi := lo + int64(rng.Intn(8)) // occasionally zero width
			gotErr := idx.Insert(id, lo, hi)
			wantErr := ref.Insert(id, lo, hi)
			fmt.Fprintf(&log, "step=%d INSERT input={id:%d interval:[%d,%d)} output={augmented:%s naive:%s} reason={valid iff id>0 && lo<hi && id-unused}\n",
				step, id, lo, hi, errLabel(gotErr), errLabel(wantErr))
			check(step, fmt.Sprintf("insert id=%d [%d,%d)", id, lo, hi), nil, gotErr, wantErr, nil)
			if gotErr == nil {
				live[id] = true
			}

		case 2: // remove
			var id int64
			if len(live) > 0 && rng.Intn(4) != 0 {
				for candidate := range live {
					id = candidate
					break
				}
			} else {
				id = int64(rng.Intn(80) + 1)
			}
			gotErr := idx.Remove(id)
			wantErr := ref.Remove(id)
			fmt.Fprintf(&log, "step=%d REMOVE input={id:%d} output={augmented:%s naive:%s} reason={valid iff id is present}\n",
				step, id, errLabel(gotErr), errLabel(wantErr))
			check(step, fmt.Sprintf("remove id=%d", id), nil, gotErr, wantErr, nil)
			if gotErr == nil {
				delete(live, id)
			}

		case 3: // stab
			x := int64(rng.Intn(50) - 15)
			got := idx.Stab(x)
			want := ref.Stab(x)
			fmt.Fprintf(&log, "step=%d STAB input={x:%d} output={augmented:%v naive:%v} reason={hit iff lo<=%d && %d<hi}\n",
				step, x, got, want, x, x)
			check(step, fmt.Sprintf("stab x=%d", x), got, nil, nil, want)

		case 4: // overlap
			a := int64(rng.Intn(50) - 15)
			b := a + int64(rng.Intn(10)-2) // can be a>=b
			got, gotErr := idx.Overlap(a, b)
			want, wantErr := ref.Overlap(a, b)
			fmt.Fprintf(&log, "step=%d OVERLAP input={query:[%d,%d)} output={augmented:%v/%s naive:%v/%s} reason={valid iff %d<%d; hit iff lo<%d && %d<hi}\n",
				step, a, b, got, errLabel(gotErr), want, errLabel(wantErr), a, b, b, a)
			check(step, fmt.Sprintf("overlap [%d,%d)", a, b), got, gotErr, wantErr, want)
		}

		if idx.Len() != ref.Len() {
			t.Fatalf("step %d length divergence: augmented=%d naive=%d", step, idx.Len(), ref.Len())
		}
	}

	// Emit the transcript; it is attached to the test output and visible with
	// `go test -v`.
	t.Logf("differential transcript (%d operations):\n%s", total, log.String())

	// Final cross-check across a dense sweep of points and ranges.
	for x := int64(-20); x <= 60; x++ {
		if got, want := idx.Stab(x), ref.Stab(x); !equalIDs(got, want) {
			t.Fatalf("final sweep Stab(%d): augmented=%v naive=%v", x, got, want)
		}
	}
	for a := int64(-20); a < 60; a++ {
		for b := a; b <= a+6; b++ {
			got, gotErr := idx.Overlap(a, b)
			want, wantErr := ref.Overlap(a, b)
			if errLabel(gotErr) != errLabel(wantErr) ||
				(gotErr == nil && !equalIDs(got, want)) {
				t.Fatalf("final sweep Overlap([%d,%d)): augmented=%v/%s naive=%v/%s",
					a, b, got, errLabel(gotErr), want, errLabel(wantErr))
			}
		}
	}
}
