package serve

import (
	"ontology/coalesce"
	"ontology/multipart"
)

// render 把每个区间读到的字节按是否封装拼成最终响应体。
// 单区间：响应体就是该区间的裸字节；
// 多区间：每段 = 定界头 + 负载 + CRLF，最后追加结束边界。
func render(chunks [][]byte, ivs []coalesce.Interval, total int64, encaps bool, boundary string) []byte {
	if !encaps {
		return append([]byte(nil), chunks[0]...)
	}

	size := 0
	for i, ch := range chunks {
		size += len(multipart.PartHeader(boundary, ivs[i], total)) + len(ch) + 2
	}
	size += len(multipart.EndBoundary(boundary))

	body := make([]byte, 0, size)
	for i, ch := range chunks {
		body = append(body, multipart.PartHeader(boundary, ivs[i], total)...)
		body = append(body, ch...)
		body = append(body, multipart.CRLF()...)
	}
	body = append(body, multipart.EndBoundary(boundary)...)
	return body
}
