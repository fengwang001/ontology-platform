package ontology

import (
	"hash/crc32"
)

const (
	maxChunkData = 65536 // 每个数据块未压缩数据上限
	maxChunkLoad = 65540 // 数据块负载上限：4 字节校验和 + 65536

	identifierType = 0xFF
	chunkRLE       = 0x00
	chunkRaw       = 0x01
)

var crcTable = crc32.MakeTable(crc32.Castagnoli)

// maskedChecksum 计算 CRC-32C 并按 Snappy 分帧规则掩码：
// ((crc>>15)|(crc<<17)) + 0xa282ead8，uint32 回绕。
func maskedChecksum(p []byte) uint32 {
	c := crc32.Checksum(p, crcTable)
	return ((c >> 15) | (c << 17)) + 0xa282ead8
}

// rleEncode 按固定规则进行字节游程压缩：
//   - 控制字节 c<128：后接 c+1 个字面字节（1..128）；
//   - 控制字节 c>=128：把后一个字节重复 (c-128)+3 次（3..130）。
func rleEncode(p []byte) []byte {
	out := make([]byte, 0, len(p))
	var lit [128]byte
	nLit := 0

	flush := func() {
		if nLit == 0 {
			return
		}
		out = append(out, byte(nLit-1))
		out = append(out, lit[:nLit]...)
		nLit = 0
	}

	for i := 0; i < len(p); {
		b := p[i]
		r := 1
		for i+r < len(p) && p[i+r] == b {
			r++
		}
		for r >= 3 {
			run := r
			if run > 130 {
				run = 130
			}
			flush()
			out = append(out, byte(128+run-3), b)
			i += run
			r -= run
		}
		for ; r > 0; r-- {
			lit[nLit] = b
			nLit++
			if nLit == 128 {
				flush()
			}
			i++
		}
	}
	flush()
	return out
}

// rleDecode 解码游程负载。令牌截断（负载不完整）返回 ErrDecode；
// 解出数据超过 limit 字节同样返回 ErrDecode。
func rleDecode(p []byte, limit int) ([]byte, error) {
	out := make([]byte, 0, len(p))
	for i := 0; i < len(p); {
		c := p[i]
		i++
		if c < 128 {
			n := int(c) + 1
			if i+n > len(p) {
				return nil, ErrDecode
			}
			if len(out)+n > limit {
				return nil, ErrDecode
			}
			out = append(out, p[i:i+n]...)
			i += n
		} else {
			n := int(c-128) + 3
			if i >= len(p) {
				return nil, ErrDecode
			}
			if len(out)+n > limit {
				return nil, ErrDecode
			}
			b := p[i]
			i++
			for j := 0; j < n; j++ {
				out = append(out, b)
			}
		}
	}
	return out, nil
}
