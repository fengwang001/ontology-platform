package backfill_test

import (
	"errors"
	"reflect"
	"testing"

	"ontology/backfill"
)

// step is one row of the nine-event derivation in NOTES section 3.
type step struct {
	via  string
	seq  int64
	key  string
	want map[string]int64
	seen int
}

var nineWalk = []step{
	{"B", 1, "a", map[string]int64{"a": 1}, 1},
	{"B", 3, "b", map[string]int64{"a": 1, "b": 1}, 2},
	{"O", 12, "b", map[string]int64{"a": 1, "b": 2}, 3},
	{"B", 5, "a", map[string]int64{"a": 2, "b": 2}, 4},
	{"O", 5, "a", map[string]int64{"a": 2, "b": 2}, 4}, // cross-boundary dup: no-op
	{"B", 7, "c", map[string]int64{"a": 2, "b": 2, "c": 1}, 5},
	{"O", 11, "c", map[string]int64{"a": 2, "b": 2, "c": 2}, 6},
	{"B", 9, "a", map[string]int64{"a": 3, "b": 2, "c": 2}, 7},
	{"O", 3, "b", map[string]int64{"a": 3, "b": 2, "c": 2}, 7}, // dup: no-op
}

// TestNineEventWalkthrough replays all nine rows and then the cutover +
// post-cutover rejection of NOTES section 3 (case 甲).
func TestNineEventWalkthrough(t *testing.T) {
	v := backfill.New(10)
	for i, st := range nineWalk {
		e := []backfill.Event{{Seq: st.seq, Key: st.key}}
		var err error
		if st.via == "B" {
			err = v.ApplyBackfill(e)
		} else {
			err = v.ApplyOnline(e)
		}
		if err != nil || !reflect.DeepEqual(v.Counts(), st.want) || v.Seen() != st.seen {
			t.Fatalf("step %d: err=%v view=%v seen=%d want %v/%d",
				i+1, err, v.Counts(), v.Seen(), st.want, st.seen)
		}
	}
	if err := v.Complete(); err != nil || v.Counts()["a"] != 3 || v.Seen() != 7 {
		t.Fatalf("cutover changed state: %v seen=%d err=%v", v.Counts(), v.Seen(), err)
	}
	if err := v.ApplyBackfill([]backfill.Event{{Seq: 2, Key: "a"}}); !errors.Is(err, backfill.ErrBackfillCompleted) {
		t.Fatalf("post-cutover backfill: %v", err)
	}
	if v.Counts()["a"] != 3 || v.Seen() != 7 { // rejected op leaves no trace
		t.Fatalf("rejection changed state: %v seen=%d", v.Counts(), v.Seen())
	}
	if err := v.SelfCheck(); err != nil {
		t.Fatal(err)
	}
}

// TestRejectedBatchAtomic pins invariant 4: every rejected operation fails
// the whole batch, leaves no trace, and the four sentinels are distinct.
func TestRejectedBatchAtomic(t *testing.T) {
	sents := []error{
		backfill.ErrInvalidW,
		backfill.ErrBackfillOutOfRange,
		backfill.ErrBackfillCompleted,
		backfill.ErrEmptyKey,
	}
	seenID := map[error]bool{}
	for _, s := range sents {
		if seenID[s] {
			t.Fatal("sentinel errors are not mutually distinct")
		}
		seenID[s] = true
	}
	v := backfill.New(10)
	if err := v.ApplyBackfill([]backfill.Event{{Seq: 1, Key: "a"}, {Seq: 3, Key: "b"}}); err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		online bool
		evs    []backfill.Event
		want   error
	}{
		{false, []backfill.Event{{Seq: 5, Key: "a"}, {Seq: 10, Key: "d"}, {Seq: 7, Key: "c"}}, backfill.ErrBackfillOutOfRange},
		{false, []backfill.Event{{Seq: 5, Key: "a"}, {Seq: 7, Key: ""}}, backfill.ErrEmptyKey},
		{true, []backfill.Event{{Seq: 12, Key: ""}}, backfill.ErrEmptyKey},
	}
	for i, c := range cases {
		var err error
		if c.online {
			err = v.ApplyOnline(c.evs)
		} else {
			err = v.ApplyBackfill(c.evs)
		}
		if !errors.Is(err, c.want) || v.Seen() != 2 || v.Counts()["a"] != 1 ||
			v.Counts()["d"] != 0 || v.Counts()["c"] != 0 {
			t.Fatalf("case %d: err=%v state=%v seen=%d", i, err, v.Counts(), v.Seen())
		}
	}
	if err := v.Complete(); err != nil {
		t.Fatal(err)
	}
	if err := v.ApplyBackfill([]backfill.Event{{Seq: 2, Key: "a"}}); !errors.Is(err, backfill.ErrBackfillCompleted) {
		t.Fatal(err)
	}
	if err := v.ApplyOnline([]backfill.Event{{Seq: 12, Key: "b"}}); err != nil || v.Counts()["b"] != 2 {
		t.Fatalf("instance unusable after rejection: b=%d err=%v", v.Counts()["b"], err)
	}
	badW := backfill.New(-1)
	if err := badW.ApplyOnline([]backfill.Event{{Seq: 1, Key: "a"}}); !errors.Is(err, backfill.ErrInvalidW) || badW.Seen() != 0 {
		t.Fatal("negative W must be rejected and hold no state")
	}
}
