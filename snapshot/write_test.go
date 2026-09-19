package snapshot

import (
	"bytes"
	"errors"
	"testing"
)

func TestWriteDeterministic(t *testing.T) {
	recs := sampleRecords()
	shuffled := []Record{recs[3], recs[1], recs[0], recs[2]}
	a := writeBytes(t, recs)
	b := writeBytes(t, shuffled)
	if !bytes.Equal(a, b) {
		t.Fatal("shuffled input produced different bytes")
	}
}

func TestWriteEmptyRoundTrip(t *testing.T) {
	data := writeBytes(t, nil)
	if len(data) == 0 {
		t.Fatal("empty record set wrote a zero-byte file")
	}
	back, err := Read(bytes.NewReader(data))
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if len(back) != 0 {
		t.Fatalf("expected 0 records, got %d", len(back))
	}
}

func TestWriteSortsByKey(t *testing.T) {
	recs := []Record{
		{Key: "b", Num: 2},
		{Key: "a", Num: 3},
		{Key: "c", Num: 1},
	}
	back, err := Read(bytes.NewReader(writeBytes(t, recs)))
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	want := []string{"a", "b", "c"}
	for i, r := range back {
		if r.Key != want[i] {
			t.Fatalf("record %d key = %q, want %q", i, r.Key, want[i])
		}
	}
}

func TestWriteErrorCountsBytes(t *testing.T) {
	fw := &failWriter{limit: headerSize + 5}
	err := Write(fw, sampleRecords())
	var we *WriteError
	if !errors.As(err, &we) {
		t.Fatalf("expected *WriteError, got %v", err)
	}
	if we.Written != int64(headerSize+5) {
		t.Fatalf("Written = %d, want %d", we.Written, headerSize+5)
	}
	if !errors.Is(err, errWriteBoom) {
		t.Fatalf("underlying error not preserved: %v", err)
	}
}

func TestWriteErrorAtStart(t *testing.T) {
	fw := &failWriter{limit: 0}
	err := Write(fw, sampleRecords())
	var we *WriteError
	if !errors.As(err, &we) {
		t.Fatalf("expected *WriteError, got %v", err)
	}
	if we.Written != 0 {
		t.Fatalf("Written = %d, want 0", we.Written)
	}
	if !errors.Is(err, errWriteBoom) {
		t.Fatalf("underlying error not preserved: %v", err)
	}
}
