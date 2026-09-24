package segment

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"testing"

	"ontology/event"
)

func TestWriteReadRoundTrip(t *testing.T) {
	cases := []struct {
		name      string
		maxEvents int
		total     int
		payload   func(i int) []byte
	}{
		{"single segment", 100, 10, func(i int) []byte { return []byte(fmt.Sprintf("p-%d", i)) }},
		{"exact rotation", 4, 8, func(i int) []byte { return []byte{byte(i)} }},
		{"uneven rotation", 3, 10, func(i int) []byte { return bytes.Repeat([]byte{byte(i)}, i) }},
		{"empty payloads", 2, 5, func(i int) []byte { return nil }},
		{"single event", 100, 1, func(i int) []byte { return []byte("x") }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			w, err := NewWriter(dir, tc.maxEvents, 2)
			if err != nil {
				t.Fatal(err)
			}
			for i := 0; i < tc.total; i++ {
				seq, err := w.Append(tc.payload(i))
				if err != nil {
					t.Fatal(err)
				}
				if seq != uint64(i) {
					t.Fatalf("seq = %d, want %d", seq, i)
				}
			}
			if err := w.Close(); err != nil {
				t.Fatal(err)
			}
			segs, err := ListSegments(dir)
			if err != nil {
				t.Fatal(err)
			}
			var got []event.Event
			var prev Header
			for i, p := range segs {
				sc, err := NewScanner(p)
				if err != nil {
					t.Fatal(err)
				}
				if i > 0 && prev.FirstSeq+prev.Count != sc.Header.FirstSeq {
					t.Fatalf("boundary not continuous: %+v -> %+v", prev, sc.Header)
				}
				prev = sc.Header
				sc.Close()
				evs, err := ReadAll(p)
				if err != nil {
					t.Fatal(err)
				}
				got = append(got, evs...)
			}
			if len(got) != tc.total {
				t.Fatalf("read %d events, want %d", len(got), tc.total)
			}
			for i, ev := range got {
				if ev.Seq != uint64(i) || !bytes.Equal(ev.Payload, tc.payload(i)) {
					t.Fatalf("event %d mismatch: %+v", i, ev)
				}
			}
		})
	}
}

func TestDecodeRecordErrors(t *testing.T) {
	rec := EncodeRecord(event.Event{Seq: 9, Payload: []byte("hello")})
	badcrc := append(append([]byte{}, rec[:len(rec)-1]...), 0xFF)
	cases := []struct {
		name string
		buf  []byte
		want error
	}{
		{"length prefix short", rec[:2], ErrLengthPrefixIncomplete},
		{"length prefix empty", rec[:0], ErrLengthPrefixIncomplete},
		{"body short", rec[:10], ErrBodyIncomplete},
		{"crc truncated", rec[:len(rec)-2], ErrCRCMismatch},
		{"crc wrong", badcrc, ErrCRCMismatch},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, _, err := DecodeRecord(tc.buf); !errors.Is(err, tc.want) {
				t.Fatalf("got %v, want %v", err, tc.want)
			}
		})
	}
}

func TestHeaderRoundTripAndErrors(t *testing.T) {
	h := Header{FirstSeq: 100, Count: 40}
	got, err := DecodeHeader(EncodeHeader(h))
	if err != nil || got != h {
		t.Fatalf("round trip: %+v %v", got, err)
	}
	if _, err := DecodeHeader(make([]byte, HeaderSize-1)); !errors.Is(err, ErrHeaderIncomplete) {
		t.Fatalf("want ErrHeaderIncomplete, got %v", err)
	}
	bad := EncodeHeader(h)
	bad[0] = 'X'
	if _, err := DecodeHeader(bad); !errors.Is(err, ErrBadHeader) {
		t.Fatalf("want ErrBadHeader, got %v", err)
	}
}

func TestIndexFileWrittenPerSegment(t *testing.T) {
	dir := t.TempDir()
	w, _ := NewWriter(dir, 2, 1)
	for i := 0; i < 5; i++ {
		w.Append([]byte{byte(i)})
	}
	w.Close()
	segs, _ := ListSegments(dir)
	if len(segs) != 3 {
		t.Fatalf("segments = %d, want 3", len(segs))
	}
	for _, p := range segs {
		if _, err := os.Stat(IndexPath(p)); err != nil {
			t.Fatalf("missing index for %s: %v", p, err)
		}
	}
}
