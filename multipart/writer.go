package multipart

import (
	"strconv"

	"ontology/coalesce"
)

// ContentType 返回 multipart/byteranges 的 Content-Type 头值。
func ContentType(boundary string) string {
	return "multipart/byteranges; boundary=" + boundary
}

// PartHeader 返回某一段在字节负载之前的封装头：
//
//	--boundary\r\n
//	Content-Range: bytes start-end/total\r\n
//	\r\n
func PartHeader(boundary string, iv coalesce.Interval, total int64) []byte {
	buf := make([]byte, 0, 96)
	buf = append(buf, "--"...)
	buf = append(buf, boundary...)
	buf = append(buf, '\r', '\n')
	buf = append(buf, "Content-Range: bytes "...)
	buf = strconv.AppendInt(buf, iv.Start, 10)
	buf = append(buf, '-')
	buf = strconv.AppendInt(buf, iv.End, 10)
	buf = append(buf, '/')
	buf = strconv.AppendInt(buf, total, 10)
	buf = append(buf, '\r', '\n', '\r', '\n')
	return buf
}

// CRLF 是每段字节负载之后的换行。
func CRLF() []byte { return []byte{'\r', '\n'} }

// EndBoundary 返回整个 multipart 响应体的结束边界。
func EndBoundary(boundary string) []byte {
	out := append([]byte("--"), boundary...)
	out = append(out, "--"...)
	out = append(out, '\r', '\n')
	return out
}
