package journal

import (
	"errors"
	"testing"
)

func TestAppendAndReplayRoundTrip(t *testing.T) {
	j := New(0, nil)
	j.Append(PhaseStart, "a", "")
	j.Append(PhaseSuccess, "a", "")
	j.Append(PhaseFailure, "b", "boom")
	recs := j.Records()
	if len(recs) != 3 {
		t.Fatalf("got %d records", len(recs))
	}
	for i, r := range recs {
		if r.Seq != uint64(i+1) {
			t.Fatalf("record %d seq = %d", i, r.Seq)
		}
	}
	if recs[2].Note != "boom" || recs[2].StepID != "b" {
		t.Fatalf("record 2 = %+v", recs[2])
	}
	if j.Len() != 3 {
		t.Fatalf("len = %d", j.Len())
	}
}

func TestTornTailDroppedAndLogStaysConsistent(t *testing.T) {
	j := New(0, nil)
	j.Append(PhaseStart, "a", "")
	j.Append(PhaseSuccess, "a", "")
	raw := j.Bytes()

	// Simulate a crash in the middle of writing a third record.
	full := New(0, nil)
	full.Append(PhaseStart, "a", "")
	full.Append(PhaseSuccess, "a", "")
	full.Append(PhaseCompStart, "a", "")
	torn := full.Bytes()[:len(raw)+5] // half of the third frame

	j2, wasTorn := FromBytes(torn, 0, nil)
	if !wasTorn {
		t.Fatal("torn tail not detected")
	}
	if got := j2.Records(); len(got) != 2 {
		t.Fatalf("replayed %d records, want 2", len(got))
	}
	// The log is still appendable and self-consistent after the drop.
	if _, err := j2.Append(PhaseCompStart, "a", ""); err != nil {
		t.Fatal(err)
	}
	recs := j2.Records()
	if len(recs) != 3 || recs[2].Phase != PhaseCompStart || recs[2].Seq != 3 {
		t.Fatalf("records after drop = %+v", recs)
	}
}

func TestCorruptCRCTreatedAsTorn(t *testing.T) {
	j := New(0, nil)
	j.Append(PhaseStart, "a", "")
	raw := j.Bytes()
	raw[len(raw)-1] ^= 0xff // flip a CRC bit
	j2, torn := FromBytes(raw, 0, nil)
	if !torn || j2.Len() != 0 {
		t.Fatalf("torn=%v len=%d", torn, j2.Len())
	}
}

func TestRecordLimit(t *testing.T) {
	j := New(2, nil)
	j.Append(PhaseStart, "a", "")
	j.Append(PhaseSuccess, "a", "")
	if _, err := j.Append(PhaseStart, "b", ""); !errors.Is(err, ErrTooLong) {
		t.Fatalf("want ErrTooLong, got %v", err)
	}
	if j.Len() != 2 {
		t.Fatalf("len changed after rejection: %d", j.Len())
	}
}

func TestHookSeesBeforeAndAfterWithSnapshots(t *testing.T) {
	type seen struct {
		when When
		size int
	}
	var events []seen
	j := New(0, func(ev Event) {
		events = append(events, seen{ev.When, len(ev.Snapshot())})
	})
	j.Append(PhaseStart, "a", "")
	j.Append(PhaseSuccess, "a", "")
	if len(events) != 4 {
		t.Fatalf("events = %d", len(events))
	}
	if events[0].when != Before || events[1].when != After {
		t.Fatalf("order = %+v", events)
	}
	if events[0].size != 0 || events[1].size == 0 {
		t.Fatalf("snapshot sizes = %+v", events)
	}
	if events[1].size != events[2].size {
		t.Fatalf("after-write snapshot must equal next before-write snapshot")
	}
}

func TestReplayIsDeterministic(t *testing.T) {
	j := New(0, nil)
	for i := 0; i < 50; i++ {
		j.Append(Phase(byte(i%7)), "s", "n")
	}
	a := j.Records()
	b := j.Records()
	if len(a) != len(b) {
		t.Fatal("replay not deterministic")
	}
	for i := range a {
		if a[i] != b[i] {
			t.Fatalf("record %d differs", i)
		}
	}
}
