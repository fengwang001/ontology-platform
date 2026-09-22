// Command demo exercises the forward-compatible message parser and
// re-writer end to end. It reads no arguments and touches no network.
package main

import (
	"bytes"
	"errors"
	"fmt"
	"os"

	"ontology/message"
	"ontology/wire"
)

var failed int

func check(name string, ok bool) {
	if ok {
		fmt.Printf("OK   %s\n", name)
		return
	}
	fmt.Printf("FAIL %s\n", name)
	failed++
}

func vid(num, v uint64) []byte { return wire.AppendVarintField(nil, num, v) }
func vbytes(num uint64, b []byte) []byte {
	return wire.AppendField(nil, num, wire.Bytes, b)
}
func vmsg(num uint64, b []byte) []byte {
	return wire.AppendField(nil, num, wire.Message, b)
}
func cat(parts ...[]byte) []byte { return bytes.Join(parts, nil) }

func parse(data []byte) (*message.Message, error) {
	return message.Parse(data, message.DefaultLimits())
}

func main() {
	// 1. Round-trip: unknown fields survive byte-for-byte.
	input := cat(vid(1, 150), vid(7, 1), vbytes(2, []byte("hi")), vbytes(9, []byte("zz")))
	m, err := parse(input)
	check("round-trip is byte-identical", err == nil && bytes.Equal(m.Marshal(), input))

	// 2. Interleaved known/unknown and repeated numbers keep their order.
	inter := cat(vbytes(9, []byte("A")), vid(1, 1), vbytes(9, []byte("B")),
		vid(5, 1), vid(5, 2), vbytes(9, []byte("C")), vid(1, 2))
	m, err = parse(inter)
	check("interleaved and repeated fields keep order", err == nil && bytes.Equal(m.Marshal(), inter))

	// 3. Unknown fields inside three nesting levels are preserved.
	inner := cat(vid(5, 1), vid(1, 7))
	deep := cat(vid(1, 1), vmsg(3, cat(vbytes(2, []byte("m")), vmsg(3, inner), vid(6, 6))))
	m, err = parse(deep)
	check("nested unknown fields preserved (3 levels)", err == nil && bytes.Equal(m.Marshal(), deep))

	// 4. The five syntax errors are distinguishable, with offsets.
	bads := map[wire.Kind][]byte{
		wire.KindVarintOverflow:       append(bytes.Repeat([]byte{0x80}, 10), 0x01),
		wire.KindUnknownWireType:      {0x01, 0x07},
		wire.KindLengthOverflow:       {0x02, byte(wire.Bytes), 0x05, 0xAA},
		wire.KindFieldNumberZero:      {0x00, byte(wire.Varint), 0x01},
		wire.KindNestedLengthMismatch: cat([]byte{0x03, byte(wire.Message), 0x02, 0x02, byte(wire.Bytes), 0x05}, []byte("abcde")),
	}
	seen := map[wire.Kind]bool{}
	zeroValue := true
	for kind, data := range bads {
		bm, berr := parse(data)
		var we *wire.Error
		if errors.As(berr, &we) && we.Kind == kind {
			seen[kind] = true
		}
		zeroValue = zeroValue && bm == nil
	}
	check("five syntax errors distinguishable", len(seen) == 5)

	// 5. A failed parse leaks no partial result.
	check("parse error returns zero message", zeroValue)

	// 6. Editing a known field keeps unknown fields in place.
	m, _ = parse(inter)
	m.SetID(99)
	want := cat(vbytes(9, []byte("A")), vid(1, 99), vbytes(9, []byte("B")),
		vid(5, 1), vid(5, 2), vbytes(9, []byte("C")))
	check("edit known field keeps unknowns in place", bytes.Equal(m.Marshal(), want))

	// 7. Deleting a known field keeps unknown relative order.
	m, _ = parse(inter)
	m.ClearID()
	want = cat(vbytes(9, []byte("A")), vbytes(9, []byte("B")),
		vid(5, 1), vid(5, 2), vbytes(9, []byte("C")))
	check("delete known field keeps unknown order", bytes.Equal(m.Marshal(), want))

	// 8. All four limits reject immediately with distinct errors.
	lim := message.DefaultLimits()
	lim.MaxMessageBytes = 4
	_, e1 := message.Parse(cat(vid(1, 1), vid(2, 2), vid(3, 3)), lim)
	lim = message.DefaultLimits()
	lim.MaxPayloadBytes = 2
	_, e2 := message.Parse(vbytes(2, []byte("toolong")), lim)
	lim = message.DefaultLimits()
	lim.MaxUnknownFields = 1
	_, e3 := message.Parse(cat(vid(7, 1), vid(8, 2)), lim)
	lim = message.DefaultLimits()
	lim.MaxDepth = 2
	_, e4 := message.Parse(vmsg(3, vmsg(3, vmsg(3, vid(1, 1)))), lim)
	check("size/payload/unknown/depth limits reject",
		errors.Is(e1, message.ErrMessageTooLarge) &&
			errors.Is(e2, message.ErrPayloadTooLarge) &&
			errors.Is(e3, message.ErrTooManyUnknowns) &&
			errors.Is(e4, message.ErrDepthExceeded))

	// 9. Marshaling twice gives identical bytes.
	m, _ = parse(input)
	check("marshal is idempotent", bytes.Equal(m.Marshal(), m.Marshal()))

	// 10. Mutating the input buffer does not affect the parsed message.
	mutable := cat(vid(1, 42), vbytes(9, []byte("keep")))
	snapshot := append([]byte(nil), mutable...)
	m, _ = parse(mutable)
	for i := range mutable {
		mutable[i] ^= 0xFF
	}
	check("input mutation does not affect parsed message", bytes.Equal(m.Marshal(), snapshot))

	total := 10
	fmt.Printf("==   %d/%d checks passed\n", total-failed, total)
	if failed > 0 {
		os.Exit(1)
	}
}
