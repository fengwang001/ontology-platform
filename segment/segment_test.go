package segment

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"ontology/event"
)

func writeSeg(t *testing.T, dir string, firstSeq, n uint64, payloadLen int) string {
	t.Helper()
	path := filepath.Join(dir, fmt.Sprintf("%020d.seg", firstSeq))
	w, err := Create(path, firstSeq)
	if err != nil {
		t.Fatal(err)
	}
	for i := uint64(0); i < n; i++ {
		if err := w.Append(event.Event{Seq: firstSeq + i, Payload: make([]byte, payloadLen)}); err != nil {
			t.Fatal(err)
		}
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestAppendAndScan(t *testing.T) {
	cases := []struct {
		name       string
		n          uint64
		payloadLen int
	}{
		{"empty segment", 0, 8},
		{"single event", 1, 8},
		{"empty payload", 5, 0},
		{"many events", 100, 16},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			path := writeSeg(t, t.TempDir(), 1000, tc.n, tc.payloadLen)
			f, err := os.Open(path)
			if err != nil {
				t.Fatal(err)
			}
			defer f.Close()
			h, err := ReadHeader(f)
			if err != nil {
				t.Fatal(err)
			}
			if h.FirstSeq != 1000 || h.Count != tc.n {
				t.Fatalf("header %+v, want first=1000 count=%d", h, tc.n)
			}
			var seqs []uint64
			var off int64 = -1
			wantOff := int64(HeaderSize)
			err = Scan(f, HeaderSize, func(offset int64, e event.Event, recLen int) error {
				if offset != wantOff {
					t.Errorf("offset %d, want %d", offset, wantOff)
				}
				wantOff += int64(recLen)
				off = offset
				seqs = append(seqs, e.Seq)
				return nil
			})
			if err != nil {
				t.Fatal(err)
			}
			if uint64(len(seqs)) != tc.n {
				t.Fatalf("scanned %d, want %d", len(seqs), tc.n)
			}
			for i, s := range seqs {
				if s != 1000+uint64(i) {
					t.Fatalf("seq[%d]=%d", i, s)
				}
			}
			_ = off
		})
	}
}

func TestScanErrors(t *testing.T) {
	path := writeSeg(t, t.TempDir(), 0, 3, 8)
	fi, _ := os.Stat(path)
	full := fi.Size()
	cases := []struct {
		name    string
		cut     int64
		wantErr error
	}{
		{"header cut", 10, ErrHeaderIncomplete},
		{"length cut", HeaderSize + 2, ErrLengthIncomplete},
		{"body cut", HeaderSize + 6, ErrBodyIncomplete},
		{"crc cut", full - 2, ErrCRCMismatch},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			p := filepath.Join(t.TempDir(), "cut.seg")
			data, _ := os.ReadFile(path)
			if err := os.WriteFile(p, data[:tc.cut], 0o644); err != nil {
				t.Fatal(err)
			}
			f, err := os.Open(p)
			if err != nil {
				t.Fatal(err)
			}
			defer f.Close()
			if _, err := ReadHeader(f); tc.wantErr == ErrHeaderIncomplete {
				if !errors.Is(err, ErrHeaderIncomplete) {
					t.Fatalf("header err %v", err)
				}
				return
			}
			err = Scan(f, HeaderSize, func(int64, event.Event, int) error { return nil })
			if !errors.Is(err, tc.wantErr) {
				t.Fatalf("err %v, want %v", err, tc.wantErr)
			}
		})
	}
}

func TestCRCCorruption(t *testing.T) {
	path := writeSeg(t, t.TempDir(), 0, 2, 8)
	f, err := os.OpenFile(path, os.O_RDWR, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if _, err := f.WriteAt([]byte{0xFF}, HeaderSize+5); err != nil {
		t.Fatal(err)
	}
	err = Scan(f, HeaderSize, func(int64, event.Event, int) error { return nil })
	if !errors.Is(err, ErrCRCMismatch) {
		t.Fatalf("err %v, want CRC mismatch", err)
	}
}
