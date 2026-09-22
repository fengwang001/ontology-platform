package chunked

import (
	"bytes"
	"sync"
	"testing"
)

func writeClose(d *Decoder, wire []byte) error {
	off := 0
	for off < len(wire) {
		n, err := d.Write(wire[off:])
		off += n
		if err != nil {
			return err
		}
	}
	return d.Close()
}

func TestChunkBoundaryStrictness(t *testing.T) {
	cases := []struct {
		name string
		wire string
		kind Kind
	}{
		{"one byte too many", "3\r\nabcd\r\n", KindMissingCRLF},
		{"one byte too few", "3\r\nab\r\n", KindMissingCRLF},
		{"bare lf after data", "3\r\nabc\n", KindMissingCRLF},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			for size := 1; size <= len(tc.wire); size++ {
				d := New()
				err := writeClose(d, []byte(tc.wire))
				ce, ok := AsError(err)
				if !ok || ce.Kind != tc.kind {
					t.Fatalf("size %d: got %v", size, err)
				}
			}
		})
	}
}

func TestLimitsRejectImmediatelyAndPreserveBody(t *testing.T) {
	t.Run("size line", func(t *testing.T) {
		d := NewWithLimits(Limits{MaxSizeLine: 3})
		err := writeClose(d, []byte("000a\r\n"))
		ce, _ := AsError(err)
		if ce == nil || ce.Kind != KindLineTooLong {
			t.Fatalf("got %v", err)
		}
		if len(d.Body()) != 0 {
			t.Fatal("body must stay empty")
		}
	})

	t.Run("chunk too large", func(t *testing.T) {
		d := NewWithLimits(Limits{MaxChunk: 3})
		err := writeClose(d, []byte("4\r\nabcd\r\n0\r\n\r\n"))
		ce, _ := AsError(err)
		if ce == nil || ce.Kind != KindChunkTooLarge {
			t.Fatalf("got %v", err)
		}
		if got := d.Body(); len(got) != 0 {
			t.Fatalf("body must not receive oversized chunk data, got %q", got)
		}
	})

	t.Run("body too large", func(t *testing.T) {
		d := NewWithLimits(Limits{MaxBody: 4})
		err := writeClose(d, []byte("3\r\nabc\r\n2\r\nde\r\n0\r\n\r\n"))
		ce, _ := AsError(err)
		if ce == nil || ce.Kind != KindBodyTooLarge {
			t.Fatalf("got %v", err)
		}
		if got := d.Body(); !bytes.Equal(got, []byte("abc")) {
			t.Fatalf("body must keep only accepted bytes, got %q", got)
		}
	})

	t.Run("trailers", func(t *testing.T) {
		d := NewWithLimits(Limits{MaxTrailers: 1})
		err := writeClose(d, []byte("0\r\nA: 1\r\nB: 2\r\n\r\n"))
		ce, _ := AsError(err)
		if ce == nil || ce.Kind != KindTooManyTrailers {
			t.Fatalf("got %v", err)
		}
	})
}

func TestLimitErrorIsSticky(t *testing.T) {
	d := NewWithLimits(Limits{MaxBody: 1})
	err := writeClose(d, []byte("2\r\nhi\r\n"))
	ce, _ := AsError(err)
	if ce == nil || ce.Kind != KindBodyTooLarge {
		t.Fatalf("got %v", err)
	}
	body := d.Body()
	again, err2 := d.Write([]byte("more"))
	if again != 0 || err2 != err {
		t.Fatalf("sticky: n=%d same=%v", again, err2 == err)
	}
	if !bytes.Equal(d.Body(), body) {
		t.Fatal("body changed after terminal limit error")
	}
}

func TestCloseStates(t *testing.T) {
	cases := []struct {
		name string
		wire string
		kind Kind
	}{
		{"in size line", "1a;x", KindIncompleteHeader},
		{"in data", "4\r\nab", KindIncompleteData},
		{"waiting CRLF", "4\r\nabcd", KindIncompleteCRLF},
		{"half CRLF", "4\r\nabcd\r", KindHalfCRLF},
		{"in trailers", "0\r\nX: y\r\n", KindIncompleteTrailers},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			d := New()
			err := writeClose(d, []byte(tc.wire))
			ce, _ := AsError(err)
			if ce == nil || ce.Kind != tc.kind {
				t.Fatalf("got %v want %d", err, tc.kind)
			}
		})
	}
}

func TestConcurrentIndependentDecoders(t *testing.T) {
	wire, want := sampleMessage()
	var wg sync.WaitGroup
	for g := 0; g < 32; g++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			size := 1 + id%len(wire)
			d, body, err := feed(t, wire, size)
			if err != nil || !d.Done() || !bytes.Equal(body, want) {
				t.Errorf("goroutine %d: err=%v done=%v match=%v",
					id, err, d.Done(), bytes.Equal(body, want))
			}
		}(g)
	}
	wg.Wait()
}
