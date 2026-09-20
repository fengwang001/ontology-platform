// Command demo exercises the chunked.Writer semantics and prints an
// OK/FAIL verdict for each one.
package main

import (
	"bytes"
	"errors"
	"fmt"
	"io"

	"ontology/chunked"
)

var failures int

func check(name string, ok bool) {
	verdict := "OK"
	if !ok {
		verdict = "FAIL"
		failures++
	}
	fmt.Printf("%-34s %s\n", name, verdict)
}

func encode(maxChunk int, writes ...[]byte) (string, *chunked.Writer) {
	var buf bytes.Buffer
	w := chunked.New(&buf, maxChunk)
	for _, p := range writes {
		_, _ = w.Write(p)
	}
	_ = w.Close()
	return buf.String(), w
}

func main() {
	// 1. Exact bytes.
	got, _ := encode(0, []byte("hello"))
	check("1 exact bytes", got == "5\r\nhello\r\n0\r\n\r\n")

	// 2. Zero-length writes produce no chunk.
	var buf2 bytes.Buffer
	w2 := chunked.New(&buf2, 0)
	n1, e1 := w2.Write(nil)
	n2, e2 := w2.Write([]byte{})
	check("2 zero-length write no-op",
		n1 == 0 && e1 == nil && n2 == 0 && e2 == nil &&
			buf2.Len() == 0 && w2.Chunks() == 0)

	// 3. Split by maxChunk.
	got3, w3 := encode(4, []byte("abcdefghi"))
	check("3 split 9 into 4+4+1",
		got3 == "4\r\nabcd\r\n4\r\nefgh\r\n1\r\ni\r\n0\r\n\r\n" &&
			w3.Chunks() == 3)

	// 4. Close idempotent.
	var buf4 bytes.Buffer
	w4 := chunked.New(&buf4, 0)
	ok4 := w4.Close() == nil && w4.Close() == nil && w4.Close() == nil
	check("4 close idempotent", ok4 && buf4.String() == "0\r\n\r\n")

	// 5. Write after Close.
	n5, e5 := w4.Write([]byte("x"))
	check("5 write after close",
		n5 == 0 && errors.Is(e5, chunked.ErrClosed) &&
			buf4.String() == "0\r\n\r\n")

	// 6. Sticky sink failure.
	sinkErr := errors.New("boom")
	w6 := chunked.New(failSink{sinkErr}, 0)
	_, errW := w6.Write([]byte("abc"))
	errC := w6.Close()
	check("6 sticky sink failure",
		errors.Is(errW, chunked.ErrSinkFail) && errors.Is(errW, sinkErr) &&
			errors.Is(errC, chunked.ErrSinkFail))

	// 7. Short write is a failure.
	w7 := chunked.New(shortSink(2), 0)
	_, err7 := w7.Write([]byte("hello"))
	check("7 short write fails", errors.Is(err7, chunked.ErrSinkFail))

	// 8. Input not mutated, deterministic output.
	in := []byte("hello")
	g1, _ := encode(2, in)
	g2, _ := encode(2, in)
	check("8 no mutation, deterministic",
		string(in) == "hello" && g1 == g2)

	fmt.Printf("---\n%d/8 semantics OK\n", 8-failures)
}

type failSink struct{ err error }

func (s failSink) Write([]byte) (int, error) { return 0, s.err }

type shortSink int

func (s shortSink) Write(p []byte) (int, error) {
	if int(s) < len(p) {
		return int(s), nil
	}
	return len(p), nil
}

var _ io.Writer = failSink{}
