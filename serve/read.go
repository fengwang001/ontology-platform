package serve

import (
	"io"

	"ontology/coalesce"
	"ontology/source"
)

// readRange 从 src 读满闭区间 iv。数据源允许短读（n<len(p) 且 err==nil），
// 这里循环补齐直到读满；若在补齐前遇到 EOF/错误且字节不足，则返回
// source.ErrShortData（EOF 不足）或底层错误，绝不静默截断。
func readRange(src source.Source, iv coalesce.Interval) ([]byte, error) {
	total := int(iv.Length())
	buf := make([]byte, total)
	off := iv.Start
	filled := 0
	for filled < total {
		n, err := src.ReadAt(buf[filled:], off+int64(filled))
		if n > 0 {
			filled += n
		}
		if filled == total {
			return buf, nil
		}
		if err == io.EOF {
			return nil, source.ErrShortData
		}
		if err != nil {
			return nil, err
		}
		if n == 0 {
			// 既未前进也无错误，等同于数据不足。
			return nil, source.ErrShortData
		}
	}
	return buf, nil
}
