package segment

import (
	"errors"
	"io"
	"os"
	"path/filepath"
	"testing"

	"ontology/event"
)

func writeFullSegment(t *testing.T, path string, n int) {
	t.Helper()
	w, err := Create(path, 0)
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < n; i++ {
		pay := []byte{}
		if i%3 == 0 {
			pay = []byte{byte(i), byte(i >> 8), 7}
		}
		if err := w.Append(event.Event{Seq: uint64(i), Payload: pay}); err != nil {
			t.Fatal(err)
		}
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "seg")
	writeFullSegment(t, path, 10)
	r, err := OpenReader(path)
	if err != nil {
		t.Fatal(err)
	}
	if r.Header.FirstSeq != 0 || r.Header.Count != 10 {
		t.Fatalf("header = %+v", r.Header)
	}
	var got []event.Event
	for {
		ev, err := r.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		got = append(got, ev)
	}
	if len(got) != 10 {
		t.Fatalf("got %d events", len(got))
	}
	for i, ev := range got {
		if ev.Seq != uint64(i) {
			t.Fatalf("seq[%d]=%d", i, ev.Seq)
		}
	}
}

func TestTruncationClassification(t *testing.T) {
	dir := t.TempDir()
	orig := filepath.Join(dir, "orig")
	writeFullSegment(t, orig, 500)
	full, err := os.ReadFile(orig)
	if err != nil {
		t.Fatal(err)
	}
	// All truncation points 1..len-1 in one loop; classify and bucket ranges.
	bounds := map[error][2]int{}
	var valid, shortH, shortL, shortF int
	for b := 1; b < len(full); b++ {
		in, err := InspectBytes(full[:b])
		switch {
		case errors.Is(err, ErrShortHeader):
			shortH++
		case errors.Is(in.Err, event.ErrShortLength):
			shortL++
			bucket(bounds, event.ErrShortLength, b)
		case errors.Is(in.Err, event.ErrShortFrame):
			shortF++
			bucket(bounds, event.ErrShortFrame, b)
		case err == nil && in.Err == nil:
			valid++
		default:
			t.Fatalf("b=%d unclassified: openErr=%v inspectErr=%v", b, err, in.Err)
		}
		if b < HeaderSize && !errors.Is(err, ErrShortHeader) {
			t.Fatalf("b=%d: want ErrShortHeader got %v", b, err)
		}
	}
	if shortH != HeaderSize-1 {
		t.Fatalf("header truncations = %d, want %d", shortH, HeaderSize-1)
	}
	if shortL == 0 || shortF == 0 || valid == 0 {
		t.Fatalf("missing classes: len=%d frame=%d valid=%d", shortL, shortF, valid)
	}
	// CRC mismatch is a corruption (not a truncation): flip one payload byte.
	bad := append([]byte(nil), full...)
	bad[HeaderSize+event.FixedPrefix] ^= 0xFF
	in, err := InspectBytes(bad)
	if err != nil || !errors.Is(in.Err, event.ErrCRC) {
		t.Fatalf("flip: err=%v inspectErr=%v", err, in.Err)
	}
	if len(in.Events) != 0 {
		t.Fatalf("corrupted first frame: recovered %d", len(in.Events))
	}
	t.Logf("ranges: %v valid=%d shortHeader=%d", bounds, valid, shortH)
}

func bucket(m map[error][2]int, k error, b int) {
	v, ok := m[k]
	if !ok {
		v = [2]int{b, b}
	}
	if b < v[0] {
		v[0] = b
	}
	if b > v[1] {
		v[1] = b
	}
	m[k] = v
}

func TestReopenAppend(t *testing.T) {
	path := filepath.Join(t.TempDir(), "seg")
	writeFullSegment(t, path, 3)
	w, err := OpenWriter(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := w.Append(event.Event{Seq: 3, Payload: []byte("x")}); err != nil {
		t.Fatal(err)
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	in, err := InspectPath(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(in.Events) != 4 || in.Header.Count != 4 {
		t.Fatalf("events=%d count=%d", len(in.Events), in.Header.Count)
	}
}
