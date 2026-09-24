// Package rangespec 解析 Range 头的词法与语法，支持 a-b、a-、-n 三种写法。
// 本包只做语法判断：越界裁剪与不可满足判定属于 coalesce 包。
package rangespec

import (
	"fmt"
	"strconv"
	"strings"
)

// Range 是一条语法上合法的区间请求，尚未按资源长度归一化。
type Range struct {
	Start  int64 // a-b / a- 的起点；-n 形式为 -1
	End    int64 // a-b 的终点（含）；a- 与 -n 形式为 -1
	Suffix int64 // -n 形式的 n；其余形式为 -1
}

// ParseError 表示语法错误，Offset 指出出错字节在header中的偏移。
type ParseError struct {
	Offset int
	Msg    string
}

func (e *ParseError) Error() string {
	return fmt.Sprintf("rangespec: syntax error at byte %d: %s", e.Offset, e.Msg)
}

// Parse 解析 Range 头值（如 "bytes=0-100,200-,-50"）。
// 语法错误返回 *ParseError；bytes=-0 语法合法，会解析为 Suffix=0。
func Parse(header string) ([]Range, error) {
	const unit = "bytes="
	if !strings.HasPrefix(header, unit) {
		return nil, &ParseError{Offset: 0, Msg: `missing "bytes=" prefix`}
	}
	i := len(unit)
	if i >= len(header) {
		return nil, &ParseError{Offset: i, Msg: "empty range set"}
	}
	var out []Range
	for {
		at := i
		a, hasA, err := scanInt(header, &i)
		if err != nil {
			return nil, &ParseError{Offset: at, Msg: err.Error()}
		}
		if i >= len(header) || header[i] != '-' {
			return nil, &ParseError{Offset: i, Msg: "expected '-'"}
		}
		i++
		b, hasB, err := scanInt(header, &i)
		if err != nil {
			return nil, &ParseError{Offset: at, Msg: err.Error()}
		}
		switch {
		case hasA && hasB:
			out = append(out, Range{Start: a, End: b, Suffix: -1})
		case hasA:
			out = append(out, Range{Start: a, End: -1, Suffix: -1})
		case hasB:
			out = append(out, Range{Start: -1, End: -1, Suffix: b})
		default:
			return nil, &ParseError{Offset: at, Msg: "empty range spec"}
		}
		if i >= len(header) {
			return out, nil
		}
		if header[i] != ',' {
			return nil, &ParseError{Offset: i, Msg: "expected ','"}
		}
		i++
		if i >= len(header) {
			return nil, &ParseError{Offset: i, Msg: "trailing ','"}
		}
	}
}

// scanInt 从 *i 处扫描十进制数字；无数字时 ok=false，溢出时报错。
func scanInt(s string, i *int) (v int64, ok bool, err error) {
	j := *i
	for j < len(s) && s[j] >= '0' && s[j] <= '9' {
		j++
	}
	if j == *i {
		return 0, false, nil
	}
	n, perr := strconv.ParseInt(s[*i:j], 10, 64)
	if perr != nil {
		return 0, false, fmt.Errorf("integer overflow")
	}
	*i = j
	return n, true, nil
}
