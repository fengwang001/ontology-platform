package journal

import (
	"errors"
	"testing"
)

func TestAppendReplayIdempotent(t *testing.T) {
	j := New(0, nil)
	for i := 0; i < 50; i++ {
		ph := PhaseDone
		if _, err := j.Append("s", ph, ""); err != nil {
			t.Fatal(err)
		}
	}
	img := j.Bytes()
	for k := 0; k < 3; k++ {
		r2, err := Load(img, 0, nil)
		if err != nil {
			t.Fatal(err)
		}
		if r2.Len() != 50 {
			t.Fatalf("replay %d len=%d want 50", k, r2.Len())
		}
		for n, rec := range r2.Snapshot() {
			if rec.Seq != n+1 || rec.StepID != "s" || rec.Phase != PhaseDone {
				t.Fatalf("record %d mismatch: %+v", n, rec)
			}
		}
	}
}

type crashSentinel struct{}

func TestTornFrameDiscarded(t *testing.T) {
	j := New(0, nil)
	_, _ = j.Append("a", PhaseStart, "")
	_, _ = j.Append("a", PhaseDone, "")
	img := j.Bytes()

	// Simulate a MidWrite crash: only the first half of frame 3 durable.
	j2 := New(0, func(r Record, p CrashPoint) {
		if r.Seq == 3 && p == MidWrite {
			panic(crashSentinel{})
		}
	})
	var panicked bool
	func() {
		defer func() {
			if recover() != nil {
				panicked = true
			}
		}()
		// Rebuild state of first two frames into j2 manually via load:
		nj, _ := Load(img, 0, j2.hook)
		j2 = nj
		_, _ = j2.Append("b", PhaseStart, "")
	}()
	if !panicked {
		t.Fatal("expected crash panic")
	}
	recovered, err := Load(j2.Bytes(), 0, nil)
	if err != nil {
		t.Fatalf("torn frame must not be a hard error: %v", err)
	}
	if recovered.Len() != 2 {
		t.Fatalf("torn tail not discarded: len=%d", recovered.Len())
	}
	if recovered.Snapshot()[1].StepID != "a" {
		t.Fatal("valid prefix lost")
	}
	// New append after recovery continues with seq 3 cleanly.
	r, err := recovered.Append("b", PhaseStart, "")
	if err != nil || r.Seq != 3 {
		t.Fatalf("resume append wrong: %+v %v", r, err)
	}
}

func TestCrcCorruptionHardError(t *testing.T) {
	j := New(0, nil)
	_, _ = j.Append("a", PhaseDone, "x")
	img := j.Bytes()
	img[len(img)-1] ^= 0xFF
	if _, err := Load(img, 0, nil); !errors.Is(err, ErrCrc) {
		t.Fatalf("want ErrCrc, got %v", err)
	}
}

func TestLimitRejectedWithoutStateChange(t *testing.T) {
	j := New(2, nil)
	if _, err := j.Append("a", PhaseStart, ""); err != nil {
		t.Fatal(err)
	}
	if _, err := j.Append("a", PhaseDone, ""); err != nil {
		t.Fatal(err)
	}
	before := j.Len()
	if _, err := j.Append("a", PhaseFailed, "x"); !errors.Is(err, ErrLimit) {
		t.Fatalf("want ErrLimit, got %v", err)
	}
	if j.Len() != before {
		t.Fatal("state changed after rejection")
	}
}

func TestAllCrashBoundaries(t *testing.T) {
	// For seq 2, crash at each of the three boundaries; recovery state
	// must be exactly the set of fully durable frames.
	for _, pt := range []CrashPoint{BeforeWrite, MidWrite, AfterWrite} {
		j := New(0, nil)
		_, _ = j.Append("a", PhaseStart, "")
		img := j.Bytes()
		pt := pt
		nj, _ := Load(img, 0, func(r Record, p CrashPoint) {
			if r.Seq == 2 && p == pt {
				panic(crashSentinel{})
			}
		})
		func() {
			defer func() { _ = recover() }()
			_, _ = nj.Append("a", PhaseDone, "")
		}()
		got, err := Load(nj.Bytes(), 0, nil)
		if err != nil {
			t.Fatalf("%s: %v", pt, err)
		}
		// Before: frame 2 absent. Mid: torn frame discarded. After: present.
		want := 1
		if pt == AfterWrite {
			want = 2
		}
		if got.Len() != want {
			t.Fatalf("%s: len=%d want %d", pt, got.Len(), want)
		}
	}
}
