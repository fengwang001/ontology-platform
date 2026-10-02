package ontology

type decodedRun struct {
	rle     bool
	count   int
	idx     int
	payload []byte
}

// hybridDecode validates and decodes the body (without the width byte) of a
// Dict page. Padding with index 0 is allowed only in the final group of the
// final bit-packed run.
func hybridDecode(data []byte, w, dictLen, rows int) ([]int, error) {
	if dictLen <= 0 || rows <= 0 || w < 0 {
		return nil, ErrCorrupt
	}
	widthBytes := (w + 7) / 8

	var runs []decodedRun
	pos := 0
	for pos < len(data) {
		v, n, err := readUvarint(data[pos:])
		if err != nil {
			return nil, err
		}
		pos += n

		if v&1 == 0 {
			count := int(v >> 1)
			if count == 0 || count > rows {
				return nil, ErrCorrupt
			}
			if pos+widthBytes > len(data) {
				return nil, ErrCorrupt
			}
			idx := 0
			for i := 0; i < widthBytes; i++ {
				idx |= int(data[pos+i]) << (8 * i)
			}
			pos += widthBytes
			if idx >= dictLen {
				return nil, ErrCorrupt
			}
			runs = append(runs, decodedRun{rle: true, count: count, idx: idx})
		} else {
			groups := int(v >> 1)
			if groups == 0 {
				return nil, ErrCorrupt
			}
			payloadLen := groups * w
			if groups > rows || pos+payloadLen > len(data) {
				return nil, ErrCorrupt
			}
			runs = append(runs, decodedRun{
				rle:     false,
				count:   groups,
				payload: data[pos : pos+payloadLen],
			})
			pos += payloadLen
		}
	}
	if pos != len(data) || len(runs) == 0 {
		return nil, ErrCorrupt
	}

	out := make([]int, 0, rows)
	remaining := rows
	for ri, run := range runs {
		last := ri == len(runs)-1
		if run.rle {
			if run.count > remaining {
				return nil, ErrCorrupt
			}
			for j := 0; j < run.count; j++ {
				out = append(out, run.idx)
			}
			remaining -= run.count
			continue
		}

		vals := unpackBits(run.payload, w, run.count)
		full := len(vals)
		if !last {
			if full > remaining {
				return nil, ErrCorrupt
			}
			for _, idx := range vals {
				if idx >= dictLen {
					return nil, ErrCorrupt
				}
				out = append(out, idx)
			}
			remaining -= full
			continue
		}

		pad := full - remaining
		if pad < 0 || pad >= 8 {
			return nil, ErrCorrupt
		}
		if pad > 0 && remaining%8 == 0 {
			// Padding is valid only within a partial final group; if the row
			// count fills the last group completely, no padding is allowed.
			return nil, ErrCorrupt
		}
		for i, idx := range vals {
			if i >= full-pad {
				if idx != 0 {
					return nil, ErrCorrupt
				}
				continue
			}
			if idx >= dictLen {
				return nil, ErrCorrupt
			}
			out = append(out, idx)
		}
		remaining = 0
	}

	if remaining != 0 || len(out) != rows {
		return nil, ErrCorrupt
	}
	return out, nil
}

func unpackBits(payload []byte, w, groups int) []int {
	vals := make([]int, groups*8)
	if w == 0 {
		return vals
	}
	bitPos := 0
	for i := range vals {
		v := 0
		for b := 0; b < w; b++ {
			if payload[bitPos/8]&(1<<uint(bitPos%8)) != 0 {
				v |= 1 << uint(b)
			}
			bitPos++
		}
		vals[i] = v
	}
	return vals
}

// readUvarint parses a canonical, minimal varint fitting in uint64.
func readUvarint(data []byte) (uint64, int, error) {
	var x uint64
	var s uint
	for i := 0; i < len(data); i++ {
		b := data[i]
		if i >= 10 || (i == 9 && b > 1) {
			return 0, 0, ErrCorrupt
		}
		x |= uint64(b&0x7f) << s
		if b < 0x80 {
			if i > 0 && b == 0 {
				return 0, 0, ErrCorrupt
			}
			return x, i + 1, nil
		}
		s += 7
	}
	return 0, 0, ErrCorrupt
}
