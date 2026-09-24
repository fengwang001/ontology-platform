package segment

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"testing"

	"ontology/event"
)

func TestAppendReadRoundTrip(t *testing.T) {
	cases := []struct {
		name string
		n    int
	}{
		{"empty segment", 0},
		{"single event", 1},
		{"empty payload events", 3},
		{"many events", 500},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			w, err := Create(dir, 10)
			if err != nil {
				t.Fatal(err)
			}
			for i := 0; i < tc.n; i++ {
				payload := []byte(fmt.Sprintf("p-%d", i))
				if tc.name == "empty payload events" {
					payload = []byte{}
				}
				if err := w.Append(event.Event{Seq: 10 + uint64(i), Payload: payload}); err != nil {
					t.Fatalf("append %d: %v", i, err)
				}
				if got := w.Count(); got != uint32(i+1) {
					t.Fatalf("count after %d: got %d", i, got)
				}
			}
			if err := w.Close(); err != nil {
				t.Fatal(err)
			}
			r, err := Open(dir, 10)
			if err != nil {
				t.Fatal(err)
			}
			for i := 0; ; i++ {
				ev, err := r.Next()
				if errors.Is(err, io.EOF) {
					if i != tc.n {
						t.Fatalf("events: got %d want %d", i, tc.n)
					}
					break
				}
				if err != nil {
					t.Fatalf("next %d: %v", i, err)
				}
				if ev.Seq != 10+uint64(i) {
					t.Fatalf("seq %d: got %d", i, ev.Seq)
				}
			}
			if r.Header().Count != uint32(tc.n) || r.Header().FirstSeq != 10 {
				t.Fatalf("header: %+v", r.Header())
			}
			r.Close()
		})
	}
}

func TestRejectNonContiguous(t *testing.T) {
	dir := t.TempDir()
	w, err := Create(dir, 5)
	if err != nil {
		t.Fatal(err)
	}
	defer w.Close()
	if err := w.Append(event.Event{Seq: 9, Payload: nil}); err == nil {
		t.Fatal("expected non-contiguous append error")
	}
	if err := w.Append(event.Event{Seq: 5, Payload: nil}); err != nil {
		t.Fatalf("valid append: %v", err)
	}
}

func TestByteFlipIsCRCMismatch(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "seg-000000000000.log")
	w, err := Create(dir, 0)
	if err != nil {
		t.Fatal(err)
	}
	if err := w.Append(event.Event{Seq: 0, Payload: []byte("payload-bytes")}); err != nil {
		t.Fatal(err)
	}
	w.Close()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	raw[22+4+8] ^= 0xFF // 翻转第一条记录载荷首字节：CRC 必不符
	if err := os.WriteFile(path, raw, 0o644); err != nil {
		t.Fatal(err)
	}
	r, err := Open(dir, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	if _, err := r.Next(); !errors.Is(err, ErrCRCMismatch) {
		t.Fatalf("want ErrCRCMismatch, got %v", err)
	}
}
