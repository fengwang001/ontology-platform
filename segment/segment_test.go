package segment

import (
	"errors"
	"io"
	"os"
	"path/filepath"
	"testing"

	"ontology/event"
)

const testPayloadLen = 10

func buildSegment(t *testing.T, dir string, base uint64, n int) (string, []event.Event) {
	t.Helper()
	path := filepath.Join(dir, "seg-0001")
	w, err := Create(path, base)
	if err != nil {
		t.Fatal(err)
	}
	evs := make([]event.Event, n)
	for i := 0; i < n; i++ {
		p := make([]byte, testPayloadLen)
		for j := range p {
			p[j] = byte((i*7 + j) % 251)
		}
		evs[i] = event.Event{Seq: base + uint64(i), Payload: p}
		if err := w.Append(evs[i]); err != nil {
			t.Fatal(err)
		}
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	return path, evs
}

func TestRoundTrip(t *testing.T) {
	dir := t.TempDir()
	cases := []struct {
		name string
		base uint64
		n    int
	}{
		{"empty segment", 100, 0},
		{"single event", 0, 1},
		{"many events", 42, 17},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			path, evs := buildSegment(t, dir, tc.base, tc.n)
			r, err := Open(path)
			if err != nil {
				t.Fatal(err)
			}
			defer r.Close()
			if r.Header().Base != tc.base || r.Header().Count != uint64(tc.n) {
				t.Fatalf("header = %+v", r.Header())
			}
			for i := 0; i < tc.n; i++ {
				ev, _, _, err := r.Next()
				if err != nil {
					t.Fatalf("event %d: %v", i, err)
				}
				if ev.Seq != evs[i].Seq || string(ev.Payload) != string(evs[i].Payload) {
					t.Fatalf("event %d = %+v, want %+v", i, ev, evs[i])
				}
			}
			if _, _, _, err := r.Next(); !errors.Is(err, io.EOF) {
				t.Fatalf("end err = %v, want EOF", err)
			}
		})
	}
}

func TestTruncationClassification(t *testing.T) {
	dir := t.TempDir()
	path, _ := buildSegment(t, dir, 0, 500)
	full, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	frame := event.FrameOver + testPayloadLen
	type span struct {
		lo, hi int
		err    error
	}
	want := []span{
		{1, HeaderSize - 1, ErrHeaderTruncated},
	}
	for k := 0; k < 500; k++ {
		base := HeaderSize + k*frame
		want = append(want,
			span{base + 1, base + event.LenSize - 1, ErrLengthTruncated},
			span{base + event.LenSize, base + event.LenSize + testPayloadLen + event.CRCSize - 1, ErrBodyTruncated},
		)
	}
	for _, s := range want {
		for cut := s.lo; cut <= s.hi; cut++ {
			got := classifyTruncation(full[:cut])
			if !errors.Is(got, s.err) {
				t.Fatalf("cut=%d span=[%d,%d] got %v want %v", cut, s.lo, s.hi, got, s.err)
			}
		}
	}
}

func classifyTruncation(raw []byte) error {
	if len(raw) < HeaderSize {
		return ErrHeaderTruncated
	}
	if _, err := DecodeHeader(raw[:HeaderSize]); err != nil {
		return err
	}
	tmp, err := os.CreateTemp("", "seg-trunc-*")
	if err != nil {
		return err
	}
	name := tmp.Name()
	defer os.Remove(name)
	if _, err := tmp.Write(raw); err != nil {
		tmp.Close()
		return err
	}
	tmp.Close()
	r, err := Open(name)
	if err != nil {
		return err
	}
	defer r.Close()
	for {
		_, _, _, err = r.Next()
		if err != nil {
			if errors.Is(err, io.EOF) {
				return ErrLengthTruncated
			}
			return err
		}
	}
}
