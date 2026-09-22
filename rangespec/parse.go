package rangespec

import (
	"math"
	"strconv"
)

const prefix = "bytes="

// Parse 解析 Range 头值（形如 "bytes=a-b,c-,-n"）。
// 语法错误返回 *SyntaxError，其 Offset 相对 "bytes=" 之后的首字节。
// Parse 只做词法语法分析，不做越界裁剪，也不产生"不可满足"错误。
func Parse(header string) ([]Spec, error) {
	if len(header) < len(prefix) || header[:len(prefix)] != prefix {
		return nil, &SyntaxError{Offset: 0, Reason: `missing "bytes=" prefix`}
	}
	body := header[len(prefix):]
	if body == "" {
		return nil, &SyntaxError{Offset: 0, Reason: "empty range list"}
	}

	var specs []Spec
	elementStart := 0
	for elementStart <= len(body) {
		end := indexByte(body, elementStart, ',')
		if end == -1 {
			end = len(body)
		}
		tok := body[elementStart:end]
		if tok == "" {
			return nil, &SyntaxError{Offset: elementStart, Reason: "empty range element"}
		}
		spec, err := parseElement(tok, elementStart)
		if err != nil {
			return nil, err
		}
		specs = append(specs, spec)
		if end == len(body) {
			break
		}
		elementStart = end + 1
	}
	return specs, nil
}

func parseElement(tok string, base int) (Spec, error) {
	dash := -1
	for i := 0; i < len(tok); i++ {
		if tok[i] == '-' {
			dash = i
			break
		}
	}
	if dash == -1 {
		return Spec{}, &SyntaxError{Offset: base + len(tok), Reason: "missing '-' in range"}
	}
	left, right := tok[:dash], tok[dash+1:]
	if left == "" && right == "" {
		return Spec{}, &SyntaxError{Offset: base + dash, Reason: "range has neither start nor end"}
	}

	if left == "" {
		n, err := parseNonNeg(right, base+dash+1)
		if err != nil {
			return Spec{}, err
		}
		return Spec{Kind: Suffix, SuffixN: n}, nil
	}
	start, err := parseNonNeg(left, base)
	if err != nil {
		return Spec{}, err
	}
	if right == "" {
		return Spec{Kind: FromEnd, Start: start}, nil
	}
	stop, err := parseNonNeg(right, base+dash+1)
	if err != nil {
		return Spec{}, err
	}
	if stop < start {
		return Spec{}, &SyntaxError{Offset: base + dash + 1, Reason: "range end is less than start"}
	}
	return Spec{Kind: FromTo, Start: start, End: stop}, nil
}

func parseNonNeg(s string, offset int) (int64, error) {
	if len(s) == 0 {
		return 0, &SyntaxError{Offset: offset, Reason: "expected non-negative integer"}
	}
	for i := 0; i < len(s); i++ {
		if s[i] < '0' || s[i] > '9' {
			return 0, &SyntaxError{Offset: offset + i, Reason: "non-digit byte in integer"}
		}
	}
	v, err := strconv.ParseInt(s, 10, 64)
	if err != nil {
		return 0, &SyntaxError{Offset: offset, Reason: "integer out of range (max " + strconv.FormatInt(math.MaxInt64, 10) + ")"}
	}
	return v, nil
}

func indexByte(s string, from int, c byte) int {
	for i := from; i < len(s); i++ {
		if s[i] == c {
			return i
		}
	}
	return -1
}
