package journal

import (
	"errors"
	"testing"
)

func TestJournalTable(t *testing.T) {
	// 1) round trip + seq + len cap is rejected without mutating state.
	j := New(2)
	mustAppend := func(id string, p Phase) {
		t.Helper()
		if _, err := j.Append(id, p, 0); err != nil {
			t.Fatalf("append: %v", err)
		}
	}
	mustAppend("a", ExecStart)
	mustAppend("a", ExecDone)
	if _, err := j.Append("a", ExecFail, 0); !errors.Is(err, ErrJournalFull) {
		t.Fatalf("want ErrJournalFull, got %v", err)
	}
	if j.Len() != 2 {
		t.Fatalf("state mutated on rejection: len=%d", j.Len())
	}

	// 2) idempotent replay: same bytes -> identical records, any number of times.
	raw := j.Bytes()
	var first []Record
	for k := 0; k < 3; k++ {
		got := Replay(raw)
		if k == 0 {
			first = got
		} else if len(got) != len(first) || got[1].StepID != "a" || got[1].Phase != ExecDone {
			t.Fatalf("replay not stable: %v", got)
		}
	}
	r2 := New(0)
	r2.Restore(raw)
	if r2.Len() != 2 {
		t.Fatalf("restore len = %d", r2.Len())
	}

	// 3) every torn tail cut point is discarded and the prefix stays consistent.
	full := New(0)
	_, _ = full.Append("x", ExecStart, 1)
	_, _ = full.Append("x", ExecDone, 1)
	fb := full.Bytes()
	frameLen := func(b []byte) int {
		if len(b) < 4 {
			return len(b)
		}
		return 4 + int(b[0])<<24 + int(b[1])<<16 + int(b[2])<<8 + int(b[3]) + 4
	}
	firstFrame := frameLen(fb)
	for cut := 1; cut < len(fb); cut++ {
		got := Replay(fb[:cut])
		if cut < firstFrame {
			if len(got) != 0 {
				t.Fatalf("cut=%d: torn first frame accepted", cut)
			}
		} else {
			if len(got) != 1 || got[0].Phase != ExecStart || got[0].Attempt != 1 {
				t.Fatalf("cut=%d: want exactly first record, got %v", cut, got)
			}
		}
	}

	// 4) CRC corruption of the tail frame is detected.
	bad := append([]byte(nil), fb...)
	bad[len(bad)-1] ^= 0xFF
	if len(Replay(bad)) != 1 {
		t.Fatalf("corrupt tail not rejected: %v", Replay(bad))
	}
}
