// Command demo runs self-checks of the chunked writer and prints one OK/FAIL
// line per required semantic. It always exits 0 when every check passes.
package main

import (
	"bytes"
	"errors"
	"fmt"

	"ontology/chunked"
)

type boomSink struct{ err error }

func (s *boomSink) Write(p []byte) (int, error) { return 0, s.err }

type shortSink struct{ calls int }

func (s *shortSink) Write(p []byte) (int, error) {
	s.calls++
	if len(p) > 1 {
		return len(p) - 1, nil
	}
	return len(p), nil
}

type countSink struct {
	bytes.Buffer
	calls int
}

func (c *countSink) Write(p []byte) (int, error) {
	c.calls++
	return c.Buffer.Write(p)
}

func report(name string, ok bool) {
	if ok {
		fmt.Printf("OK   %s\n", name)
	} else {
		fmt.Printf("FAIL %s\n", name)
	}
}

func main() {
	var sink bytes.Buffer
	w := chunked.New(&sink, 0)
	n, err := w.Write([]byte("hello"))
	w.Close()
	report("1 exact bytes (5\\r\\nhello\\r\\n0\\r\\n\\r\\n)",
		n == 5 && err == nil && sink.String() == "5\r\nhello\r\n0\r\n\r\n")

	sink.Reset()
	w = chunked.New(&sink, 0)
	w.Write(bytes.Repeat([]byte{'x'}, 26))
	w.Close()
	report("2 lowercase hex without padding (1a)",
		bytes.HasPrefix(sink.Bytes(), []byte("1a\r\n")) &&
			!bytes.Contains(sink.Bytes(), []byte("0x")))

	sink.Reset()
	w = chunked.New(&sink, 4)
	zn, zerr := w.Write(nil)
	en, eerr := w.Write([]byte{})
	report("3 zero-length write emits nothing",
		zn == 0 && zerr == nil && en == 0 && eerr == nil &&
			sink.Len() == 0 && w.Chunks() == 0)

	sink.Reset()
	w = chunked.New(&sink, 4)
	n, err = w.Write([]byte("012345678"))
	w.Close()
	report("4 split over limit into 4+4+1",
		n == 9 && err == nil && w.Chunks() == 3 &&
			sink.String() == "4\r\n0123\r\n4\r\n4567\r\n1\r\n8\r\n0\r\n\r\n")

	cs := &countSink{}
	w = chunked.New(cs, 0)
	c1, c2, c3 := w.Close(), w.Close(), w.Close()
	report("5 Close is idempotent (one terminal chunk)",
		c1 == nil && c2 == nil && c3 == nil && cs.calls == 1 &&
			cs.String() == "0\r\n\r\n")

	cs = &countSink{}
	w = chunked.New(cs, 0)
	w.Close()
	an, aerr := w.Write([]byte("data"))
	report("6 Write after Close returns ErrClosed",
		an == 0 && errors.Is(aerr, chunked.ErrClosed) && cs.calls == 1)

	boom := errors.New("boom")
	w = chunked.New(&boomSink{err: boom}, 0)
	_, f1 := w.Write([]byte("abc"))
	_, f2 := w.Write([]byte("def"))
	f3 := w.Close()
	report("7 sink failure is sticky (ErrSinkFail wraps cause)",
		errors.Is(f1, chunked.ErrSinkFail) && errors.Is(f1, boom) &&
			errors.Is(f2, chunked.ErrSinkFail) && errors.Is(f3, chunked.ErrSinkFail))

	ss := &shortSink{}
	w = chunked.New(ss, 0)
	_, serr := w.Write([]byte("abc"))
	sclose := w.Close()
	report("8 short write is failure and sticky",
		errors.Is(serr, chunked.ErrSinkFail) && errors.Is(sclose, chunked.ErrSinkFail) &&
			ss.calls == 1)
}
