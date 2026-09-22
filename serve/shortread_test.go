package serve_test

import (
	"errors"
	"io"
	"testing"

	"ontology/serve"
	"ontology/source"
)

func makeData(n int) []byte {
	b := make([]byte, n)
	for i := range b {
		b[i] = byte(i*31 + 7)
	}
	return b
}

func build(t *testing.T, header string, src source.Source) *serve.Assembler {
	t.Helper()
	a := serve.New(serve.Config{})
	if err := a.Build(header, src); err != nil {
		t.Fatalf("build: %v", err)
	}
	return a
}

func TestShortReadsAreFilled(t *testing.T) {
	data := makeData(40)
	f := source.NewFlaky(source.NewMemory(data))
	f.MaxChunk = 4 // 每次最多 4 字节，且强制若干次只给 1 字节
	f.ShortCalls = map[int]bool{0: true, 2: true, 5: true}

	a := build(t, "bytes=0-39", f)
	got := drainAll(t, a)
	if string(got) != string(data) {
		t.Fatalf("short-read fill mismatch: got %v want %v", got, data)
	}
}

func TestEOFBeforeSatisfiedIsDistinctError(t *testing.T) {
	data := makeData(40)
	f := source.NewFlaky(source.NewMemory(data))
	// 读到一半长度收缩，导致区间无法读满。
	f.MaxChunk = 8
	f.NewLength = 20
	f.ShrinkCall = 1 // 第 0 次读 8 字节后，第 1 次起长度收缩为 20

	a := serve.New(serve.Config{})
	err := a.Build("bytes=0-39", f)
	if !errors.Is(err, source.ErrShortData) {
		t.Fatalf("want ErrShortData, got %v", err)
	}
	// 失败后组装器保持未构建的零状态。
	if a.TotalSize() != 0 || a.Written() != 0 || a.Ranges() != nil {
		t.Fatal("failed build must leave zero state")
	}
}

func TestInjectedReadErrorPropagates(t *testing.T) {
	f := source.NewFlaky(source.NewMemory(makeData(40)))
	f.MaxChunk = 8
	f.FailCall = 1
	a := serve.New(serve.Config{})
	err := a.Build("bytes=0-39", f)
	if !errors.Is(err, io.ErrUnexpectedEOF) {
		t.Fatalf("want injected error, got %v", err)
	}
}
