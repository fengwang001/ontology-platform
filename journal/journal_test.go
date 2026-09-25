package journal

import (
	"errors"
	"math"
	"os"
	"path/filepath"
	"testing"

	"ontology/change"
)

func mkChange(i uint64, op change.Op) change.Change {
	c := change.Change{Version: i, Op: op, GroupSet: true, RKey: "r", Group: "g", Value: float64(i)}
	if op == change.OpUpdate {
		c.HasOld = true
		c.OldGroup = "h"
		c.OldValue = -float64(i)
	}
	return c
}

func TestChangeRoundTrip(t *testing.T) {
	cases := []struct {
		name string
		in   change.Change
		want change.Change
	}{
		{"insert", mkChange(1, change.OpInsert), mkChange(1, change.OpInsert)},
		{"delete", mkChange(2, change.OpDelete), mkChange(2, change.OpDelete)},
		{"update", mkChange(3, change.OpUpdate), mkChange(3, change.OpUpdate)},
		{"empty group", change.Change{Version: 4, Op: change.OpInsert, GroupSet: true, RKey: "k"},
			change.Change{Version: 4, Op: change.OpInsert, GroupSet: true, RKey: "k"}},
		{"neg zero", change.Change{Version: 5, Op: change.OpInsert, GroupSet: true, Value: math.Copysign(0, -1)},
			change.Change{Version: 5, Op: change.OpInsert, GroupSet: true, Value: 0}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			b, err := tc.in.Encode()
			if err != nil {
				t.Fatal(err)
			}
			got, err := change.Decode(b)
			if err != nil {
				t.Fatal(err)
			}
			if got != tc.want {
				t.Fatalf("got %+v want %+v", got, tc.want)
			}
		})
	}
}

func TestValidate(t *testing.T) {
	cases := []struct {
		name string
		in   change.Change
		err  error
	}{
		{"bad op", change.Change{Version: 1, Op: 9, GroupSet: true}, change.ErrBadOp},
		{"missing group", change.Change{Version: 1, Op: change.OpInsert}, change.ErrMissingGroup},
		{"nan", change.Change{Version: 1, Op: change.OpInsert, GroupSet: true, Value: math.NaN()}, change.ErrNaNValue},
		{"nan old", change.Change{Version: 1, Op: change.OpUpdate, GroupSet: true, HasOld: true, OldValue: math.NaN()}, change.ErrNaNValue},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if err := tc.in.Validate(); !errors.Is(err, tc.err) {
				t.Fatalf("got %v want %v", err, tc.err)
			}
		})
	}
}

// buildLog writes 200 records and returns the journal path.
func buildLog(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "log")
	w, err := Create(path)
	if err != nil {
		t.Fatal(err)
	}
	for i := uint64(1); i <= 200; i++ {
		if err := w.Append(mkChange(i, change.OpInsert)); err != nil {
			t.Fatal(err)
		}
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	return path
}

func countReplay(t *testing.T, path string) (int, error) {
	t.Helper()
	n := 0
	k, err := Replay(path, func(change.Change) error { n++; return nil })
	if n != k {
		t.Fatalf("callback %d != return %d", n, k)
	}
	return k, err
}

func truncated(path string, n int64) (string, error) {
	dst := filepath.Join(filepath.Dir(path), "cut")
	b, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	if err := os.WriteFile(dst, b[:n], 0o600); err != nil {
		return "", err
	}
	return dst, nil
}

func TestBytewiseTruncation(t *testing.T) {
	path := buildLog(t)
	fi, _ := os.Stat(path)
	size := fi.Size()
	body0, _ := mkChange(1, change.OpInsert).Encode()
	frame := int64(lenPrefixSize + len(body0) + crcSize)

	seen := map[error]bool{}
	for cut := int64(1); cut < size; cut++ {
		p, err := truncated(path, cut)
		if err != nil {
			t.Fatal(err)
		}
		n, rerr := countReplay(t, p)
		var want error
		if cut < int64(len(magic)) {
			want = ErrHeaderIncomplete
		} else {
			pos := cut - int64(len(magic))
			k, d := pos/frame, pos%frame
			if k >= 200 { // inside the 8-byte END marker
				if d < lenPrefixSize {
					want = ErrLenPrefixIncomplete
				} else {
					want = ErrCRCMismatch
				}
			} else {
				switch {
				case d < lenPrefixSize:
					want = ErrLenPrefixIncomplete
				case d < frame-crcSize:
					want = ErrBodyIncomplete
				default:
					want = ErrCRCMismatch
				}
			}
		}
		if !errors.Is(rerr, want) {
			t.Fatalf("cut=%d n=%d got %v want %v", cut, n, rerr, want)
		}
		// exactly the complete prefix took effect.
		wantN := 0
		if cut >= int64(len(magic)) {
			wantN = int((cut - int64(len(magic))) / frame)
			if wantN > 200 {
				wantN = 200
			}
		}
		if n != wantN {
			t.Fatalf("cut=%d applied %d want %d", cut, n, wantN)
		}
		seen[want] = true
	}
	for _, e := range []error{ErrHeaderIncomplete, ErrLenPrefixIncomplete, ErrBodyIncomplete, ErrCRCMismatch} {
		if !seen[e] {
			t.Fatalf("class never observed: %v", e)
		}
	}
}
