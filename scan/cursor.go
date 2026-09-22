package scan

import "errors"

// Cursor 是扫描续扫位置的不透明令牌。
// 编码内容：下一个待消费行的（行组号, 组内行偏移）。
// 被裁剪的行组不进入游标——游标只记录"下一组从哪开始"，
// 因此能穿过被裁剪的行组且不重不漏。
type Cursor struct {
	group  int
	offset int
}

// ErrBadCursor 表示游标字节无法解析。
var ErrBadCursor = errors.New("scan: malformed cursor")

// Encode 把游标序列化为字节（两个 uvarint：行组号、组内偏移）。
func (c Cursor) Encode() []byte {
	var out []byte
	out = appendUvarint(out, uint64(c.group))
	out = appendUvarint(out, uint64(c.offset))
	return out
}

// DecodeCursor 从字节还原游标。
func DecodeCursor(data []byte) (Cursor, error) {
	var c Cursor
	g, n1, err := readUvarint(data)
	if err != nil {
		return c, err
	}
	o, n2, err := readUvarint(data[n1:])
	if err != nil {
		return c, err
	}
	if n1+n2 != len(data) {
		return c, ErrBadCursor
	}
	c.group, c.offset = int(g), int(o)
	return c, nil
}

func appendUvarint(dst []byte, v uint64) []byte {
	for v >= 0x80 {
		dst = append(dst, byte(v)|0x80)
		v >>= 7
	}
	return append(dst, byte(v))
}

func readUvarint(data []byte) (uint64, int, error) {
	var v uint64
	for i := 0; i < len(data) && i < 10; i++ {
		b := data[i]
		if b < 0x80 {
			if i == 9 && b > 1 {
				return 0, 0, ErrBadCursor
			}
			return v | uint64(b)<<uint(7*i), i + 1, nil
		}
		v |= uint64(b&0x7f) << uint(7*i)
	}
	return 0, 0, ErrBadCursor
}
