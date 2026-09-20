// Command demo exercises every semantic guarantee of the framing package
// and prints one OK/FAIL line per guarantee.
package main

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"os"

	"ontology/framing"
)

var failed bool

func check(name string, ok bool) {
	status := "OK"
	if !ok {
		status = "FAIL"
		failed = true
	}
	fmt.Printf("%-28s %s\n", name, status)
}

func encode(frames ...[]byte) []byte {
	var buf bytes.Buffer
	for _, f := range frames {
		var hdr [4]byte
		binary.BigEndian.PutUint32(hdr[:], uint32(len(f)))
		buf.Write(hdr[:])
		buf.Write(f)
	}
	return buf.Bytes()
}

func feedIn(r *framing.Reader, stream []byte, sizes []int) [][]byte {
	var got [][]byte
	for off, i := 0, 0; off < len(stream); i++ {
		n := sizes[i%len(sizes)]
		if n == 0 {
			frames, _ := r.Feed(nil)
			got = append(got, frames...)
			continue
		}
		if off+n > len(stream) {
			n = len(stream) - off
		}
		frames, _ := r.Feed(stream[off : off+n])
		got = append(got, frames...)
		off += n
	}
	return got
}

func equal(a, b [][]byte) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] == nil || !bytes.Equal(a[i], b[i]) {
			return false
		}
	}
	return true
}

func main() {
	payloads := [][]byte{[]byte("hello"), {}, []byte("a longer payload here"), {1, 2, 3}, []byte("x")}
	stream := encode(payloads...)

	// 1. Chunking invariance.
	invariant := true
	for _, sizes := range [][]int{{len(stream)}, {1}, {2, 0, 3}, {7, 1, 0, 13}, {3, 5, 2, 11, 1, 0, 4}} {
		if !equal(feedIn(framing.New(1<<20), stream, sizes), payloads) {
			invariant = false
		}
	}
	check("1 chunking invariance", invariant)

	// 2. Coalesced frames.
	frames, err := framing.New(1 << 20).Feed(encode([]byte("one"), []byte("two"), []byte("three")))
	check("2 coalesced frames", err == nil && equal(frames, [][]byte{[]byte("one"), []byte("two"), []byte("three")}))

	// 3. Partial frames buffered.
	r := framing.New(1 << 20)
	f1, e1 := r.Feed(stream[:2])
	f2, e2 := r.Feed(stream[2:6])
	mid := r.Buffered()
	f3, e3 := r.Feed(stream[6:])
	check("3 partial frame buffering", e1 == nil && e2 == nil && e3 == nil &&
		len(f1) == 0 && len(f2) == 0 && mid == 6 && equal(f3, payloads) && r.Buffered() == 0)

	// 4. Oversized frame is terminal.
	r = framing.New(4)
	_, err = r.Feed(encode([]byte("this payload is way too long")))
	sticky := errors.Is(err, framing.ErrFrameTooLarge)
	buf := r.Buffered()
	for i := 0; i < 3 && sticky; i++ {
		_, err = r.Feed([]byte{9, 9, 9})
		sticky = errors.Is(err, framing.ErrFrameTooLarge) && r.Buffered() == buf
	}
	check("4 oversized frame terminal", sticky)

	// 5. Zero-length frames.
	frames, err = framing.New(1 << 20).Feed(encode([]byte{}, []byte("m"), []byte{}))
	check("5 zero-length frames", err == nil && len(frames) == 3 &&
		frames[0] != nil && len(frames[0]) == 0 && frames[2] != nil && len(frames[2]) == 0)

	// 6. Copy isolation.
	r = framing.New(1 << 20)
	p := encode([]byte("aaaa"))
	frames, _ = r.Feed(p)
	p[len(p)-1] = 'X'
	frames[0][0] = 'Z'
	frames2, _ := r.Feed(encode([]byte("bbbb")))
	check("6 copy isolation", string(frames[0]) == "Zaaa" && string(frames2[0]) == "bbbb")

	// 7. Close semantics.
	r = framing.New(1 << 20)
	_, _ = r.Feed(encode([]byte("done")))
	clean := r.Close() == nil
	r = framing.New(1 << 20)
	_, _ = r.Feed(stream[:3])
	incomplete := errors.Is(r.Close(), framing.ErrIncomplete) && errors.Is(r.Close(), framing.ErrIncomplete)
	_, err = r.Feed([]byte{0})
	check("7 close semantics", clean && incomplete && errors.Is(err, framing.ErrClosed))

	// 8. Bounded buffering.
	r = framing.New(1 << 20)
	chunk, n := encode([]byte("tiny")), 0
	for i := 0; i < 10000; i++ {
		frames, _ = r.Feed(chunk)
		n += len(frames)
	}
	check("8 bounded buffering", n == 10000 && r.Buffered() == 0)

	if failed {
		os.Exit(1)
	}
	fmt.Println("all semantics satisfied")
}
