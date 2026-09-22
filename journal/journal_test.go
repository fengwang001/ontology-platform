package journal

import (
	"errors"
	"testing"
)

func TestAppendAndReplay(t *testing.T) {
	st := NewStore()
	j := New(st, Options{})
	for i := 1; i <= 3; i++ {
		r, err := j.Append(Record{StepID: "s", Phase: ExecStart, Attempt: i})
		if err != nil {
			t.Fatal(err)
		}
		if r.Seq != i {
			t.Fatalf("seq = %d, want %d", r.Seq, i)
		}
	}
	var seen []int
	n, err := Replay(st, func(r Record) error {
		seen = append(seen, r.Seq)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if n != 3 || len(seen) != 3 {
		t.Fatalf("replay = %v (%d)", seen, n)
	}
}

func TestTornTailDetectedAndDiscarded(t *testing.T) {
	st := NewStore()
	j := New(st, Options{})
	if _, err := j.Append(Record{StepID: "a", Phase: ExecStart}); err != nil {
		t.Fatal(err)
	}
	good := st.Len()
	// Append a torn fragment: header claiming 10 bytes but only 3 present.
	st.AppendRaw([]byte{0, 0, 0, 10, 1, 2, 3})
	n, err := Replay(st, func(Record) error { return nil })
	if err == nil {
		t.Fatalf("replay on torn tail should fail, got n=%d", n)
	}
	j2, kept, torn, err := Recover(st, Options{})
	if err != nil {
		t.Fatal(err)
	}
	if kept != 1 || torn != 7 {
		t.Fatalf("kept=%d torn=%d", kept, torn)
	}
	if st.Len() != good {
		t.Fatalf("store bytes = %d, want %d", st.Len(), good)
	}
	var phases []Phase
	if _, err := Replay(st, func(r Record) error {
		phases = append(phases, r.Phase)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if len(phases) != 1 {
		t.Fatalf("phases = %v", phases)
	}
	// Post-recovery appends continue the sequence.
	r, err := j2.Append(Record{StepID: "b", Phase: ExecStart})
	if err != nil {
		t.Fatal(err)
	}
	if r.Seq != 2 {
		t.Fatalf("seq after recovery = %d", r.Seq)
	}
}

func TestCrashPoints(t *testing.T) {
	for _, point := range []CrashPoint{BeforeWrite, MidWrite, AfterWrite} {
		st := NewStore()
		j := New(st, Options{Hook: func(Record, int) (CrashPoint, bool) {
			return point, true
		}})
		_, err := j.Append(Record{StepID: "a", Phase: ExecStart})
		if !errors.Is(err, ErrCrashed) {
			t.Fatalf("point %d: want ErrCrashed, got %v", point, err)
		}
		// Dead process refuses writes.
		if _, err := j.Append(Record{}); !errors.Is(err, ErrCrashed) {
			t.Fatalf("point %d: post-crash append = %v", point, err)
		}
		// Restart: MidWrite/AfterWrite leave exactly 0 or 1 records.
		j2, kept, _, err := Recover(st, Options{})
		if err != nil {
			t.Fatal(err)
		}
		switch point {
		case BeforeWrite:
			if kept != 0 {
				t.Fatalf("before: kept=%d", kept)
			}
		case MidWrite:
			if kept != 0 {
				t.Fatalf("mid: kept=%d, want 0", kept)
			}
		case AfterWrite:
			if kept != 1 {
				t.Fatalf("after: kept=%d, want 1", kept)
			}
		}
		if !j2.Crashed() == false {
			t.Fatal("new journal after recovery must be alive")
		}
	}
}

func TestLengthLimitRejectsBeforeWrite(t *testing.T) {
	st := NewStore()
	j := New(st, Options{MaxRecords: 2})
	for i := 0; i < 2; i++ {
		if _, err := j.Append(Record{StepID: "a"}); err != nil {
			t.Fatal(err)
		}
	}
	before := st.Len()
	if _, err := j.Append(Record{StepID: "b"}); !errors.Is(err, ErrLimit) {
		t.Fatalf("want ErrLimit, got %v", err)
	}
	if st.Len() != before {
		t.Fatal("rejected append changed store")
	}
}

func TestReplayIdempotent(t *testing.T) {
	st := NewStore()
	j := New(st, Options{})
	for k := 0; k < 5; k++ {
		if _, err := j.Append(Record{StepID: "s", Phase: ExecDone, OK: true}); err != nil {
			t.Fatal(err)
		}
	}
	fold := func() int {
		count := 0
		if _, err := Replay(st, func(Record) error { count++; return nil }); err != nil {
			t.Fatal(err)
		}
		return count
	}
	if fold() != 5 || fold() != 5 || fold() != 5 {
		t.Fatal("replay not stable across repetitions")
	}
}
