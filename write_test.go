package snapshot

import (
	"bytes"
	"errors"
	"testing"
)

func sampleRecords() []Record {
	return []Record{
		{Key: "charlie", Value: 30, Note: "three"},
		{Key: "alpha", Value: 10, Note: "one"},
		{Key: "bravo", Value: 20, Note: "two"},
	}
}

func TestRoundTrip(t *testing.T) {
	var buf bytes.Buffer
	if err := Write(&buf, sampleRecords()); err != nil {
		t.Fatalf("write: %v", err)
	}
	got, err := Read(&buf)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	want := []Record{
		{Key: "alpha", Value: 10, Note: "one"},
		{Key: "bravo", Value: 20, Note: "two"},
		{Key: "charlie", Value: 30, Note: "three"},
	}
	if len(got) != len(want) {
		t.Fatalf("got %d records, want %d", len(got), len(want))
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("record %d = %+v, want %+v", i, got[i], want[i])
		}
	}
}

func TestEmptySetRoundTrip(t *testing.T) {
	var buf bytes.Buffer
	if err := Write(&buf, nil); err != nil {
		t.Fatalf("write empty: %v", err)
	}
	if buf.Len() == 0 {
		t.Fatal("empty set produced a zero-byte file")
	}
	if buf.Len() != headerSize {
		t.Fatalf("empty file size = %d, want %d", buf.Len(), headerSize)
	}
	got, err := Read(&buf)
	if err != nil {
		t.Fatalf("read empty: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("empty round trip gave %d records", len(got))
	}
}

func TestDeterministicShuffledWrite(t *testing.T) {
	first := sampleRecords()
	var a bytes.Buffer
	if err := Write(&a, first); err != nil {
		t.Fatal(err)
	}
	shuffled := []Record{first[2], first[0], first[1]}
	var b bytes.Buffer
	if err := Write(&b, shuffled); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(a.Bytes(), b.Bytes()) {
		t.Fatal("shuffled input produced different bytes")
	}
	var c bytes.Buffer
	if err := Write(&c, first); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(a.Bytes(), c.Bytes()) {
		t.Fatal("repeated write was not deterministic")
	}
}

type failWriter struct{ failAfter, written int }

func (f *failWriter) Write(p []byte) (int, error) {
	remaining := f.failAfter - f.written
	if remaining <= 0 {
		return 0, errors.New("disk full")
	}
	if len(p) > remaining {
		f.written += remaining
		return remaining, errors.New("disk full")
	}
	f.written += len(p)
	return len(p), nil
}

func TestWriteErrorReportsBytesAndWrapsCause(t *testing.T) {
	fw := &failWriter{failAfter: headerSize + 3}
	err := Write(fw, sampleRecords())
	if err == nil {
		t.Fatal("expected error")
	}
	var we *WriteError
	if !errors.As(err, &we) {
		t.Fatalf("want *WriteError, got %T: %v", err, err)
	}
	if we.BytesWritten != int64(fw.failAfter) {
		t.Fatalf("bytes written = %d, want %d", we.BytesWritten, fw.failAfter)
	}
	if we.Unwrap() == nil || we.Unwrap().Error() != "disk full" {
		t.Fatalf("underlying error not wrapped: %v", we.Unwrap())
	}
}
