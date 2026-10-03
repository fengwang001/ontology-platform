// Package version 解析客户端声明的 API 版本区间。
//
// 文法：
//
//	N    表示 lo=hi=N
//	N-M  表示 lo=N、hi=M，要求 N<=M
//	N-   表示 lo=N、hi=Max
//
// N、M 为不含前导零与符号的十进制整数，范围 [1, Max]。
package version

import (
	"errors"
	"strconv"
	"strings"
)

// Max 是版本号的最大合法取值。
const Max = 1000

// ErrSyntax 表示区间串不符合文法或版本号越界。
var ErrSyntax = errors.New("version: invalid range syntax")

// Range 是闭区间 [Lo, Hi]。
type Range struct {
	Lo int
	Hi int
}

// Parse 解析版本区间串，任何非法输入都返回 ErrSyntax。
func Parse(s string) (Range, error) {
	dash := strings.IndexByte(s, '-')
	if dash < 0 {
		n, err := parseNumber(s)
		if err != nil {
			return Range{}, err
		}
		return Range{Lo: n, Hi: n}, nil
	}
	lo, err := parseNumber(s[:dash])
	if err != nil {
		return Range{}, err
	}
	rest := s[dash+1:]
	if rest == "" {
		return Range{Lo: lo, Hi: Max}, nil
	}
	hi, err := parseNumber(rest)
	if err != nil {
		return Range{}, err
	}
	if lo > hi {
		return Range{}, ErrSyntax
	}
	return Range{Lo: lo, Hi: hi}, nil
}

// parseNumber 解析不含前导零与符号、范围 [1, Max] 的十进制版本号。
func parseNumber(s string) (int, error) {
	if s == "" {
		return 0, ErrSyntax
	}
	if len(s) > 1 && s[0] == '0' {
		return 0, ErrSyntax
	}
	for i := 0; i < len(s); i++ {
		if s[i] < '0' || s[i] > '9' {
			return 0, ErrSyntax
		}
	}
	n, err := strconv.Atoi(s)
	if err != nil || n < 1 || n > Max {
		return 0, ErrSyntax
	}
	return n, nil
}
