package hdlc

import (
	"bytes"
	"testing"
)

func TestCRC16X25CheckValue(t *testing.T) {
	got := CRC16X25([]byte("123456789"))
	if got != 0x906E {
		t.Fatalf("CRC16X25(123456789) = %#04x, want 0x906e", got)
	}
}

func TestRoundTripAndSharedFlag(t *testing.T) {
	frames := [][]byte{
		[]byte("123456789"),
		{0xFF, 0x01, 0xFE},
	}

	var encoded bytes.Buffer
	encoder := NewEncoder(&encoded, 16)
	for _, frame := range frames {
		if err := encoder.WriteFrame(frame); err != nil {
			t.Fatalf("WriteFrame(%x): %v", frame, err)
		}
	}
	if err := encoder.Flush(); err != nil {
		t.Fatalf("Flush: %v", err)
	}

	bits := bytesToBits(encoded.Bytes())
	if countBitStrings(bits, flagBits()) != 3 {
		t.Fatalf("flag count = %d, want 3; input=%x output bits=%s", countBitStrings(bits, flagBits()), encoded.Bytes(), formatBits(bits))
	}

	decoder := NewDecoder()
	delivered, err := decoder.Write(encoded.Bytes())
	if err != nil {
		t.Fatalf("Write: %v", err)
	}
	assertFrames(t, delivered, frames)
	assertStats(t, decoder.Stats(), Stats{Frames: 2})
}

func TestRejectedFrameDoesNotChangeState(t *testing.T) {
	var rejected bytes.Buffer
	encoder := NewEncoder(&rejected, 2)
	if err := encoder.WriteFrame(nil); err != ErrEmptyPayload {
		t.Fatalf("empty payload error = %v, want %v", err, ErrEmptyPayload)
	}
	if err := encoder.WriteFrame([]byte{1, 2, 3}); err != ErrPayloadTooLong {
		t.Fatalf("long payload error = %v, want %v", err, ErrPayloadTooLong)
	}
	if rejected.Len() != 0 {
		t.Fatalf("rejected writes produced %d bytes", rejected.Len())
	}

	if err := encoder.WriteFrame([]byte{0xAB}); err != nil {
		t.Fatalf("WriteFrame after rejection: %v", err)
	}
	if err := encoder.Flush(); err != nil {
		t.Fatalf("Flush: %v", err)
	}

	var normal bytes.Buffer
	normalEncoder := NewEncoder(&normal, 2)
	if err := normalEncoder.WriteFrame([]byte{0xAB}); err != nil {
		t.Fatalf("normal WriteFrame: %v", err)
	}
	if err := normalEncoder.Flush(); err != nil {
		t.Fatalf("normal Flush: %v", err)
	}
	if !bytes.Equal(rejected.Bytes(), normal.Bytes()) {
		t.Fatalf("rejected encoder output=%x, want %x; rejection changed shared state", rejected.Bytes(), normal.Bytes())
	}
}

func bytesToBits(data []byte) []byte {
	bits := make([]byte, 0, len(data)*8)
	for _, b := range data {
		for i := 0; i < 8; i++ {
			bits = append(bits, (b>>i)&1)
		}
	}
	return bits
}

func bitsToTestBytes(bits []byte) []byte {
	result := make([]byte, (len(bits)+7)/8)
	for i, bit := range bits {
		result[i/8] |= bit << (i % 8)
	}
	return result
}

func flagBits() []byte {
	return []byte{0, 1, 1, 1, 1, 1, 1, 0}
}

func countBitStrings(bits, want []byte) int {
	count := 0
	for i := 0; i+len(want) <= len(bits); i++ {
		match := true
		for j := range want {
			if bits[i+j] != want[j] {
				match = false
				break
			}
		}
		if match {
			count++
		}
	}
	return count
}

func formatBits(bits []byte) string {
	result := make([]byte, len(bits))
	for i, bit := range bits {
		result[i] = '0' + bit
	}
	return string(result)
}

func assertFrames(t *testing.T, got, want [][]byte) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("frames=%x, want %x", got, want)
	}
	for i := range want {
		if !bytes.Equal(got[i], want[i]) {
			t.Fatalf("frame %d=%x, want %x", i, got[i], want[i])
		}
	}
}

func assertStats(t *testing.T, got, want Stats) {
	t.Helper()
	if got != want {
		t.Fatalf("stats=%+v, want %+v", got, want)
	}
}
