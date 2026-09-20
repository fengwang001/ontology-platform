// Package byterange 实现 HTTP Range 头的解析与区间归一化。
package byterange

// Range 是归一化后的闭区间 [Start, End]，均以 0 起算。
type Range struct {
	Start int64
	End   int64
}

// Parse 解析 Range 头并按资源总长度 size 归一化。
// size 为资源字节数（>=0）。
func Parse(header string, size int64) ([]Range, error) {
	specs, err := parseHeader(header)
	if err != nil {
		return nil, err
	}
	ranges := make([]Range, 0, len(specs))
	for _, spec := range specs {
		if r, ok := spec.normalize(size); ok {
			ranges = append(ranges, r)
		}
	}
	if len(ranges) == 0 {
		return nil, ErrUnsatisfiable
	}
	return mergeRanges(ranges), nil
}
