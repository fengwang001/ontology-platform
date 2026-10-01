package hdlc

import (
	"bytes"
	"testing"
)

func TestBitStuffingBoundaries(t *testing.T) {
	payload := []byte{0x9F}

	var encoded bytes.Buffer
	encoder := NewEncoder(&encoded, 1)
	if err := encoder.WriteFrame(payload); err != nil {
		t.Fatal(err)
	}
	if err := encoder.Flush(); err != nil {
		t.Fatal(err)
	}

	bits := bytesToBits(encoded.Bytes())
	t.Logf("input payload=% x; output bytes=% x; output bits=%s", payload, encoded.Bytes(), formatBits(bits))
	if !containsBits(bits, []byte{1, 1, 1, 1, 1, 0, 0}) {
		t.Fatalf("missing stuffed zero after final five ones: %s", formatBits(bits))
	}

	decoder := NewDecoder()
	frames, err := decoder.Write(encoded.Bytes())
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("decision: delivered=%x stats=%+v; reason=%s", frames, decoder.Stats(), "exact five trailing ones are stuffed, not merged into closing flag; seven trailing flush ones count once as abort")
	assertFrames(t, frames, [][]byte{payload})
	assertStats(t, decoder.Stats(), Stats{Frames: 1, Aborts: 1})
}

func TestAllOnesPayload(t *testing.T) {
	payload := []byte{0xFF, 0xFF, 0xFF, 0xFF}

	var encoded bytes.Buffer
	encoder := NewEncoder(&encoded, len(payload))
	if err := encoder.WriteFrame(payload); err != nil {
		t.Fatal(err)
	}
	if err := encoder.Flush(); err != nil {
		t.Fatal(err)
	}

	bits := bytesToBits(encoded.Bytes())
	t.Logf("input all-ones payload=% x; output bits=%s; decision: verify runs of six transmitted ones have inserted zeroes", payload, formatBits(bits))

	decoder := NewDecoder()
	frames := decodeInChunks(t, decoder, encoded.Bytes(), 1)
	assertFrames(t, frames, [][]byte{payload})
	assertStats(t, decoder.Stats(), Stats{Frames: 1})
}

func TestAbortCutsFrameAndResynchronizes(t *testing.T) {
	validPayload := []byte{0x42}
	bits := []byte{}
	bits = append(bits, flagBits()...)
	bits = append(bits, bytesToBits([]byte{0xFF})...)
	bits = append(bits, trimThroughLastFlag(completeFrameBits(t, validPayload))...)

	data := bitsToTestBytes(padBits(bits))
	t.Logf("input bits=%s; output=% x; decision: seven consecutive ones abort discarded frame, later exact flag resynchronizes", formatBits(bits), data)

	decoder := NewDecoder()
	frames := decodeInChunks(t, decoder, data, 1)
	assertFrames(t, frames, [][]byte{validPayload})
	assertStats(t, decoder.Stats(), Stats{Frames: 1, Aborts: 1})
}

func TestIdleOnesAreNotFrames(t *testing.T) {
	bits := []byte{}
	bits = append(bits, trimThroughLastFlag(completeFrameBits(t, []byte{7}))...)
	bits = append(bits, flagBits()[1:]...)

	data := bitsToTestBytes(padBits(bits))
	t.Logf("input bits=%s; output=% x; decision: zero-bit idle between adjacent flags is ignored and does not count as a frame", formatBits(bits), data)

	decoder := NewDecoder()
	frames := decodeInChunks(t, decoder, data, 1)
	t.Logf("idle decision frames=%x stats=%+v", frames, decoder.Stats())
	assertFrames(t, frames, [][]byte{{7}})
	assertStats(t, decoder.Stats(), Stats{Frames: 1})
}

func TestDropReasons(t *testing.T) {
	alignment := frameAroundBits([]byte{
		0, 1, 0, 1, 0, 1, 0, 1,
		0, 0, 1, 0, 1, 0, 1, 0,
		0, 0,
	})

	short := []byte{0, 1, 0, 1, 0, 1, 0, 1, 0, 1, 0, 1, 0, 1, 0, 1}
	short = frameAroundBits(short)

	fcsPayload := []byte{0x00}
	wrongFCS := uint16(0x0001)
	fcs := frameAroundBits(contentBitsWithFCS(fcsPayload, wrongFCS))

	tests := []struct {
		name   string
		bits   []byte
		reason string
		want   Stats
	}{
		{
			name:   "alignment",
			bits:   alignment,
			reason: "destuffed content has 9 bits",
			want:   Stats{AlignmentErrors: 1},
		},
		{
			name:   "short",
			bits:   short,
			reason: "destuffed content has 2 bytes, fewer than payload plus two FCS bytes",
			want:   Stats{ShortFrames: 1},
		},
		{
			name:   "fcs",
			bits:   fcs,
			reason: "content is aligned and at least three bytes but CRC-16/X-25 does not match",
			want:   Stats{FCSErrors: 1},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			data := bitsToTestBytes(padBits(test.bits))
			t.Logf("case=%s input bits=%s bytes=% x; output frames=<none>; decision: %s", test.name, formatBits(test.bits), data, test.reason)

			decoder := NewDecoder()
			frames := decodeInChunks(t, decoder, data, 1)
			if len(frames) != 0 {
				t.Fatalf("frames=%x, want none", frames)
			}
			assertStats(t, decoder.Stats(), test.want)
		})
	}
}

func TestChunkingInvariance(t *testing.T) {
	var encoded bytes.Buffer
	encoder := NewEncoder(&encoded, 16)
	payloads := [][]byte{{0x1F}, {0xFF, 0xFF}, {0, 1, 2, 3}}
	for _, payload := range payloads {
		if err := encoder.WriteFrame(payload); err != nil {
			t.Fatal(err)
		}
	}
	if err := encoder.Flush(); err != nil {
		t.Fatal(err)
	}

	reference := decodeInChunks(t, NewDecoder(), encoded.Bytes(), len(encoded.Bytes()))
	for chunkSize := 1; chunkSize <= len(encoded.Bytes()); chunkSize++ {
		decoder := NewDecoder()
		got := decodeInChunks(t, decoder, encoded.Bytes(), chunkSize)
		if !frameSlicesEqual(got, reference) {
			t.Fatalf("chunk size %d frames=%x, want %x", chunkSize, got, reference)
		}
		assertStats(t, decoder.Stats(), Stats{Frames: uint64(len(payloads))})
	}
}

func contentBits(payload []byte) []byte {
	fcs := CRC16X25(payload)
	data := append(append([]byte(nil), payload...), byte(fcs), byte(fcs>>8))
	return bytesToBits(data)
}

func encodedFrameBits(t *testing.T, payload []byte) []byte {
	t.Helper()
	var encoded bytes.Buffer
	encoder := NewEncoder(&encoded, len(payload))
	if err := encoder.WriteFrame(payload); err != nil {
		t.Fatal(err)
	}
	bits := bytesToBits(encoded.Bytes())
	return bits[8:]
}

func completeFrameBits(t *testing.T, payload []byte) []byte {
	t.Helper()
	return bytesToBits(encodedFrameBytes(t, payload))
}

func frameWithoutStartBits(t *testing.T, payload []byte) []byte {
	t.Helper()
	bits := encodedFrameBytes(t, payload)
	bitsData := bytesToBits(bits)
	return bitsData[8:]
}

func frameAroundBits(content []byte) []byte {
	bits := append([]byte{}, flagBits()...)
	bits = append(bits, content...)
	bits = append(bits, flagBits()...)
	return bits
}

func frameAroundRawBits(rawWithEndFlag []byte) []byte {
	bits := append([]byte{}, flagBits()...)
	bits = append(bits, rawWithEndFlag...)
	return bits
}

func trimThroughLastFlag(bits []byte) []byte {
	flag := flagBits()
	for i := len(bits) - len(flag); i >= 0; i-- {
		if bytes.Equal(bits[i:i+len(flag)], flag) {
			return bits[:i+len(flag)]
		}
	}
	return bits
}

func encodedFrameBytes(t *testing.T, payload []byte) []byte {
	t.Helper()
	var encoded bytes.Buffer
	encoder := NewEncoder(&encoded, len(payload))
	if err := encoder.WriteFrame(payload); err != nil {
		t.Fatal(err)
	}
	if err := encoder.Flush(); err != nil {
		t.Fatal(err)
	}
	return encoded.Bytes()
}

func contentBitsWithFCS(payload []byte, fcs uint16) []byte {
	data := append(append([]byte(nil), payload...), byte(fcs), byte(fcs>>8))
	return bytesToBits(data)
}

func containsBits(haystack, needle []byte) bool {
	for i := 0; i+len(needle) <= len(haystack); i++ {
		if bytes.Equal(haystack[i:i+len(needle)], needle) {
			return true
		}
	}
	return false
}

func padBits(bits []byte) []byte {
	padded := append([]byte{}, bits...)
	for len(padded)%8 != 0 {
		padded = append(padded, 1)
	}
	return padded
}

func decodeInChunks(t *testing.T, decoder *Decoder, data []byte, chunkSize int) [][]byte {
	t.Helper()
	var frames [][]byte
	if chunkSize <= 0 {
		chunkSize = 1
	}
	for offset := 0; offset < len(data); offset += chunkSize {
		end := min(offset+chunkSize, len(data))
		delivered, err := decoder.Write(data[offset:end])
		if err != nil {
			t.Fatalf("Write(%x): %v", data[offset:end], err)
		}
		frames = append(frames, delivered...)
	}
	return frames
}

func frameSlicesEqual(left, right [][]byte) bool {
	if len(left) != len(right) {
		return false
	}
	for i := range left {
		if !bytes.Equal(left[i], right[i]) {
			return false
		}
	}
	return true
}
