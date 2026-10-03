package journal

import (
	"bytes"
	"errors"
	"testing"
)

func TestMemorySinkClone(t *testing.T) {
	s := NewMemorySink()
	data := []byte("hello")
	r := Record{Op: OpChunk, Data: data}
	if err := s.Append(r); err != nil {
		t.Fatal(err)
	}
	data[0] = 'X'
	got := s.Records()
	if !bytes.Equal(got[0].Data, []byte("hello")) {
		t.Fatalf("Sink 须防御性拷贝, got %q", got[0].Data)
	}
	got[0].Data[0] = 'Y'
	if s.Records()[0].Data[0] != 'h' {
		t.Fatal("Records() 须返回副本")
	}
}

func TestFaultSinkTable(t *testing.T) {
	inner := NewMemorySink()
	f := NewFaultSink(inner, 1)
	rec := Record{Op: OpChunk}
	if err := f.Append(rec); err != nil {
		t.Fatalf("第0条应成功: %v", err)
	}
	if err := f.Append(rec); !errors.Is(err, ErrAppendFailed) {
		t.Fatalf("第1条应注入失败, got %v", err)
	}
	if err := f.Append(rec); err != nil {
		t.Fatalf("第2条应成功: %v", err)
	}
	if len(f.Records()) != 2 {
		t.Fatalf("失败记录不得落地, got %d 条", len(f.Records()))
	}
}
