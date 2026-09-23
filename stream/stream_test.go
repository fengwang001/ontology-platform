package stream_test

import (
	"bytes"
	"errors"
	"sync"
	"testing"

	"ontology/addr"
	"ontology/store"
	"ontology/stream"
)

func newStream(t *testing.T, limits store.Limits) *stream.Streamer {
	t.Helper()
	s, err := stream.New(stream.Params{Window: 16, Min: 64, Max: 256, Bits: 6}, limits)
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func feed(s *stream.Streamer, data []byte, cuts []int) {
	prev := 0
	for _, c := range cuts {
		_, _ = s.Write(data[prev:c])
		prev = c
	}
	_, _ = s.Write(data[prev:])
}

func TestWriteIndependenceAndReassemble(t *testing.T) {
	data := make([]byte, 5000)
	for i := range data {
		data[i] = byte(i*1103515245+12345) | 1
	}
	patterns := [][]int{
		nil,
		{1}, // 一次一字节开头后整段
		{7, 7, 100, 100, 3333, 4999},
	}
	var base []stream.Chunk
	for pi, cuts := range patterns {
		s := newStream(t, store.Limits{})
		feed(s, data, cuts)
		if err := s.Flush(); err != nil {
			t.Fatal(err)
		}
		got := s.Chunks()
		if pi == 0 {
			base = got
			continue
		}
		if len(got) != len(base) {
			t.Fatalf("切法 %d 块数不同 %d!=%d", pi, len(got), len(base))
		}
		for i := range base {
			if got[i] != base[i] {
				t.Fatalf("切法 %d 块 %d 逐字段不同: %+v != %+v", pi, i, got[i], base[i])
			}
		}
		out, err := s.Reassemble()
		if err != nil || !bytes.Equal(out, data) {
			t.Fatalf("重组不精确 err=%v eq=%v", err, bytes.Equal(out, data))
		}
		if err := s.SelfCheck(64, 256); err != nil {
			t.Fatal(err)
		}
	}
}

func TestEmptyVsZeroWrite(t *testing.T) {
	empty := newStream(t, store.Limits{})
	if err := empty.Flush(); err != nil {
		t.Fatal(err)
	}
	if len(empty.Chunks()) != 0 {
		t.Fatal("空流必须 0 块")
	}
	out, err := empty.Reassemble()
	if err != nil || len(out) != 0 {
		t.Fatal("空流必须重组为空")
	}
	zero := newStream(t, store.Limits{})
	if _, err := zero.Write(nil); err != nil {
		t.Fatal(err)
	}
	if _, err := zero.Write([]byte{}); err != nil {
		t.Fatal(err)
	}
	if err := zero.Flush(); err != nil {
		t.Fatal(err)
	}
	if zero.Writes() != 2 || empty.Writes() != 0 {
		t.Fatalf("零长写入应可区分: %d vs %d", zero.Writes(), empty.Writes())
	}
	if err := zero.SelfCheck(64, 256); err != nil {
		t.Fatal(err)
	}
}

func TestDedup(t *testing.T) {
	s, err := stream.New(stream.Params{Window: 4, Min: 8, Max: 64, Bits: 6}, store.Limits{})
	if err != nil {
		t.Fatal(err)
	}
	block := bytes.Repeat([]byte("content-defined-chunk"), 2) // 48 字节，不触发谓词
	sep := []byte{0, 0, 0, 0, 0, 0, 0, 0, 0, 0}
	for i := 0; i < 3; i++ {
		_, _ = s.Write(block)
		_, _ = s.Write(sep)
	}
	if err := s.Flush(); err != nil {
		t.Fatal(err)
	}
	if err := s.SelfCheck(8, 64); err != nil {
		t.Fatal(err)
	}
	a := addr.Of(block)
	if s.Store().Refs(a) != 3 {
		t.Fatalf("去重块引用计数=%d，期望 3", s.Store().Refs(a))
	}
	chunks := s.Chunks()
	distinct := map[addr.Addr]bool{}
	for _, c := range chunks {
		distinct[c.Addr] = true
	}
	if !distinct[a] {
		t.Fatal("序列中未找到重复块地址")
	}
}

func TestErrorsDistinctAndNoTrace(t *testing.T) {
	cases := []struct {
		name   string
		params stream.Params
		limits store.Limits
		data   []byte
		want   error
	}{
		{"min>max", stream.Params{16, 256, 64, 6}, store.Limits{}, nil, nil},
		{"window0", stream.Params{0, 64, 256, 6}, store.Limits{}, nil, nil},
		{"window>min", stream.Params{128, 64, 256, 6}, store.Limits{}, nil, nil},
		{"maxChunks", stream.Params{16, 64, 256, 6}, store.Limits{MaxChunks: 1}, bytes.Repeat([]byte{1}, 5000), store.ErrTooManyChunks},
		{"maxBytes", stream.Params{16, 64, 256, 6}, store.Limits{MaxBytes: 100}, bytes.Repeat([]byte{1}, 5000), store.ErrTooManyBytes},
	}
	seen := map[error]bool{}
	for _, c := range cases {
		s, err := stream.New(c.params, c.limits)
		if c.want == nil { // 构造期错误
			if err == nil || seen[err] {
				t.Fatalf("%s: 期望一个新的可判定错误，得到 %v", c.name, err)
			}
			seen[err] = true
			continue
		}
		if err != nil {
			t.Fatal(err)
		}
		_, _ = s.Write(c.data)
		before := s.Store().ChunkCount()
		if err := s.Flush(); !errors.Is(err, c.want) {
			t.Fatalf("%s: err=%v want %v", c.name, err, c.want)
		}
		if s.Store().ChunkCount() != before || s.Store().ByteCount() != 0 {
			t.Fatalf("%s: 失败留痕 chunks=%d bytes=%d", c.name, s.Store().ChunkCount(), s.Store().ByteCount())
		}
		if err := s.Flush(); !errors.Is(err, c.want) {
			t.Fatalf("%s: 拒绝后应仍可使用且同样拒绝，得到 %v", c.name, err)
		}
		seen[c.want] = true
	}
	// 不存在的地址
	s := newStream(t, store.Limits{})
	var missing addr.Addr
	if _, err := s.Store().Get(missing); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("ErrNotFound: %v", err)
	}
	seen[store.ErrNotFound] = true
	if len(seen) != 5 {
		t.Fatalf("五类可判定错误应互不相同，实际 %d 类", len(seen))
	}
}

func TestConcurrentReassemble(t *testing.T) {
	data := make([]byte, 20000)
	for i := range data {
		data[i] = byte(i*2246822519 + 37)
	}
	s := newStream(t, store.Limits{})
	_, _ = s.Write(data)
	if err := s.Flush(); err != nil {
		t.Fatal(err)
	}
	const n = 16
	var wg sync.WaitGroup
	errs := make(chan error, n)
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			out, err := s.Reassemble()
			if err != nil {
				errs <- err
				return
			}
			if !bytes.Equal(out, data) {
				errs <- errors.New("重组结果不一致")
				return
			}
			if err := s.SelfCheck(64, 256); err != nil {
				errs <- err
			}
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Fatal(err)
	}
}
