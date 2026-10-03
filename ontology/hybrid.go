package ontology

import "encoding/binary"

// hybridEncode 把索引序列切成 RLE 游程与位打包游程并编码。
// w 为每个索引的位宽（0..16）。
func hybridEncode(idx []uint32, w int) []byte {
	var out []byte
	var pend []uint32

	// 把待出文字缓冲写成位打包游程；final 时允许末组用索引 0 补足 8 个。
	emitLiterals := func(final bool) {
		if len(pend) == 0 {
			return
		}
		if final {
			for len(pend)%8 != 0 {
				pend = append(pend, 0)
			}
		}
		groups := len(pend) / 8
		out = binary.AppendUvarint(out, uint64(groups<<1|1))
		out = appendPacked(out, pend, w)
		pend = pend[:0]
	}

	i := 0
	for i < len(idx) {
		j := i + 1
		for j < len(idx) && idx[j] == idx[i] {
			j++
		}
		x := idx[i]
		l := j - i
		if l >= 8 {
			f := (8 - len(pend)%8) % 8
			if l-f >= 8 {
				for k := 0; k < f; k++ {
					pend = append(pend, x)
				}
				emitLiterals(false)
				out = binary.AppendUvarint(out, uint64((l-f)<<1))
				out = appendLE(out, x, (w+7)/8)
			} else {
				for k := 0; k < l; k++ {
					pend = append(pend, x)
				}
			}
		} else {
			for k := 0; k < l; k++ {
				pend = append(pend, x)
			}
		}
		i = j
	}
	emitLiterals(true)
	return out
}

// appendLE 以 n 字节小端追加 v。
func appendLE(out []byte, v uint32, n int) []byte {
	for k := 0; k < n; k++ {
		out = append(out, byte(v>>(8*k)))
	}
	return out
}

// appendPacked 把 8 的倍数个索引按每组 8 个、每个 w 位打包，
// 先出现的值在低位，字节小端，共 len(vals)/8*w 字节。
func appendPacked(out []byte, vals []uint32, w int) []byte {
	var acc uint64
	var nbits uint
	for _, v := range vals {
		acc |= uint64(v) << nbits
		nbits += uint(w)
		for nbits >= 8 {
			out = append(out, byte(acc))
			acc >>= 8
			nbits -= 8
		}
	}
	if nbits > 0 {
		out = append(out, byte(acc))
	}
	return out
}
