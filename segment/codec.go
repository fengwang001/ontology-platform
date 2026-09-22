package segment

// 本文件是段的底层二进制编解码：LEB128 varint、zigzag 有符号 varint，
// 以及带阶段定位的解析器。任何长度不足都在读取前检查，
// 因此任意截断点都只会得到 CorruptError，不会越界读。

// appendUvarint 以 LEB128 追加无符号变长整数。
func appendUvarint(dst []byte, v uint64) []byte {
	for v >= 0x80 {
		dst = append(dst, byte(v)|0x80)
		v >>= 7
	}
	return append(dst, byte(v))
}

// appendSvarint 以 zigzag+LEB128 追加有符号变长整数。
func appendSvarint(dst []byte, v int64) []byte {
	return appendUvarint(dst, uint64(v)<<1^uint64(v>>63))
}

// parser 在 [pos, limit) 区间内顺序解析，越界即报当前阶段的损坏错误。
type parser struct {
	data  []byte
	pos   int
	limit int
	group int
	stage Stage
}

func (p *parser) fail(msg string) error {
	return &CorruptError{Group: p.group, Stage: p.stage, Msg: msg}
}

func (p *parser) byte() (byte, error) {
	if p.pos >= p.limit {
		return 0, p.fail("unexpected end of data")
	}
	b := p.data[p.pos]
	p.pos++
	return b, nil
}

func (p *parser) bytes(n int) ([]byte, error) {
	if n < 0 || p.limit-p.pos < n {
		return nil, p.fail("unexpected end of data")
	}
	b := p.data[p.pos : p.pos+n]
	p.pos += n
	return b, nil
}

func (p *parser) uvarint() (uint64, error) {
	var v uint64
	for shift := uint(0); shift < 64; shift += 7 {
		b, err := p.byte()
		if err != nil {
			return 0, err
		}
		v |= uint64(b&0x7f) << shift
		if b < 0x80 {
			return v, nil
		}
	}
	return 0, p.fail("varint overflow")
}

func (p *parser) svarint() (int64, error) {
	u, err := p.uvarint()
	if err != nil {
		return 0, err
	}
	return int64(u>>1) ^ -int64(u&1), nil
}
