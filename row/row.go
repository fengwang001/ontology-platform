// Package row 定义连接行、连接键及其变长编解码。
package row

import "encoding/binary"

// Row 是一路输入中的一行：连接键 Key 与负载 Val（均可为空串）。
type Row struct {
	Key string
	Val string
}

// Encode 将一行编码为 keylen|key|vallen|val 的小端字节流。
func Encode(r Row) []byte {
	buf := make([]byte, 4+len(r.Key)+4+len(r.Val))
	binary.LittleEndian.PutUint32(buf, uint32(len(r.Key)))
	copy(buf[4:], r.Key)
	binary.LittleEndian.PutUint32(buf[4+len(r.Key):], uint32(len(r.Val)))
	copy(buf[4+len(r.Key)+4:], r.Val)
	return buf
}

// Decode 从字节流头部解码一行，返回该行与消耗的字节数；数据不足时 ok=false。
func Decode(p []byte) (r Row, n int, ok bool) {
	if len(p) < 4 {
		return Row{}, 0, false
	}
	kl := int(binary.LittleEndian.Uint32(p))
	if len(p) < 4+kl+4 {
		return Row{}, 0, false
	}
	r.Key = string(p[4 : 4+kl])
	vp := 4 + kl
	vl := int(binary.LittleEndian.Uint32(p[vp:]))
	if len(p) < vp+4+vl {
		return Row{}, 0, false
	}
	r.Val = string(p[vp+4 : vp+4+vl])
	return r, vp + 4 + vl, true
}
