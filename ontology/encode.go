package ontology

import (
	"encoding/binary"
)

// hybridEncode applies the RLE / bit-packed run split described in the
// package spec. indices must all be representable in w bits. Every read index
// increments *touches.
func hybridEncode(indices []int, w int, touches *int) []byte {
	if len(indices) == 0 {
		return nil
	}

	var out []byte
	var p []int

	emitBitPack := func() {
		if len(p) == 0 {
			return
		}
		groups := len(p) / 8
		if len(p)%8 != 0 {
			groups++
		}
		hdr := make([]byte, binary.MaxVarintLen64)
		n := binary.PutUvarint(hdr, uint64(groups<<1|1))
		out = append(out, hdr[:n]...)

		payload := make([]byte, groups*w)
		if w > 0 {
			for gi := 0; gi < groups; gi++ {
				var acc uint64
				var nb uint
				bitOff := gi * w
				for j := 0; j < 8; j++ {
					idx := 0
					pos := gi*8 + j
					if pos < len(p) {
						idx = p[pos]
					}
					acc |= uint64(idx) << nb
					nb += uint(w)
					for nb >= 8 {
						payload[bitOff] = byte(acc)
						acc >>= 8
						nb -= 8
						bitOff++
					}
				}
			}
		}
		out = append(out, payload...)
		p = p[:0]
	}

	emitRLE := func(x, count int) {
		hdr := make([]byte, binary.MaxVarintLen64)
		n := binary.PutUvarint(hdr, uint64(count<<1))
		out = append(out, hdr[:n]...)
		nb := (w + 7) / 8
		for i := 0; i < nb; i++ {
			out = append(out, byte(x>>(8*i)))
		}
	}

	start := 0
	for start < len(indices) {
		x := indices[start]
		end := start + 1
		for end < len(indices) && indices[end] == x {
			end++
		}
		length := end - start

		if length >= 8 {
			f := (8 - len(p)%8) % 8
			if length-f >= 8 {
				p = append(p, indices[start:start+f]...)
				*touches += f
				emitBitPack()
				emitRLE(x, length-f)
				*touches += length - f
			} else {
				p = append(p, indices[start:end]...)
				*touches += length
			}
		} else {
			p = append(p, indices[start:end]...)
			*touches += length
		}

		start = end
	}
	emitBitPack()

	return out
}
