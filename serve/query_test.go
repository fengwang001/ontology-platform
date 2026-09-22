package serve_test

import (
	"reflect"
	"testing"

	"ontology/serve"
	"ontology/source"
)

func TestZeroValueBeforeBuild(t *testing.T) {
	a := serve.New(serve.Config{})
	if a.TotalSize() != 0 || a.Written() != 0 || a.Ranges() != nil || a.IsMultipart() {
		t.Fatal("assembler must report zero values before build")
	}
}

func TestRepeatedQueriesAreStableAndDoNotAdvance(t *testing.T) {
	data := makeData(80)
	a := build(t, "bytes=0-9,20-29", source.NewMemory(data))

	// 写一半，制造非零游标。
	w := &fixedWriter{step: 4}
	for a.Written() < a.TotalSize()/2 {
		if _, err := a.WriteTo(w); err != nil {
			t.Fatal(err)
		}
	}

	written := a.Written()
	size := a.TotalSize()
	r1 := a.Ranges()
	r2 := a.Ranges()
	m1, m2 := a.IsMultipart(), a.IsMultipart()

	if !reflect.DeepEqual(r1, r2) {
		t.Fatal("two consecutive Ranges() differ")
	}
	if m1 != m2 || a.Written() != written || a.TotalSize() != size {
		t.Fatal("queries must not advance state")
	}

	// 再连查两次，结果必须完全一致。
	if a.Written() != written || a.TotalSize() != size ||
		!reflect.DeepEqual(a.Ranges(), r1) {
		t.Fatal("state drifted across repeated queries")
	}
}

func TestWrittenMatchesTotalAtDone(t *testing.T) {
	data := makeData(40)
	a := build(t, "bytes=0-9,20-29", source.NewMemory(data))
	_ = drainAll(t, a)
	if !a.Done() || a.Written() != a.TotalSize() {
		t.Fatalf("written=%d total=%d", a.Written(), a.TotalSize())
	}
}
