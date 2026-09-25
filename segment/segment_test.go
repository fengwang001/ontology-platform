package segment

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"ontology/event"
)

func writeEvents(t *testing.T, path string, every, first uint64, payloads ...[]byte) Header {
	t.Helper()
	w, err := Create(path, every, first)
	if err != nil {
		t.Fatal(err)
	}
	for i, p := range payloads {
		if err := w.Append(event.Encode(event.Event{Seq: first + uint64(i), Payload: p})); err != nil {
			t.Fatal(err)
		}
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	return w.Header()
}

func TestAppendAndScan(t *testing.T) {
	cases := []struct {
		name string
		n    int
	}{
		{"empty", 0},
		{"single", 1},
		{"many with empty payloads", 50},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "seg.log")
			payloads := make([][]byte, tc.n)
			for i := range payloads {
				if i%2 == 0 {
					payloads[i] = []byte{byte(i)}
				}
			}
			hdr := writeEvents(t, path, 8, 100, payloads...)
			if hdr.Count != uint64(tc.n) || hdr.FirstSeq != 100 || hdr.IndexEvery != 8 {
				t.Fatalf("header = %+v", hdr)
			}
			var got []event.Event
			_, good, _, err := Scan(path, func(_ int64, enc []byte) error {
				e, err := event.Decode(enc)
				if err != nil {
					return err
				}
				got = append(got, e)
				return nil
			})
			if err != nil || good != uint64(tc.n) {
				t.Fatalf("scan good=%d err=%v", good, err)
			}
			for i, e := range got {
				if e.Seq != 100+uint64(i) || string(e.Payload) != string(payloads[i]) {
					t.Fatalf("event %d = %+v", i, e)
				}
			}
		})
	}
}

func TestAdjacentSegmentContinuity(t *testing.T) {
	dir := t.TempDir()
	cases := []struct {
		first uint64
		n     int
	}{
		{0, 7}, {7, 3}, {10, 9},
	}
	var prev Header
	for i, tc := range cases {
		payloads := make([][]byte, tc.n)
		hdr := writeEvents(t, filepath.Join(dir, "seg"+string(rune('0'+i))+".log"), 4, tc.first, payloads...)
		if i > 0 && prev.FirstSeq+prev.Count != hdr.FirstSeq {
			t.Fatalf("gap: prev %+v next %+v", prev, hdr)
		}
		prev = hdr
	}
}

func TestReadRecordErrors(t *testing.T) {
	path := filepath.Join(t.TempDir(), "seg.log")
	writeEvents(t, path, 4, 0, []byte("aaaa"), []byte("bbbb"))
	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	cases := []struct {
		name string
		off  int64
		want error
	}{
		{"past end", 1 << 20, ErrLengthPrefixIncomplete},
		{"zero length", HeaderSize + 8, ErrLengthPrefixInvalid},
		{"huge length", HeaderSize + 16, ErrLengthPrefixInvalid},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, _, err := ReadRecordAt(f, tc.off); !errors.Is(err, tc.want) {
				t.Fatalf("err = %v, want %v", err, tc.want)
			}
		})
	}
}

func TestReadHeaderIncomplete(t *testing.T) {
	path := filepath.Join(t.TempDir(), "short.log")
	if err := os.WriteFile(path, []byte("OSE"), 0o644); err != nil {
		t.Fatal(err)
	}
	f, _ := os.Open(path)
	defer f.Close()
	if _, err := ReadHeader(f); !errors.Is(err, ErrHeaderIncomplete) {
		t.Fatalf("err = %v", err)
	}
}
