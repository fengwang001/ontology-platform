package block

// Cut returns raw truncated to n bytes (n < len(raw)).
func Cut(raw []byte, n int) []byte {
	if n > len(raw) {
		n = len(raw)
	}
	out := make([]byte, n)
	copy(out, raw[:n])
	return out
}

// FlipCRC corrupts the trailing CRC byte so Parse reports ErrCRC.
func FlipCRC(raw []byte) []byte {
	out := append([]byte(nil), raw...)
	out[len(out)-1] ^= 0xFF
	return out
}

// LieShared sets the shared length of entry idx to prevLen+1.
func LieShared(raw []byte, idx, prevLen int) []byte {
	if _, err := parseHeader(raw); err != nil {
		return raw
	}
	out := append([]byte(nil), raw...)
	pos := hdrLen
	for i := 0; i <= idx; i++ {
		if i == idx {
			putU32(out[pos:pos+4], uint32(prevLen+1))
		}
		diffLen := int(u32(raw[pos+4:]))
		pos += 8 + diffLen
	}
	return out
}

// BreakRestart shifts restart table entry r by +1 so it points mid-entry.
func BreakRestart(raw []byte, r int) []byte {
	h, err := parseHeader(raw)
	if err != nil {
		return raw
	}
	out := append([]byte(nil), raw...)
	off := hdrLen + h.dataLen + uint32(r)*4
	putU32(out[off:off+4], u32(out[off:off+4])+1)
	return out
}

// Layout reports byte offsets used by truncation-interval classification.
func Layout(raw []byte) (dataEnd, restartEnd, totalEnd int) {
	h, err := parseHeader(raw)
	if err != nil {
		return 0, 0, 0
	}
	return hdrLen + int(h.dataLen), hdrLen + int(h.dataLen) + int(h.restartCount)*4,
		hdrLen + int(h.dataLen) + int(h.restartCount)*4 + 4
}
