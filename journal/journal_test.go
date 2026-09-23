package journal

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"ontology/change"
)

func sp(s string) *string { return &s }

func makeLog(t *testing.T, n int) ([]byte, []change.Change, []int) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "j.log")
	w, err := Create(path)
	if err != nil {
		t.Fatal(err)
	}
	cs := make([]change.Change, n)
	for i := range cs {
		cs[i] = change.Change{
			Op:      change.Insert,
			Version: uint64(i + 1),
			ID:      "rec-" + itoa(i),
			Group:   sp("g"),
			Value:   float64(i),
		}
		if err := w.Append(cs[i]); err != nil {
			t.Fatal(err)
		}
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	// Record start offsets and frame lengths.
	starts := []int{headerSize}
	pos := headerSize
	for pos < len(data) {
		nl := int(data[pos])<<24 | int(data[pos+1])<<16 |
			int(data[pos+2])<<8 | int(data[pos+3])
		pos += lenSize + nl + crcSize
		if pos < len(data) {
			starts = append(starts, pos)
		}
	}
	return data, cs, starts
}

func TestAppendReplayRoundTrip(t *testing.T) {
	data, cs, _ := makeLog(t, 50)
	var got []change.Change
	n, err := ReplayBytes(data, func(c change.Change) error {
		got = append(got, c)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if n != len(cs) || len(got) != len(cs) {
		t.Fatalf("applied=%d got=%d want %d", n, len(got), len(cs))
	}
	for i := range cs {
		if got[i].Version != cs[i].Version || got[i].ID != cs[i].ID ||
			got[i].Value != cs[i].Value || *got[i].Group != *cs[i].Group {
			t.Fatalf("record %d mismatch", i)
		}
	}
}

// TestEveryTruncationPoint walks byte offsets 1..len-1 and asserts the
// classified error (or clean frame boundary) and that only the intact record
// prefix is delivered.
func TestEveryTruncationPoint(t *testing.T) {
	const N = 200
	data, cs, starts := makeLog(t, N)
	full := len(data)

	seen := map[error]int{}
	bounds := map[error][2]int{}
	note := func(err error, L int) {
		seen[err]++
		if b, ok := bounds[err]; ok {
			if L < b[0] {
				b[0] = L
			}
			if L > b[1] {
				b[1] = L
			}
			bounds[err] = b
		} else {
			bounds[err] = [2]int{L, L}
		}
	}

	for L := 1; L < full; L++ {
		wantErr, wantApplied := classify(data, starts, L)
		gotApplied := 0
		err := func() error {
			n, e := ReplayBytes(data[:L], func(c change.Change) error {
				if c.Version != cs[gotApplied].Version {
					t.Fatalf("L=%d wrong record", L)
				}
				gotApplied++
				return nil
			})
			if n != wantApplied {
				t.Fatalf("L=%d applied=%d want %d", L, n, wantApplied)
			}
			return e
		}()
		if !errors.Is(err, wantErr) {
			t.Fatalf("L=%d err=%v want %v", L, err, wantErr)
		}
		note(err, L)
	}
	for _, e := range []error{ErrShortHeader, ErrShortLength, ErrShortBody, ErrCRC} {
		if seen[e] == 0 {
			t.Fatalf("class %s never observed", e)
		}
	}
}

// classify returns the expected error (nil at a clean frame boundary) and the
// number of fully intact records for retained length L.
func classify(data []byte, starts []int, L int) (error, int) {
	if L < headerSize {
		return ErrShortHeader, 0
	}
	k := 0
	for k < len(starts) && starts[k] <= L {
		k++
	}
	// k complete records precede position starts[k-1]; find frame at start
	// s where s <= L < s+frameLen.
	s := starts[k-1]
	if L == s {
		return nil, k - 1
	}
	frame := data[s:]
	n := int(frame[0])<<24 | int(frame[1])<<16 | int(frame[2])<<8 | int(frame[3])
	switch {
	case L < s+lenSize:
		return ErrShortLength, k - 1
	case L < s+lenSize+n:
		return ErrShortBody, k - 1
	default:
		return ErrCRC, k - 1
	}
}

func TestBadHeader(t *testing.T) {
	cases := []struct {
		name string
		data []byte
		err  error
	}{
		{"empty", nil, ErrShortHeader},
		{"five", []byte("IVWJ\x01"), ErrShortHeader},
		{"magic", []byte("XXXX\x01\x00"), ErrBadHeader},
		{"version", []byte("IVWJ\x02\x00"), ErrBadHeader},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			n, err := ReplayBytes(tc.data, func(change.Change) error { return nil })
			if !errors.Is(err, tc.err) || n != 0 {
				t.Fatalf("n=%d err=%v want %v", n, err, tc.err)
			}
		})
	}
}

func TestCorruptBodyCRC(t *testing.T) {
	data, _, _ := makeLog(t, 3)
	// Flip one payload byte (header=6, len prefix=4 => first body byte at 10).
	bad := append([]byte{}, data...)
	bad[10] ^= 0xFF
	n, err := ReplayBytes(bad, func(change.Change) error { return nil })
	if !errors.Is(err, ErrCRC) || n != 0 {
		t.Fatalf("n=%d err=%v", n, err)
	}
}

func itoa(i int) string {
	if i == 0 {
		return "0"
	}
	var b [20]byte
	pos := len(b)
	for i > 0 {
		pos--
		b[pos] = byte('0' + i%10)
		i /= 10
	}
	return string(b[pos:])
}
