package wsframe

import (
	"bytes"
	"errors"
	"fmt"
	"log"
	"math/rand"
	"os"
)

var testLogger = log.New(os.Stdout, "wsframe-test: ", log.Lmicroseconds)

// frame builds a standard masked frame with minimal length encoding.
func frame(fin bool, op byte, payload, mask []byte) []byte {
	var b bytes.Buffer
	b0 := op
	if fin {
		b0 |= 0x80
	}
	b.WriteByte(b0)
	switch n := len(payload); {
	case n < 126:
		b.WriteByte(0x80 | byte(n))
	case n <= 65535:
		b.WriteByte(0x80 | 126)
		b.WriteByte(byte(n >> 8))
		b.WriteByte(byte(n))
	default:
		b.WriteByte(0x80 | 127)
		for i := 7; i >= 0; i-- {
			b.WriteByte(byte(n >> (8 * i)))
		}
	}
	b.Write(mask)
	for i, p := range payload {
		b.WriteByte(p ^ mask[i%4])
	}
	return b.Bytes()
}

// rawFrame builds a frame with fully controlled header bytes, allowing
// malformed length encodings. maskedPayload must already be XORed with key.
func rawFrame(b0, b1 byte, extLen, key, maskedPayload []byte) []byte {
	var b bytes.Buffer
	b.WriteByte(b0)
	b.WriteByte(b1)
	b.Write(extLen)
	b.Write(key)
	b.Write(maskedPayload)
	return b.Bytes()
}

func xorBytes(p, key []byte) []byte {
	out := make([]byte, len(p))
	for i := range p {
		out[i] = p[i] ^ key[i%4]
	}
	return out
}

func mkPayload(n int, seed byte) []byte {
	p := make([]byte, n)
	for i := range p {
		p[i] = seed + byte(i*7)
	}
	return p
}

// ---- naive whole-stream reference decoder ----------------------------------

type naiveResult struct {
	events []Event
	off    int64
	reason error
	closed bool
}

var errTruncated = errors.New("naive: truncated")

// naiveDecode is an independent whole-buffer implementation of the same
// protocol, sharing only the error sentinels with the production decoder.
func naiveDecode(maxMessage int, data []byte) naiveResult {
	var res naiveResult
	pos := 0
	frag := false
	var msgOp byte
	var msg []byte

	fail := func(off int, reason error) {
		res.off = int64(off)
		res.reason = reason
	}

	for pos < len(data) {
		frameStart := pos
		if len(data)-pos < 2 {
			fail(frameStart, errTruncated)
			return res
		}
		b0, b1 := data[pos], data[pos+1]
		fin := b0&0x80 != 0
		rsv := b0 & 0x70
		op := b0 & 0x0f
		masked := b1&0x80 != 0
		p7 := int(b1 & 0x7f)

		if rsv != 0 {
			fail(frameStart, ErrRSV)
			return res
		}
		if !legalOpcode(op) {
			fail(frameStart, ErrOpcode)
			return res
		}
		control := op >= 0x8
		if control && !fin {
			fail(frameStart, ErrControlFragment)
			return res
		}
		if !control {
			if frag && op != OpContinuation {
				fail(frameStart, ErrNewDataFragment)
				return res
			}
			if !frag && op == OpContinuation {
				fail(frameStart, ErrUnexpectedCont)
				return res
			}
		}
		if !masked {
			fail(frameStart+1, ErrUnmasked)
			return res
		}

		pos += 2
		var plen int
		switch {
		case p7 < 126:
			plen = p7
		case p7 == 126:
			if pos+2 > len(data) {
				fail(pos, errTruncated)
				return res
			}
			v := int(data[pos])<<8 | int(data[pos+1])
			if v < 126 {
				fail(frameStart+3, ErrLengthEncoding)
				return res
			}
			plen = v
			pos += 2
		default:
			if pos >= len(data) {
				fail(pos, errTruncated)
				return res
			}
			if data[pos]&0x80 != 0 {
				fail(frameStart+2, ErrLength64Bit)
				return res
			}
			// Non-minimal form is decidable once the top six bytes are
			// known to be zero: frameStart + 7.
			if pos+6 > len(data) {
				fail(pos, errTruncated)
				return res
			}
			topSixZero := true
			for i := 0; i < 6; i++ {
				if data[pos+i] != 0 {
					topSixZero = false
					break
				}
			}
			if topSixZero {
				fail(frameStart+7, ErrLengthEncoding)
				return res
			}
			if pos+8 > len(data) {
				fail(pos, errTruncated)
				return res
			}
			var v int64
			for i := 0; i < 8; i++ {
				v = v<<8 | int64(data[pos+i])
			}
			if v <= 65535 {
				fail(frameStart+9, ErrLengthEncoding)
				return res
			}
			plen = int(v)
			pos += 8
		}

		lenEnd := pos
		if control && plen > 125 {
			fail(lenEnd-1, ErrControlTooLong)
			return res
		}
		if !control && len(msg)+plen > maxMessage {
			fail(lenEnd-1, ErrMessageTooLarge)
			return res
		}
		if pos+4+plen > len(data) {
			fail(pos, errTruncated)
			return res
		}
		key := data[pos : pos+4]
		pos += 4
		payload := make([]byte, plen)
		for i := 0; i < plen; i++ {
			payload[i] = data[pos+i] ^ key[i%4]
		}

		if op == OpClose {
			if plen == 1 {
				fail(pos, ErrCloseLength)
				return res
			}
			if plen >= 2 {
				code := int(payload[0])<<8 | int(payload[1])
				if !legalCloseCode(code) {
					fail(pos+1, ErrCloseCode)
					return res
				}
			}
		}

		switch {
		case control:
			switch op {
			case OpPing:
				res.events = append(res.events, Event{Kind: KindPing, Op: op, Payload: payload})
			case OpPong:
				res.events = append(res.events, Event{Kind: KindPong, Op: op, Payload: payload})
			default:
				res.events = append(res.events, Event{Kind: KindClose, Op: op, Payload: payload})
				res.closed = true
			}
		case op == OpContinuation:
			msg = append(msg, payload...)
			if fin {
				res.events = append(res.events, Event{Kind: KindMessage, Op: msgOp, Payload: msg})
				frag = false
				msg = nil
			}
		default:
			if fin {
				res.events = append(res.events, Event{Kind: KindMessage, Op: op, Payload: payload})
			} else {
				frag = true
				msgOp = op
				msg = payload
			}
		}
		pos += plen
		if res.closed {
			return res
		}
	}
	return res
}

// ---- chunked execution ------------------------------------------------------

type chunkedResult struct {
	events []Event
	off    int64
	reason error
	closed bool
}

func runChunked(maxMessage int, data []byte, cuts []int) chunkedResult {
	d := NewDecoder(maxMessage)
	var out chunkedResult
	prev := 0
	feed := func(end int) {
		evs, err := d.Feed(data[prev:end])
		out.events = append(out.events, evs...)
		if err != nil {
			var de *DecodeError
			if errors.As(err, &de) {
				out.off = de.Offset
				out.reason = de.Err
			} else {
				out.reason = err
			}
		}
		prev = end
	}
	for _, c := range cuts {
		feed(c)
		if out.reason != nil {
			return out
		}
	}
	if prev < len(data) {
		feed(len(data))
	}
	if errors.Is(out.reason, ErrClosed) {
		out.reason = nil
		out.closed = true
	} else if out.reason == nil {
		if _, err := d.Feed(nil); errors.Is(err, ErrClosed) {
			out.closed = true
		}
	}
	return out
}

func eventsEqual(a, b []Event) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i].Kind != b[i].Kind || a[i].Op != b[i].Op || !bytes.Equal(a[i].Payload, b[i].Payload) {
			return false
		}
	}
	return true
}

func checkCuts(t testingT, name string, maxMessage int, data []byte, cutSets [][]int) {
	ref := naiveDecode(maxMessage, data)
	testLogger.Printf("case=%s input=%d bytes ref_events=%s ref_reason=%v ref_offset=%d ref_closed=%v",
		name, len(data), summarize(ref.events), ref.reason, ref.off, ref.closed)

	for ci, cuts := range cutSets {
		got := runChunked(maxMessage, data, cuts)
		if !eventsEqual(ref.events, got.events) {
			t.Fatalf("%s chunking#%d %v: events mismatch\n ref=%v\n got=%v",
				name, ci, cuts, summarize(ref.events), summarize(got.events))
		}
		if ref.reason != nil && ref.reason != errTruncated {
			if !errors.Is(got.reason, ref.reason) || got.off != ref.off {
				t.Fatalf("%s chunking#%d %v: error mismatch ref=(%v @%d) got=(%v @%d)",
					name, ci, cuts, ref.reason, ref.off, got.reason, got.off)
			}
		} else if got.reason != nil {
			t.Fatalf("%s chunking#%d %v: unexpected error %v", name, ci, cuts, got.reason)
		}
		if got.closed != ref.closed {
			t.Fatalf("%s chunking#%d %v: closed mismatch ref=%v got=%v",
				name, ci, cuts, ref.closed, got.closed)
		}
	}
	testLogger.Printf("case=%s -> %d chunkings agree (reason=%v offset=%d)",
		name, len(cutSets), ref.reason, ref.off)
}

type testingT interface {
	Fatalf(format string, args ...any)
}

func summarize(evs []Event) string {
	var b bytes.Buffer
	b.WriteByte('[')
	for i, e := range evs {
		if i > 0 {
			b.WriteByte(' ')
		}
		fmt.Fprintf(&b, "%s(op=%d,len=%d)", kindName(e.Kind), e.Op, len(e.Payload))
	}
	b.WriteByte(']')
	return b.String()
}

func kindName(k EventKind) string {
	switch k {
	case KindMessage:
		return "msg"
	case KindPing:
		return "ping"
	case KindPong:
		return "pong"
	case KindClose:
		return "close"
	}
	return "?"
}

func allIndices(n int) []int {
	c := make([]int, 0, n+1)
	for i := 0; i <= n; i++ {
		c = append(c, i)
	}
	return c
}

func randomCuts(n int, rng *rand.Rand) []int {
	var cuts []int
	for i := 1; i < n; i++ {
		if rng.Intn(2) == 0 {
			cuts = append(cuts, i)
		}
	}
	return cuts
}

// smallCuts covers every split point plus random multi-split chunkings.
func smallCuts(n int, rng *rand.Rand) [][]int {
	sets := make([][]int, 0, n+8)
	sets = append(sets, nil, allIndices(n))
	for i := 1; i < n; i++ {
		sets = append(sets, []int{i})
	}
	for k := 0; k < 8; k++ {
		sets = append(sets, randomCuts(n, rng))
	}
	return sets
}

// bigCuts is the cheaper strategy for large payloads: splits around the
// start/end, around every 4096-byte stride, and random multi-splits.
func bigCuts(n int, rng *rand.Rand) [][]int {
	var sets [][]int
	sets = append(sets, nil, allIndices(n))
	add := func(c int) {
		if c > 0 && c < n {
			sets = append(sets, []int{c})
		}
	}
	for i := 0; i <= 20; i++ {
		add(i)
		add(n - i)
	}
	for i := 4096; i < n; i += 4096 {
		for j := -4; j <= 4; j++ {
			add(i + j)
		}
	}
	for k := 0; k < 16; k++ {
		sets = append(sets, randomCuts(n, rng))
	}
	return sets
}
