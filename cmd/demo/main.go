// Command demo exercises the forward-compatible message parser.
// Run with: go run ./cmd/demo
package main

import (
	"bytes"
	"errors"
	"fmt"
	"os"

	"ontology/message"
	"ontology/unknown"
	"ontology/wire"
)

var pass, fail int

func report(name string, ok bool) {
	if ok {
		pass++
		fmt.Printf("OK   %s\n", name)
	} else {
		fail++
		fmt.Printf("FAIL %s\n", name)
	}
}

func main() {
	lim := message.Limits{}

	// 1. Round trip keeps unknown fields byte-for-byte.
	in := cat(vfield(1, 42), vfield(100, 7), bfield(101, []byte("hi")), mfield(102, vfield(999, 3)))
	m, err := message.Parse(in, demoSchema, lim)
	out, _ := m.Marshal()
	report("round-trip byte-identical with unknown fields", err == nil && bytes.Equal(out, in))

	// 2. Interleaved known/unknown and repeated same-number fields.
	rep := cat(vfield(1, 1), vfield(50, 11), vfield(3, 30), vfield(3, 31), vfield(50, 22), bfield(2, []byte("n")))
	mr, _ := message.Parse(rep, demoSchema, lim)
	ro, _ := mr.Marshal()
	report("interleaved and repeated field order preserved", bytes.Equal(ro, rep) && mr.Unknowns().Len() == 2)

	// 3. Three-level nesting preserves unknowns recursively.
	nested := mfield(5, mfield(1, cat(vfield(1, 9), vfield(88, 8))))
	mn, err := message.Parse(nested, demoSchema, lim)
	no, _ := mn.Marshal()
	deep, _ := mn.Message(5)
	inner, _ := deep.Message(1)
	report("unknown fields retained through 3-level nesting", err == nil && bytes.Equal(no, nested) && inner.Unknowns().Len() == 1)

	// 4-8. The five syntax errors are distinguishable with offsets.
	cases := []struct {
		name string
		buf  []byte
		want error
		off  int
	}{
		{"varint over 10 bytes", bytes.Repeat([]byte{0x80}, 11), wire.ErrVarintTooLong, 0},
		{"unknown wire type byte", []byte{0x01, 0x09}, wire.ErrUnknownWireType, 1},
		{"length exceeds remaining bytes", []byte{0x01, 0x01, 0x05, 'a'}, wire.ErrLengthOverflow, 2},
		{"field number zero", []byte{0x00, 0x00}, wire.ErrFieldNumberZero, 1},
	}
	off := func(e error) int { var pe *wire.ParseError; errors.As(e, &pe); return pe.Offset }
	for _, c := range cases {
		_, e := message.Parse(c.buf, demoSchema, lim)
		report("syntax error: "+c.name, errors.Is(e, c.want) && off(e) == c.off)
	}
	mismatch := []byte{0x04, 0x02, 0x03, 0x63, 0x01, 0x0a}
	mismatch = append(mismatch, []byte("abcdefghij")...)
	_, eNested := message.Parse(mismatch, demoSchema, lim)
	var pe *wire.ParseError
	errors.As(eNested, &pe)
	report("syntax error: nested length mismatch", errors.Is(eNested, wire.ErrNestedLength) && pe.Offset == 0)

	// 9. Errors return zero value, never half results.
	bad := cat(vfield(1, 42), vfield(50, 7), []byte{0x02, 0x01, 0x32})
	half, eBad := message.Parse(bad, demoSchema, lim)
	report("parse failure yields no partial message", eBad != nil && half == nil)

	// 10. Modifying a known field leaves unknowns in place.
	mm, _ := message.Parse(cat(vfield(50, 11), vfield(1, 1), vfield(51, 22)), demoSchema, lim)
	mm.SetVarint(1, 300000000)
	mo, _ := mm.Marshal()
	want := cat(vfield(50, 11), vfield(1, 300000000), vfield(51, 22))
	report("modify known field keeps unknowns positioned", bytes.Equal(mo, want))

	// 11. Deleting a known field preserves surviving relative order.
	md, _ := message.Parse(cat(vfield(50, 11), vfield(1, 1), vfield(51, 22)), demoSchema, lim)
	md.Delete(1)
	do, _ := md.Marshal()
	report("delete known field keeps remaining order", bytes.Equal(do, cat(vfield(50, 11), vfield(51, 22))))

	// 12. All three size/count limits reject immediately.
	_, eSize := message.Parse(bfield(2, make([]byte, 50)), demoSchema, message.Limits{MaxMessageBytes: 10})
	_, eField := message.Parse(bfield(2, make([]byte, 50)), demoSchema, message.Limits{MaxFieldPayloadBytes: 10})
	_, eUnk := message.Parse(cat(vfield(90, 1), vfield(91, 2)), demoSchema, message.Limits{MaxUnknown: 1})
	report("message/field/unknown limits reject at once",
		errors.Is(eSize, wire.ErrSizeLimit) && errors.Is(eField, wire.ErrFieldSizeLimit) && errors.Is(eUnk, unknown.ErrTooManyUnknown))

	// 13. Repeated marshalling is idempotent and non-mutating.
	mi, _ := message.Parse(in, demoSchema, lim)
	a, _ := mi.Marshal()
	b, _ := mi.Marshal()
	report("marshal twice on same struct is identical", bytes.Equal(a, b))

	// 14. Mutating the input buffer does not affect parsed data.
	mut := append([]byte(nil), in...)
	mu, _ := message.Parse(mut, demoSchema, lim)
	for i := range mut {
		mut[i] = 0xff
	}
	muo, _ := mu.Marshal()
	report("rewriting input buffer leaves parsed struct intact", bytes.Equal(muo, in))

	fmt.Printf("TOTAL %d passed, %d failed\n", pass, fail)
	if fail != 0 {
		os.Exit(1)
	}
}
