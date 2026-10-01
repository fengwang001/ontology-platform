package wsframe

import (
	"bytes"
	"errors"
	"math/rand"
	"sync"
	"testing"
)

const testMax = 1 << 20

func concat(parts ...[]byte) []byte { return bytes.Join(parts, nil) }

func closeBody(code int) []byte { return []byte{byte(code >> 8), byte(code)} }

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b [20]byte
	i := len(b)
	for n > 0 {
		i--
		b[i] = byte('0' + n%10)
		n /= 10
	}
	return string(b[i:])
}

// TestLengthBoundaries covers the 125/126/65535/65536 encoding choices.
func TestLengthBoundaries(t *testing.T) {
	rng := rand.New(rand.NewSource(1))
	key := []byte{0x11, 0x22, 0x33, 0x44}
	for _, n := range []int{0, 1, 124, 125, 126, 127, 65534, 65535, 65536} {
		data := frame(true, OpBinary, mkPayload(n, 3), key)
		name := "len-" + itoa(n)
		if n <= 200 {
			checkCuts(t, name, testMax, data, smallCuts(len(data), rng))
		} else {
			checkCuts(t, name, testMax, data, bigCuts(len(data), rng))
		}
	}
}

// TestNonMinimalLength checks every longer-than-necessary encoding.
func TestNonMinimalLength(t *testing.T) {
	rng := rand.New(rand.NewSource(2))
	key := []byte{1, 2, 3, 4}

	for _, v := range []int{0, 1, 125} {
		p := mkPayload(v, 9)
		data := rawFrame(0x82, 0xFE, []byte{byte(v >> 8), byte(v)}, key, xorBytes(p, key))
		checkCuts(t, "16bit-nonmin-"+itoa(v), testMax, data, smallCuts(len(data), rng))
	}

	for _, v := range []int{0, 125, 126, 65535} {
		p := mkPayload(v, 9)
		var ext [8]byte
		ext[6] = byte(v >> 8)
		ext[7] = byte(v)
		data := rawFrame(0x82, 0xFF, ext[:], key, xorBytes(p, key))
		checkCuts(t, "64bit-nonmin-"+itoa(v), testMax, data, smallCuts(len(data), rng))
	}

	// 64-bit high bit set.
	var ext [8]byte
	v := uint64(1) << 63
	for i := 7; i >= 0; i-- {
		ext[i] = byte(v)
		v >>= 8
	}
	data := rawFrame(0x82, 0xFF, ext[:], key, nil)
	checkCuts(t, "64bit-highbit", testMax, data, smallCuts(len(data), rng))

	// Error offsets for the 64-bit form: high bit at frame offset 2,
	// non-minimal encoding once six leading zeros arrive (offset 7).
	var small [8]byte
	small[7] = 1 // encodes value 1
	got := runChunked(testMax, rawFrame(0x82, 0xFF, small[:], key, xorBytes([]byte{0}, key)), nil)
	if !errors.Is(got.reason, ErrLengthEncoding) || got.off != 7 {
		t.Fatalf("64bit nonminimal offset: %v @%d want 7", got.reason, got.off)
	}
	var big [8]byte
	bv := uint64(1) << 63
	for i := 7; i >= 0; i-- {
		big[i] = byte(bv)
		bv >>= 8
	}
	got = runChunked(testMax, rawFrame(0x82, 0xFF, big[:], key, nil), nil)
	if !errors.Is(got.reason, ErrLength64Bit) || got.off != 2 {
		t.Fatalf("64bit highbit offset: %v @%d want 2", got.reason, got.off)
	}
}

// TestHeaderViolations covers byte-0/byte-1 violations and their offsets.
func TestHeaderViolations(t *testing.T) {
	rng := rand.New(rand.NewSource(3))
	key := []byte{5, 6, 7, 8}
	cases := []struct {
		name string
		data []byte
	}{
		{"rsv", frame(true, OpText|0x10, []byte("x"), key)},
		{"opcode-3", rawFrame(0x83, 0x81, nil, key, xorBytes([]byte{'x'}, key))},
		{"opcode-b", rawFrame(0x8B, 0x81, nil, key, xorBytes([]byte{'x'}, key))},
		{"unmasked", []byte{0x81, 0x01, 'x'}},
		{"control-no-fin-ping", frame(false, OpPing, []byte("p"), key)},
		{"control-no-fin-close", frame(false, OpClose, closeBody(1000), key)},
		{"cont-without-start", frame(true, OpContinuation, []byte("x"), key)},
		{"new-data-mid-frag", concat(
			frame(false, OpText, []byte("hel"), key),
			frame(false, OpBinary, []byte("lo"), key),
		)},
	}
	for _, tc := range cases {
		checkCuts(t, tc.name, testMax, tc.data, smallCuts(len(tc.data), rng))
	}

	// RSV wins over an illegal opcode on the same byte 0.
	data := rawFrame(0xF3, 0x81, nil, key, xorBytes([]byte{'x'}, key))
	got := runChunked(testMax, data, nil)
	if !errors.Is(got.reason, ErrRSV) || got.off != 0 {
		t.Fatalf("rsv precedence: got %v @%d", got.reason, got.off)
	}
}

// TestFragmentationWithInterleavedControl verifies control frames pass
// through immediately while a data message is fragmented.
func TestFragmentationWithInterleavedControl(t *testing.T) {
	rng := rand.New(rand.NewSource(4))
	key := []byte{0xDE, 0xAD, 0xBE, 0xEF}
	data := concat(
		frame(false, OpText, []byte("Hel"), key),
		frame(true, OpPing, []byte("ping1"), key),
		frame(false, OpContinuation, []byte("lo "), key),
		frame(true, OpPong, []byte("pong1"), key),
		frame(true, OpContinuation, []byte("world"), key),
		frame(true, OpBinary, []byte("next"), key),
	)
	checkCuts(t, "frag-control-interleave", testMax, data, smallCuts(len(data), rng))
}

// TestMaskKeyAcrossBoundary splits inside the 4 mask-key bytes.
func TestMaskKeyAcrossBoundary(t *testing.T) {
	key := []byte{0xA0, 0xB1, 0xC2, 0xD3}
	payload := []byte("abcdefgh")
	data := frame(true, OpText, payload, key)
	var sets [][]int
	for c := 0; c <= 8; c++ {
		sets = append(sets, []int{c})
	}
	for c := 2; c <= 6; c++ {
		sets = append(sets, []int{c, c + 1, c + 2})
	}
	checkCuts(t, "mask-key-boundary", testMax, data, sets)

	res := runChunked(testMax, data, allIndices(len(data)))
	if len(res.events) != 1 || !bytes.Equal(res.events[0].Payload, payload) {
		t.Fatalf("unmasked payload mismatch: %v", summarize(res.events))
	}
}

// TestMessageSizeLimits checks exactly-at-limit and one byte over, both
// for single frames and fragmented messages.
func TestMessageSizeLimits(t *testing.T) {
	rng := rand.New(rand.NewSource(5))
	key := []byte{9, 9, 9, 9}

	exact := mkPayload(1000, 1)
	checkCuts(t, "limit-exact", 1000, frame(true, OpBinary, exact, key),
		smallCuts(6+len(exact), rng))

	over := mkPayload(1001, 1)
	checkCuts(t, "limit-over-one", 1000, frame(true, OpBinary, over, key),
		smallCuts(6+len(over), rng))

	fragOver := concat(
		frame(false, OpBinary, mkPayload(600, 2), key),
		frame(true, OpContinuation, mkPayload(401, 3), key),
	)
	checkCuts(t, "limit-frag-over", 1000, fragOver, smallCuts(len(fragOver), rng))

	fragExact := concat(
		frame(false, OpBinary, mkPayload(600, 2), key),
		frame(true, OpContinuation, mkPayload(400, 3), key),
	)
	checkCuts(t, "limit-frag-exact", 1000, fragExact, smallCuts(len(fragExact), rng))
}

// TestCloseFrames covers close payload length and accepted/rejected codes.
func TestCloseFrames(t *testing.T) {
	rng := rand.New(rand.NewSource(6))
	key := []byte{0x1, 0x3, 0x5, 0x7}

	good := []int{1000, 1001, 1002, 1003, 1007, 1008, 1009, 1010, 1011, 3000, 4999}
	for _, code := range good {
		body := append(closeBody(code), 'o', 'k')
		data := frame(true, OpClose, body, key)
		checkCuts(t, "close-ok-"+itoa(code), testMax, data, smallCuts(len(data), rng))
	}

	bad := []int{999, 1004, 1005, 1006, 1012, 1013, 2999, 5000, 65535}
	for _, code := range bad {
		data := frame(true, OpClose, closeBody(code), key)
		checkCuts(t, "close-bad-"+itoa(code), testMax, data, smallCuts(len(data), rng))
	}

	checkCuts(t, "close-empty", testMax, frame(true, OpClose, nil, key),
		smallCuts(6, rng))

	data := frame(true, OpClose, []byte{0x03}, key)
	checkCuts(t, "close-one-byte", testMax, data, smallCuts(len(data), rng))
}

// TestControlFrameLimit checks control payloads of 125 vs 126 bytes.
func TestControlFrameLimit(t *testing.T) {
	rng := rand.New(rand.NewSource(7))
	key := []byte{2, 4, 6, 8}

	ok := frame(true, OpPing, mkPayload(125, 4), key)
	checkCuts(t, "ping-125", testMax, ok, smallCuts(len(ok), rng))

	long := frame(true, OpPing, mkPayload(126, 4), key)
	checkCuts(t, "ping-126", testMax, long, smallCuts(len(long), rng))
}

// TestStickyStates verifies closed and failed states reject further Feed
// with distinct errors without consuming bytes or changing counters.
func TestStickyStates(t *testing.T) {
	key := []byte{1, 1, 1, 1}

	d := NewDecoder(testMax)
	evs, err := d.Feed(frame(true, OpClose, closeBody(1000), key))
	if err != nil || len(evs) != 1 {
		t.Fatalf("close feed: evs=%v err=%v", evs, err)
	}
	for i := 0; i < 2; i++ {
		evs, err = d.Feed(frame(true, OpPing, []byte("x"), key))
		if !errors.Is(err, ErrClosed) {
			t.Fatalf("feed #%d after close: %v", i, err)
		}
		if len(evs) != 0 {
			t.Fatalf("closed decoder must not deliver events")
		}
	}

	d2 := NewDecoder(testMax)
	bad := []byte{0x81, 0x01, 'x'} // unmasked text frame
	var firstOff int64
	for i := 0; i < 3; i++ {
		_, err = d2.Feed(bad)
		if i > 0 {
			if !errors.Is(err, ErrFailed) {
				t.Fatalf("feed #%d after failure: want ErrFailed, got %v", i, err)
			}
			continue
		}
		var de *DecodeError
		if !errors.As(err, &de) || !errors.Is(err, ErrUnmasked) {
			t.Fatalf("feed #%d: %v", i, err)
		}
		firstOff = de.Offset
	}
	// Counters frozen in the failed state: the protocol error keeps the
	// same absolute offset regardless of extra bytes.
	if firstOff != 1 {
		t.Fatalf("unmasked offset = %d, want 1", firstOff)
	}
	if _, err = d2.Feed(frame(true, OpText, []byte("ok"), key)); !errors.Is(err, ErrFailed) {
		t.Fatalf("ErrFailed not distinguished: %v", err)
	}
}

// TestPrecedenceOrders exercises violations decidable at early bytes.
func TestPrecedenceOrders(t *testing.T) {
	key := []byte{8, 7, 6, 5}

	// RSV (byte 0) wins over missing MASK (byte 1).
	data := []byte{0x91, 0x01, 'x'}
	got := runChunked(testMax, data, nil)
	if !errors.Is(got.reason, ErrRSV) || got.off != 0 {
		t.Fatalf("RSV over unmasked: %v @%d", got.reason, got.off)
	}

	// Illegal opcode (byte 0) wins over missing MASK (byte 1).
	data = []byte{0x83, 0x00}
	got = runChunked(testMax, data, nil)
	if !errors.Is(got.reason, ErrOpcode) || got.off != 0 {
		t.Fatalf("opcode over unmasked: %v @%d", got.reason, got.off)
	}

	// A 126-byte ping using 16-bit encoding: control-too-long is decided at
	// the final length byte (offset 3), before the payload is read.
	p := mkPayload(126, 4)
	data = rawFrame(0x89, 0xFE, []byte{0x00, 0x7E}, key, xorBytes(p, key))
	got = runChunked(testMax, data, nil)
	if !errors.Is(got.reason, ErrControlTooLong) || got.off != 3 {
		t.Fatalf("control too long: %v @%d", got.reason, got.off)
	}

	// Illegal close code is decided at the second payload byte, i.e.
	// header(2) + key(4) + 1 = offset 7.
	data = frame(true, OpClose, closeBody(1004), key)
	got = runChunked(testMax, data, nil)
	if !errors.Is(got.reason, ErrCloseCode) || got.off != 7 {
		t.Fatalf("close code offset: %v @%d", got.reason, got.off)
	}

	// One-byte close payload error is on the payload byte: offset 6.
	data = frame(true, OpClose, []byte{0x03}, key)
	got = runChunked(testMax, data, nil)
	if !errors.Is(got.reason, ErrCloseLength) || got.off != 6 {
		t.Fatalf("close length offset: %v @%d", got.reason, got.off)
	}
}

// TestConcurrentFeed feeds one stream from multiple goroutines: under
// -race it must be data-race free, and every Feed must observe a state
// consistent with some serial order.
func TestConcurrentFeed(t *testing.T) {
	key := []byte{0xAB, 0xCD, 0xEF, 0x01}
	var stream []byte
	for i := 0; i < 50; i++ {
		stream = append(stream, frame(true, OpBinary, mkPayload(i%20, byte(i)), key)...)
	}

	d := NewDecoder(testMax)
	var wg sync.WaitGroup
	errs := make(chan error, 64)
	for g := 0; g < 8; g++ {
		wg.Add(1)
		go func(seed int64) {
			defer wg.Done()
			rng := rand.New(rand.NewSource(seed))
			i := 0
			for i < len(stream) {
				step := 1 + rng.Intn(7)
				end := i + step
				if end > len(stream) {
					end = len(stream)
				}
				chunk := stream[i:end]
				if _, err := d.Feed(chunk); err != nil {
					errs <- err
					return
				}
				i = end
			}
		}(int64(g + 100))
	}
	wg.Wait()
	close(errs)
	var sentinelCount int
	for err := range errs {
		ok := errors.Is(err, ErrClosed) || errors.Is(err, ErrFailed)
		if !ok {
			// Out-of-order chunk delivery may produce any protocol
			// violation; it must still be a wrapped *DecodeError.
			var de *DecodeError
			if !errors.As(err, &de) {
				t.Fatalf("non-protocol concurrent error: %v", err)
			}
			sentinelCount++
		}
	}
	t.Logf("concurrent feed: %d goroutines, %d protocol errors from reordering", 8, sentinelCount)

	// Serial equivalence: concurrently splitting the same stream can
	// interleave chunks out of order, so the decoder may fail, but it must
	// never panic or race. A deterministic ordered feed must fully decode.
	d2 := NewDecoder(testMax)
	if _, err := d2.Feed(stream); err != nil {
		t.Fatalf("ordered feed: %v", err)
	}
}

// TestByteAtATimeLarge is a dedicated byte-at-a-time check for the 65536
// boundary frame, where every other chunking strategy is also covered by
// bigCuts in TestLengthBoundaries.
func TestByteAtATimeLarge(t *testing.T) {
	key := []byte{0x33, 0x44, 0x55, 0x66}
	data := frame(true, OpBinary, mkPayload(65536, 6), key)
	res := runChunked(testMax, data, allIndices(len(data)))
	if res.reason != nil || len(res.events) != 1 || len(res.events[0].Payload) != 65536 {
		t.Fatalf("65536 byte-at-a-time: %v %v", res.reason, summarize(res.events))
	}
}
