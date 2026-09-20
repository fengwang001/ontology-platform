package byterange

import (
	"math"
	"strconv"
	"strings"
)

// spec 是一条尚未归一化的区间描述。
type spec struct {
	start  int64
	end    int64
	suffix bool // true 表示后缀形式（最后 N 字节，长度存于 end）
}

// parseHeader 解析 Range 头的语法部分，如 "bytes=0-499,500-"。
func parseHeader(header string) ([]spec, error) {
	unit, list, found := strings.Cut(header, "=")
	if !found || !strings.EqualFold(strings.TrimSpace(unit), "bytes") {
		return nil, ErrMalformed
	}
	items := strings.Split(list, ",")
	specs := make([]spec, 0, len(items))
	for _, item := range items {
		s, err := parseItem(strings.TrimSpace(item))
		if err != nil {
			return nil, err
		}
		specs = append(specs, s)
	}
	return specs, nil
}

// parseItem 解析单条区间，如 "0-499"、"500-" 或 "-500"。
func parseItem(item string) (spec, error) {
	first, second, found := strings.Cut(item, "-")
	if !found {
		return spec{}, ErrMalformed
	}
	first, second = strings.TrimSpace(first), strings.TrimSpace(second)
	if first == "" {
		if second == "" {
			return spec{}, ErrMalformed // "-" 既无起点也无后缀长度
		}
		n, err := parseNumber(second)
		if err != nil {
			return spec{}, err
		}
		return spec{end: n, suffix: true}, nil
	}
	start, err := parseNumber(first)
	if err != nil {
		return spec{}, err
	}
	if second == "" {
		return spec{start: start, end: -1}, nil // end=-1 表示到资源末尾
	}
	end, err := parseNumber(second)
	if err != nil {
		return spec{}, err
	}
	if start > end {
		return spec{}, ErrMalformed
	}
	return spec{start: start, end: end}, nil
}

// parseNumber 解析非负十进制整数，超过 int64 范围时报语法错误。
func parseNumber(s string) (int64, error) {
	if s == "" {
		return 0, ErrMalformed
	}
	for _, c := range s {
		if c < '0' || c > '9' {
			return 0, ErrMalformed
		}
	}
	n, err := strconv.ParseUint(s, 10, 64)
	if err != nil || n > math.MaxInt64 {
		return 0, ErrMalformed
	}
	return int64(n), nil
}

// normalize 按资源长度 size 归一化为绝对闭区间。
// 返回 ok=false 表示该区间不可满足。
func (s spec) normalize(size int64) (Range, bool) {
	if size <= 0 {
		return Range{}, false
	}
	if s.suffix {
		if s.end <= 0 {
			return Range{}, false // 后缀长度为 0 不可满足
		}
		if s.end >= size {
			return Range{Start: 0, End: size - 1}, true
		}
		return Range{Start: size - s.end, End: size - 1}, true
	}
	if s.start >= size {
		return Range{}, false // 起点越过资源末尾
	}
	end := s.end
	if end < 0 || end > size-1 {
		end = size - 1 // 越界裁剪而非报错
	}
	return Range{Start: s.start, End: end}, true
}
