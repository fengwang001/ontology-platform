package hdlc

import (
	"bytes"
	"math/rand"
	"sync"
	"testing"
)

// stuffedBitLen returns the on-wire bit length of the frame content
// (payload + FCS) after bit stuffing.
func stuffedBitLen(payload []byte) int {
	fcs := crc16X25(payload)
	content := make([]byte, 0, len(payload)+2)
	content = append(content, payload...)
	content = append(content, byte(fcs), byte(fcs>>8))
	n, ones := 0, 0
	for _, b := range content {
		for i := 0; i < 8; i++ {
			bit := (b >> i) & 1
			n++
			if bit == 1 {
				ones++
				if ones == 5 {
					n++
					ones = 0
				}
			} else {
				ones = 0
			}
		}
	}
	return n
}

// findPayload returns a deterministic 2-byte payload whose stuffed content bit
// length mod 8 equals wantMod.
func findPayload(wantMod int) []byte {
	for v := 0; v < 1<<16; v++ {
		p := []byte{byte(v), byte(v >> 8)}
		if stuffedBitLen(p)%8 == wantMod {
			return p
		}
	}
	panic("unreachable")
}

func encodeAll(t *testing.T, maxPayload int, frames [][]byte, flushBetween, flushEnd bool) []byte {
	t.Helper()
	var buf bytes.Buffer
	enc := NewEncoder(&buf, maxPayload)
	for i, p := range frames {
		if err := enc.WriteFrame(p); err != nil {
			t.Fatalf("WriteFrame %d: %v", i, err)
		}
		if flushBetween {
			if err := enc.Flush(); err != nil {
				t.Fatalf("Flush: %v", err)
			}
		}
	}
	if flushEnd && !flushBetween {
		if err := enc.Flush(); err != nil {
			t.Fatalf("Flush: %v", err)
		}
	}
	return buf.Bytes()
}

func decodeAll(t *testing.T, stream []byte, chunk int) ([][]byte, Stats) {
	t.Helper()
	dec := NewDecoder()
	var frames [][]byte
	for i := 0; i < len(stream); i += chunk {
		end := i + chunk
		if end > len(stream) {
			end = len(stream)
		}
		frames = append(frames, dec.Write(stream[i:end])...)
	}
	return frames, dec.Stats()
}

func framesEqual(a, b [][]byte) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if !bytes.Equal(a[i], b[i]) {
			return false
		}
	}
	return true
}

func TestCRCCheckValue(t *testing.T) {
	if got := crc16X25([]byte("123456789")); got != 0x906E {
		t.Fatalf("crc16X25(\"123456789\") = %#04x, want 0x906E", got)
	}
	t.Logf("crc16X25(\"123456789\") = 0x906E as required by CRC-16/X-25")
}

func TestEncoderRejectsInvalidPayload(t *testing.T) {
	var buf bytes.Buffer
	enc := NewEncoder(&buf, 4)
	if err := enc.WriteFrame(nil); err != ErrEmptyPayload {
		t.Fatalf("empty payload: got %v", err)
	}
	if err := enc.WriteFrame([]byte{1, 2, 3, 4, 5}); err != ErrPayloadTooLarge {
		t.Fatalf("oversized payload: got %v", err)
	}
	if buf.Len() != 0 {
		t.Fatalf("rejected frames emitted %d bytes", buf.Len())
	}
	// State must be untouched: the next valid frame still gets exactly one
	// leading flag, matching a fresh naive stream.
	p := []byte{0x11, 0x22}
	if err := enc.WriteFrame(p); err != nil {
		t.Fatal(err)
	}
	if err := enc.Flush(); err != nil {
		t.Fatal(err)
	}
	naive := &naiveEncoder{}
	naive.frame(p)
	naive.flush()
	want := packBitsLSB(naive.bits)
	if !bytes.Equal(buf.Bytes(), want) {
		t.Fatalf("after rejections stream mismatch:\n got %08b\nwant %08b", buf.Bytes(), want)
	}
	t.Logf("rejected empty/oversized payloads emitted nothing; next frame stream = %08b", buf.Bytes())
}

func TestStuffingTrailingFiveOnes(t *testing.T) {
	// Find a payload whose FCS high byte is 0xF8: its bits end with 11111000,
	// so the frame content ends with exactly five 1 bits.
	var payload []byte
	for v := 0; ; v++ {
		p := []byte{byte(v), byte(v >> 8), 0x5A}
		if crc16X25(p)>>8 == 0xF8 {
			payload = p
			break
		}
	}
	fcs := crc16X25(payload)
	t.Logf("payload=%x fcs=%04x (high byte 0xF8 -> content ends with exactly five 1s)", payload, fcs)

	naive := &naiveEncoder{}
	naive.frame(payload)
	tail := naive.bits[len(naive.bits)-14:]
	wantTail := []int{1, 1, 1, 1, 1, 0, 0, 1, 1, 1, 1, 1, 1, 0}
	for i := range wantTail {
		if tail[i] != wantTail[i] {
			t.Fatalf("stream tail %v, want ...11111 0(stuff) 01111110(flag)", tail)
		}
	}
	t.Logf("stream tail bits %v = five 1s + inserted stuff 0 + flag 01111110", tail)

	stream := encodeAll(t, DefaultMaxPayload, [][]byte{payload}, false, true)
	naive.flush()
	if !bytes.Equal(stream, packBitsLSB(naive.bits)) {
		t.Fatalf("encoder bits differ from naive reference")
	}
	frames, stats := decodeAll(t, stream, 1<<20)
	if len(frames) != 1 || !bytes.Equal(frames[0], payload) {
		t.Fatalf("roundtrip failed: frames=%x stats=%+v", frames, stats)
	}
	// Account for the flush padding: 7 pad 1s are one abort, 1-5 pad 1s become
	// a bad-length drop, 0 or 6 pad 1s are idle.
	want := Stats{FramesDelivered: 1}
	switch pad := (8 - stuffedBitLen(payload)%8) % 8; pad {
	case 7:
		want.Aborts = 1
	case 1, 2, 3, 4, 5:
		want.DroppedBadLength = 1
	}
	if stats != want {
		t.Fatalf("stats=%+v, want %+v", stats, want)
	}
	t.Logf("roundtrip ok: delivered %x, stats=%+v", frames[0], stats)
}

func TestAllFFPayload(t *testing.T) {
	payload := bytes.Repeat([]byte{0xFF}, 32)
	naive := &naiveEncoder{}
	naive.frame(payload)
	raw := (len(payload) + 2) * 8
	stuffed := len(naive.bits) - 16 // minus the two flags
	t.Logf("payload=32x0xFF raw content=%d bits, stuffed=%d bits (%d stuff bits)",
		raw, stuffed, stuffed-raw)
	if stuffed <= raw {
		t.Fatalf("expected stuff bits for all-0xFF payload")
	}
	// The stuffed content region must never contain six consecutive 1s.
	content := naive.bits[8 : len(naive.bits)-8]
	run := 0
	for _, b := range content {
		if b == 1 {
			run++
			if run >= 6 {
				t.Fatalf("stuffed content contains %d consecutive 1s", run)
			}
		} else {
			run = 0
		}
	}
	stream := encodeAll(t, DefaultMaxPayload, [][]byte{payload}, false, true)
	naive.flush()
	if !bytes.Equal(stream, packBitsLSB(naive.bits)) {
		t.Fatalf("encoder bits differ from naive reference")
	}
	frames, stats := decodeAll(t, stream, 1<<20)
	if len(frames) != 1 || !bytes.Equal(frames[0], payload) {
		t.Fatalf("roundtrip failed: stats=%+v", stats)
	}
	t.Logf("roundtrip ok, stats=%+v", stats)
}

func TestSharedFlagBetweenAdjacentFrames(t *testing.T) {
	p1 := []byte{0xDE, 0xAD}
	p2 := []byte{0xBE, 0xEF, 0x01}
	s1, s2 := stuffedBitLen(p1), stuffedBitLen(p2)
	stream := encodeAll(t, DefaultMaxPayload, [][]byte{p1, p2}, false, true)
	// Exactly one flag between the frames: 8 + s1 + 8 + s2 + 8 bits total.
	wantBits := 8 + s1 + 8 + s2 + 8
	pad := (8 - wantBits%8) % 8
	if got := len(stream) * 8; got != wantBits+pad {
		t.Fatalf("stream=%d bits, want %d (adjacent frames must share one flag)", got, wantBits)
	}
	t.Logf("p1=%x p2=%x: total %d bits = flag(8)+%d+flag(8)+%d+flag(8); adjacent frames share one flag",
		p1, p2, wantBits, s1, s2)
	frames, stats := decodeAll(t, stream, 1<<20)
	if !framesEqual(frames, [][]byte{p1, p2}) {
		t.Fatalf("frames=%x, want [%x %x]", frames, p1, p2)
	}
	if stats != (Stats{FramesDelivered: 2}) {
		t.Fatalf("stats=%+v", stats)
	}
}

func TestAbortCutsFrameAndResync(t *testing.T) {
	good := []byte{0x42, 0x43}
	naive := &naiveEncoder{}
	naive.frame(good)

	var bits []int
	// A partial flag cut off by an abort sequence.
	bits = append(bits, 0, 1, 1, 1)
	bits = append(bits, 1, 1, 1, 1, 1, 1, 1) // seven 1s -> abort
	// A frame whose content is interrupted mid-way by an abort.
	bits = append(bits, naiveFlag...)
	bits = append(bits, 0, 0, 1, 1, 0, 1)    // fragment of content
	bits = append(bits, 1, 1, 1, 1, 1, 1, 1) // seven 1s -> abort
	// A complete valid frame afterwards proves resynchronization.
	bits = append(bits, naive.bits...)
	t.Logf("input bits: partial flag + abort + partial frame + abort + valid frame (%x)", good)

	stream := packBitsLSB(bits)
	frames, stats := decodeAll(t, stream, 1<<20)
	if len(frames) != 1 || !bytes.Equal(frames[0], good) {
		t.Fatalf("frames=%x, want [%x]", frames, good)
	}
	if stats.Aborts != 2 {
		t.Fatalf("Aborts=%d, want 2 (stats=%+v)", stats.Aborts, stats)
	}
	if stats.DroppedBadLength != 0 || stats.DroppedTooShort != 0 || stats.DroppedBadFCS != 0 {
		t.Fatalf("aborted frames must not be counted as drops: %+v", stats)
	}
	t.Logf("delivered=%x stats=%+v: aborts drop the partial frames, decoder resyncs on next flag", frames, stats)
}

func TestIdlePadOnesNotAFrame(t *testing.T) {
	// Raw level: flag + six 1s + flag. The six 1s merge with the neighboring
	// flag bits into shared-zero flags; content between them is zero bits.
	var bits []int
	bits = append(bits, naiveFlag...)
	bits = append(bits, 1, 1, 1, 1, 1, 1)
	bits = append(bits, naiveFlag...)
	frames, stats := decodeAll(t, packBitsLSB(bits), 1<<20)
	if len(frames) != 0 || stats != (Stats{}) {
		t.Fatalf("idle ones produced frames=%x stats=%+v, want none", frames, stats)
	}
	t.Logf("flag + six 1s + flag: idle, stats=%+v (no frame, no drop, no abort)", stats)

	// Encoder level: pick a payload whose frame ends 2 bits into a byte, so
	// Flush pads exactly six 1s before the next frame's opening flag.
	p1 := findPayload(2)
	p2 := findPayload(0) // final flush pads nothing
	stream := encodeAll(t, DefaultMaxPayload, [][]byte{p1}, false, true)
	s2 := encodeAll(t, DefaultMaxPayload, [][]byte{p2}, false, true)
	stream = append(stream, s2...)
	frames, stats = decodeAll(t, stream, 1<<20)
	if !framesEqual(frames, [][]byte{p1, p2}) {
		t.Fatalf("frames=%x, want [%x %x]", frames, p1, p2)
	}
	if stats != (Stats{FramesDelivered: 2}) {
		t.Fatalf("six pad 1s must be idle, got stats=%+v", stats)
	}
	t.Logf("p1=%x p2=%x: flush padded six 1s between frames; delivered both, stats=%+v", p1, p2, stats)
}

func TestFlushPadSevenOnesCountsAbort(t *testing.T) {
	// Frame ends 1 bit into a byte -> Flush pads exactly seven 1s -> one abort.
	p1 := findPayload(1)
	p2 := findPayload(0)
	stream := encodeAll(t, DefaultMaxPayload, [][]byte{p1}, false, true)
	s2 := encodeAll(t, DefaultMaxPayload, [][]byte{p2}, false, true)
	stream = append(stream, s2...)
	frames, stats := decodeAll(t, stream, 1<<20)
	if !framesEqual(frames, [][]byte{p1, p2}) {
		t.Fatalf("frames=%x, want [%x %x]", frames, p1, p2)
	}
	if stats.Aborts != 1 {
		t.Fatalf("Aborts=%d, want 1 (stats=%+v)", stats.Aborts, stats)
	}
	t.Logf("p1=%x p2=%x: flush padded seven 1s -> exactly one abort, both frames delivered, stats=%+v",
		p1, p2, stats)
}

func TestDropReasons(t *testing.T) {
	good := []byte{0x99}
	goodNaive := &naiveEncoder{}
	goodNaive.frame(good)
	// The good frame's leading flag is supplied by the preceding bits.
	goodBits := goodNaive.bits[8:]

	cases := []struct {
		name    string
		content []int // raw bits between two flags
		check   func(t *testing.T, s Stats)
		why     string
	}{
		{
			name:    "bad length",
			content: []int{1, 0, 1},
			check: func(t *testing.T, s Stats) {
				t.Helper()
				if s.DroppedBadLength != 1 {
					t.Fatalf("stats=%+v", s)
				}
			},
			why: "3 content bits after unstuffing, not a multiple of 8",
		},
		{
			name:    "too short",
			content: make([]int, 16),
			check: func(t *testing.T, s Stats) {
				t.Helper()
				if s.DroppedTooShort != 1 {
					t.Fatalf("stats=%+v", s)
				}
			},
			why: "16 zero bits = 2 bytes < 3 (need payload + 2-byte FCS)",
		},
		{
			name:    "bad FCS",
			content: unpackBitsLSB([]byte{0x01, 0x00, 0x00}, 24),
			check: func(t *testing.T, s Stats) {
				t.Helper()
				if s.DroppedBadFCS != 1 {
					t.Fatalf("stats=%+v", s)
				}
			},
			why: "payload 0x01 with FCS 0x0000 does not match crc16X25",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var bits []int
			bits = append(bits, naiveFlag...)
			bits = append(bits, tc.content...)
			bits = append(bits, naiveFlag...)
			bits = append(bits, goodBits...) // valid frame must still sync afterwards
			frames, stats := decodeAll(t, packBitsLSB(bits), 1<<20)
			tc.check(t, stats)
			if len(frames) != 1 || !bytes.Equal(frames[0], good) {
				t.Fatalf("frames=%x, want [%x] (decoder must resync after drop)", frames, good)
			}
			t.Logf("input content bits=%v -> dropped: %s; then delivered %x; stats=%+v",
				tc.content, tc.why, good, stats)
		})
	}
}

func TestChunkingInvariance(t *testing.T) {
	payloads := [][]byte{
		{0x00}, {0xFF, 0xFF, 0xFF}, findPayload(1), findPayload(2), {0x55, 0xAA},
	}
	// Include flushes so the stream contains pad-1 idle/abort events too.
	stream := encodeAll(t, DefaultMaxPayload, payloads, true, true)
	wantFrames, wantStats := decodeAll(t, stream, 1<<20)
	t.Logf("stream=%d bytes, whole-read: frames=%d stats=%+v", len(stream), len(wantFrames), wantStats)
	for _, chunk := range []int{1, 2, 3, 5, 7, 13} {
		frames, stats := decodeAll(t, stream, chunk)
		if !framesEqual(frames, wantFrames) || stats != wantStats {
			t.Fatalf("chunk=%d: frames=%x stats=%+v, want frames=%x stats=%+v",
				chunk, frames, stats, wantFrames, wantStats)
		}
		t.Logf("chunk=%d: identical frames and stats=%+v", chunk, stats)
	}
}

func TestConcurrentEncoder(t *testing.T) {
	var buf bytes.Buffer
	enc := NewEncoder(&buf, DefaultMaxPayload)
	want := map[string]bool{}
	var mu sync.Mutex
	var wg sync.WaitGroup
	for g := 0; g < 8; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			for i := 0; i < 25; i++ {
				p := []byte{byte(g), byte(i), 0xA5}
				if err := enc.WriteFrame(p); err != nil {
					t.Error(err)
					return
				}
				mu.Lock()
				want[string(p)] = true
				mu.Unlock()
			}
		}(g)
	}
	wg.Wait()
	if err := enc.Flush(); err != nil {
		t.Fatal(err)
	}
	frames, stats := decodeAll(t, buf.Bytes(), 1<<20)
	if len(frames) != len(want) || stats.FramesDelivered != len(want) {
		t.Fatalf("delivered %d frames, want %d (stats=%+v)", len(frames), len(want), stats)
	}
	for _, f := range frames {
		if !want[string(f)] {
			t.Fatalf("unexpected frame %x", f)
		}
	}
	t.Logf("8 goroutines x 25 frames: all %d delivered exactly once, stats=%+v", len(want), stats)
}

func TestConcurrentDecoder(t *testing.T) {
	// Each unit is a whole byte-aligned frame (stuffed length 0 mod 8), so any
	// serial order of concurrent Writes decodes to the same set of frames.
	unitPayload := findPayload(0)
	unit := encodeAll(t, DefaultMaxPayload, [][]byte{unitPayload}, false, false)
	if len(unit)*8 != 8+stuffedBitLen(unitPayload)+8 {
		t.Fatalf("unit not byte aligned")
	}
	dec := NewDecoder()
	var mu sync.Mutex
	var frames [][]byte
	var wg sync.WaitGroup
	const goroutines = 8
	for g := 0; g < goroutines; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			var stream []byte
			for i := 0; i < 10; i++ {
				stream = append(stream, unit...)
			}
			got := dec.Write(stream)
			mu.Lock()
			frames = append(frames, got...)
			mu.Unlock()
		}()
	}
	wg.Wait()
	stats := dec.Stats()
	if len(frames) != goroutines*10 || stats != (Stats{FramesDelivered: goroutines * 10}) {
		t.Fatalf("frames=%d stats=%+v, want %d frames", len(frames), stats, goroutines*10)
	}
	for _, f := range frames {
		if !bytes.Equal(f, unitPayload) {
			t.Fatalf("bad frame %x", f)
		}
	}
	t.Logf("%d goroutines x 10 aligned frames: %d delivered, stats=%+v", goroutines, len(frames), stats)
}

func TestRandomizedAgainstNaive(t *testing.T) {
	rng := rand.New(rand.NewSource(20261001))
	var buf bytes.Buffer
	enc := NewEncoder(&buf, 64)
	naiveEnc := &naiveEncoder{}
	var payloads [][]byte
	for op := 0; op < 300; op++ {
		if rng.Intn(4) == 0 {
			if err := enc.Flush(); err != nil {
				t.Fatal(err)
			}
			naiveEnc.flush()
			t.Logf("op %d: flush (pad with 1s to byte boundary)", op)
			continue
		}
		p := make([]byte, 1+rng.Intn(40))
		if _, err := rng.Read(p); err != nil {
			t.Fatal(err)
		}
		payloads = append(payloads, p)
		if err := enc.WriteFrame(p); err != nil {
			t.Fatal(err)
		}
		naiveEnc.frame(p)
		t.Logf("op %d: frame payload=%x", op, p)
	}
	if err := enc.Flush(); err != nil {
		t.Fatal(err)
	}
	naiveEnc.flush()
	stream := buf.Bytes()
	if want := packBitsLSB(naiveEnc.bits); !bytes.Equal(stream, want) {
		t.Fatalf("encoder bit stream differs from naive reference:\n got %08b\nwant %08b", stream, want)
	}
	t.Logf("encoder output %d bytes identical to naive bit-level reference", len(stream))

	// Decode with random chunking and compare against the naive decoder.
	dec := NewDecoder()
	naiveDec := &naiveDecoder{}
	var frames [][]byte
	prev := Stats{}
	for i := 0; i < len(stream); {
		n := 1 + rng.Intn(9)
		if i+n > len(stream) {
			n = len(stream) - i
		}
		got := dec.Write(stream[i : i+n])
		frames = append(frames, got...)
		now := dec.Stats()
		for _, f := range got {
			t.Logf("chunk [%d,%d): delivered frame %x", i, i+n, f)
		}
		if now.Aborts != prev.Aborts {
			t.Logf("chunk [%d,%d): abort (7 consecutive 1s), Aborts=%d", i, i+n, now.Aborts)
		}
		if now.DroppedBadLength != prev.DroppedBadLength {
			t.Logf("chunk [%d,%d): drop, content bits not a multiple of 8", i, i+n)
		}
		if now.DroppedTooShort != prev.DroppedTooShort {
			t.Logf("chunk [%d,%d): drop, content shorter than 3 bytes", i, i+n)
		}
		if now.DroppedBadFCS != prev.DroppedBadFCS {
			t.Logf("chunk [%d,%d): drop, FCS mismatch", i, i+n)
		}
		prev = now
		i += n
	}
	for _, b := range stream {
		for i := 0; i < 8; i++ {
			naiveDec.feedBit(int((b >> i) & 1))
		}
	}
	stats := dec.Stats()
	if !framesEqual(frames, naiveDec.frames) {
		t.Fatalf("frames differ from naive decoder:\n got %x\nwant %x", frames, naiveDec.frames)
	}
	if stats.Aborts != naiveDec.aborts || stats.DroppedBadLength != naiveDec.badLen ||
		stats.DroppedTooShort != naiveDec.tooShort || stats.DroppedBadFCS != naiveDec.badFCS ||
		stats.FramesDelivered != len(naiveDec.frames) {
		t.Fatalf("stats=%+v, naive: aborts=%d badLen=%d tooShort=%d badFCS=%d frames=%d",
			stats, naiveDec.aborts, naiveDec.badLen, naiveDec.tooShort, naiveDec.badFCS, len(naiveDec.frames))
	}
	if !framesEqual(frames, payloads) {
		t.Fatalf("delivered frames do not match the encoded payloads")
	}
	t.Logf("final: %d frames delivered and identical to naive decoder; stats=%+v", len(frames), stats)
}
