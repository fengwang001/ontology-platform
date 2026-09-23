package segment

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func writeSeg(t *testing.T, path string, firstSeq uint64, payloads [][]byte) Header {
	t.Helper()
	w, err := Create(path, firstSeq)
	if err != nil {
		t.Fatal(err)
	}
	for i, p := range payloads {
		seq, err := w.Append(p)
		if err != nil {
			t.Fatal(err)
		}
		if seq != firstSeq+uint64(i) {
			t.Fatalf("append seq = %d, want %d", seq, firstSeq+uint64(i))
		}
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	return Header{FirstSeq: firstSeq, Count: uint64(len(payloads))}
}

func TestWriteReadRoundTrip(t *testing.T) {
	dir := t.TempDir()
	cases := []struct {
		name     string
		firstSeq uint64
		payloads [][]byte
	}{
		{"empty segment", 0, nil},
		{"single event", 0, [][]byte{[]byte("only")}},
		{"empty payload", 7, [][]byte{{}, []byte("x")}},
		{"many events", 100, [][]byte{[]byte("a"), []byte("bb"), []byte("ccc")}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(dir, "seg")
			want := writeSeg(t, path, tc.firstSeq, tc.payloads)
			h, evs, err := ReadAll(path)
			if err != nil {
				t.Fatalf("ReadAll: %v", err)
			}
			if h != want {
				t.Fatalf("header = %+v, want %+v", h, want)
			}
			if len(evs) != len(tc.payloads) {
				t.Fatalf("got %d events, want %d", len(evs), len(tc.payloads))
			}
			for i, ev := range evs {
				if ev.Seq != tc.firstSeq+uint64(i) {
					t.Fatalf("event %d seq = %d", i, ev.Seq)
				}
				if !bytes.Equal(ev.Payload, tc.payloads[i]) {
					t.Fatalf("event %d payload mismatch", i)
				}
			}
		})
	}
}

func TestHeaderIncomplete(t *testing.T) {
	path := filepath.Join(t.TempDir(), "seg")
	if err := os.WriteFile(path, []byte("OSE"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, _, err := ReadAll(path); !errors.Is(err, ErrHeaderIncomplete) {
		t.Fatalf("err = %v, want ErrHeaderIncomplete", err)
	}
}

func TestAdjacentSegmentsContinuous(t *testing.T) {
	dir := t.TempDir()
	h1 := writeSeg(t, filepath.Join(dir, "s1"), 0, [][]byte{[]byte("a"), []byte("b")})
	h2 := writeSeg(t, filepath.Join(dir, "s2"), h1.FirstSeq+h1.Count, [][]byte{[]byte("c")})
	h3 := writeSeg(t, filepath.Join(dir, "s3"), h2.FirstSeq+h2.Count, nil)
	if h1.FirstSeq+h1.Count != h2.FirstSeq || h2.FirstSeq+h2.Count != h3.FirstSeq {
		t.Fatalf("segment seqs not continuous: %+v %+v %+v", h1, h2, h3)
	}
}
