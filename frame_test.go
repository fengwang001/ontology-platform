package ontology

import ()

type parsedChunk struct {
	start int64
	typ   byte
	load  []byte
}

// parseFrames 按规则解析整段流（供测试断言，不依赖 Reader）。
func parseFrames(t interface{ Helper() }, raw []byte) []parsedChunk {
	t.Helper()
	var chunks []parsedChunk
	pos := 0
	for pos < len(raw) {
		if pos+4 > len(raw) {
			panic("truncated header")
		}
		start := pos
		typ := raw[pos]
		n := int(raw[pos+1]) | int(raw[pos+2])<<8 | int(raw[pos+3])<<16
		pos += 4
		if pos+n > len(raw) {
			panic("truncated payload")
		}
		chunks = append(chunks, parsedChunk{start: int64(start), typ: typ, load: append([]byte(nil), raw[pos:pos+n]...)})
		pos += n
	}
	return chunks
}

// chunkTypes 提取除标识块外的块类型序列。
func chunkTypes(chunks []parsedChunk) []byte {
	var types []byte
	for _, c := range chunks {
		if c.typ != identifierType {
			types = append(types, c.typ)
		}
	}
	return types
}

// incompressibleBlock 返回 n 字节 RLE 后无严格收益的数据。
func incompressibleBlock(n int, seed byte) []byte {
	p := make([]byte, n)
	for i := range p {
		// 严格交替，最长同值段为 1。
		p[i] = seed + byte(i&1)
	}
	return p
}
