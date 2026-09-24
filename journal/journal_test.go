package journal

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"ontology/change"
)

const truncRecords = 200

func writeLog(t *testing.T, n int) (string, []change.Change) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "log")
	j, err := Create(path)
	if err != nil {
		t.Fatal(err)
	}
	all := make([]change.Change, n)
	for i := 0; i < n; i++ {
		all[i] = change.Change{
			Version:    int64(i + 1),
			Op:         change.Insert,
			NewGroup:   fmt.Sprintf("g%d", i%7),
			NewGroupOK: true,
			NewValue:   float64(i),
		}
		if err := j.Append(all[i]); err != nil {
			t.Fatal(err)
		}
	}
	if err := j.Close(); err != nil {
		t.Fatal(err)
	}
	return path, all
}

// expectedErrAt derives the classified error for a file truncated to size bytes.
func expectedErrAt(size int, rec []recordBounds) error {
	if size < len(magic) {
		return ErrShortHeader
	}
	if size == len(magic) {
		return nil
	}
	for _, r := range rec {
		if size == r.end {
			return nil
		}
		if size > r.start && size < r.start+4 {
			return ErrShortLength
		}
		if size >= r.start+4 && size < r.end {
			return ErrShortBody
		}
	}
	return nil
}

type recordBounds struct{ start, end int }

func TestTruncationByteByByte(t *testing.T) {
	path, all := writeLog(t, truncRecords)
	raw, err := readAll(path)
	if err != nil {
		t.Fatal(err)
	}
	rec := make([]recordBounds, truncRecords)
	pos := len(magic)
	for i := 0; i < truncRecords; i++ {
		start := pos
		pos += 4 + len(mustEncode(t, all[i])) + 4
		rec[i] = recordBounds{start, pos}
	}
	first := map[error]int{}
	for size := 1; size < len(raw); size++ {
		p := filepath.Join(t.TempDir(), "cut")
		if err := writeCut(p, raw[:size]); err != nil {
			t.Fatal(err)
		}
		var got []change.Change
		n, err := Replay(p, func(c change.Change) { got = append(got, c) })
		want := expectedErrAt(size, rec)
		if !errors.Is(err, want) {
			t.Fatalf("size %d: err=%v want=%v", size, err, want)
		}
		if n != len(got) {
			t.Fatalf("size %d: count %d != delivered %d", size, n, len(got))
		}
		wantRecs := completeCount(size, rec)
		if n != wantRecs {
			t.Fatalf("size %d: replayed %d want %d", size, n, wantRecs)
		}
		if _, seen := first[want]; !seen {
			first[want] = size
		}
	}
	if len(first) != 3 {
		t.Fatalf("truncation did not hit all 3 classes: %v", first)
	}
	t.Logf("first truncation byte per class: header=%d length=%d body=%d",
		first[ErrShortHeader], first[ErrShortLength], first[ErrShortBody])
}

func completeCount(size int, rec []recordBounds) int {
	for i, r := range rec {
		if size < r.end {
			return i
		}
	}
	return len(rec)
}

func TestCRCMismatch(t *testing.T) {
	path, _ := writeLog(t, 3)
	raw, err := readAll(path)
	if err != nil {
		t.Fatal(err)
	}
	raw[len(raw)-1] ^= 0xFF
	p := filepath.Join(t.TempDir(), "crc")
	if err := writeCut(p, raw); err != nil {
		t.Fatal(err)
	}
	n, err := Replay(p, func(change.Change) {})
	if !errors.Is(err, ErrCRC) || n != 2 {
		t.Fatalf("err=%v n=%d, want ErrCRC and 2 complete records", err, n)
	}
}

func TestHappyReplay(t *testing.T) {
	cases := []int{1, 50, 200}
	for _, n := range cases {
		path, want := writeLog(t, n)
		var got []change.Change
		cnt, err := Replay(path, func(c change.Change) { got = append(got, c) })
		if err != nil || cnt != n || len(got) != n {
			t.Fatalf("n=%d cnt=%d err=%v", n, cnt, err)
		}
		for i := range want {
			if got[i] != want[i] {
				t.Fatalf("record %d mismatch", i)
			}
		}
	}
}

func TestEmptyAndBadMagic(t *testing.T) {
	cases := []struct {
		name    string
		content []byte
		want    error
	}{
		{"empty", nil, ErrShortHeader},
		{"partial header", []byte("ONT"), ErrShortHeader},
		{"bad magic", []byte("XXXXXX"), ErrBadMagic},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			p := filepath.Join(t.TempDir(), "x")
			if err := writeCut(p, tc.content); err != nil {
				t.Fatal(err)
			}
			if _, err := Replay(p, func(change.Change) {}); !errors.Is(err, tc.want) {
				t.Fatalf("err=%v want %v", err, tc.want)
			}
		})
	}
}

func mustEncode(t *testing.T, c change.Change) []byte {
	t.Helper()
	b, err := c.Encode()
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func readAll(path string) ([]byte, error) {
	return os.ReadFile(path)
}

func writeCut(path string, b []byte) error { return os.WriteFile(path, b, 0o600) }
