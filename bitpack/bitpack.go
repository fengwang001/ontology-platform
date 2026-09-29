// Package bitpack encodes fixed-width unsigned integers into a compact byte
// stream. Bit order is least-significant bit first; words are little endian.
package bitpack

import (
	"encoding/binary"
	"errors"
)

var (
	ErrInvalidWidth  = errors.New("bitpack: width must be 1..64")
	ErrValueOverflow = errors.New("bitpack: value exceeds width")
	ErrShortBuffer   = errors.New("bitpack: truncated packed block")
)

// PackedLen returns the exact number of bytes needed for n values.
func PackedLen(n, width int) int {
	if n <= 0 || width <= 0 {
		return 0
	}
	return ((n * width) + 7) / 8
}

func mask(width int) uint64 {
	if width == 64 {
		return ^uint64(0)
	}
	return uint64(1)<<width - 1
}

// Encode packs values using width bits each.
func Encode(values []uint64, width int) ([]byte, error) {
	if width < 1 || width > 64 {
		return nil, ErrInvalidWidth
	}
	m := mask(width)
	words := make([]uint64, 0, (len(values)*width+63)/64)
	for i, value := range values {
		if value&^m != 0 {
			return nil, ErrValueOverflow
		}
		bit := uint(i * width)
		word := int(bit / 64)
		shift := bit % 64
		for len(words) <= word {
			words = append(words, 0)
		}
		words[word] |= value << shift
		if shift > 64-uint(width) {
			for len(words) <= word+1 {
				words = append(words, 0)
			}
			words[word+1] |= value >> (64 - shift)
		}
	}
	out := make([]byte, PackedLen(len(values), width))
	for i, word := range words {
		base := i * 8
		if base >= len(out) {
			break
		}
		if base+8 <= len(out) {
			binary.LittleEndian.PutUint64(out[base:base+8], word)
			continue
		}
		var buf [8]byte
		binary.LittleEndian.PutUint64(buf[:], word)
		copy(out[base:], buf[:])
	}
	return out, nil
}

// Decode unpacks n values from data. Exact padding bits are tolerated but the
// caller supplies the logical count, so padding can never become a value.
func Decode(data []byte, n, width int) ([]uint64, error) {
	if width < 1 || width > 64 {
		return nil, ErrInvalidWidth
	}
	if n < 0 {
		return nil, ErrShortBuffer
	}
	if n == 0 {
		return []uint64{}, nil
	}
	if len(data) < PackedLen(n, width) {
		return nil, ErrShortBuffer
	}
	m := mask(width)
	words := make([]uint64, (n*width+63)/64)
	for i := range words {
		base := i * 8
		if base+8 <= len(data) {
			words[i] = binary.LittleEndian.Uint64(data[base : base+8])
			continue
		}
		var buf [8]byte
		copy(buf[:], data[base:])
		words[i] = binary.LittleEndian.Uint64(buf[:])
	}
	out := make([]uint64, n)
	for i := range out {
		bit := uint(i * width)
		word := int(bit / 64)
		shift := bit % 64
		out[i] = words[word] >> shift
		if shift > 64-uint(width) {
			out[i] |= words[word+1] << (64 - shift)
		}
		out[i] &= m
	}
	return out, nil
}
