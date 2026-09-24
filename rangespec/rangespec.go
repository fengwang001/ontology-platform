// Package rangespec 解析 HTTP Range 头的词法与语法。
//
// 支持三种写法：a-b（含两端）、a-（到末尾）、-n（末尾 n 字节）。
// 本包只做语法解析，不感知资源长度；越界裁剪与不可满足判定由
// coalesce 包在归一化时完成（不可满足错误类型定义在本包，
// 以便两类错误可被调用方用 errors.As 判定）。
package rangespec

import (
	"fmt"
	"strconv"
	"strings"
)

// Spec 是一条解析后的区间请求。
// Start == -1 表示后缀写法（末尾 Suffix 个字节，Suffix 可为 0）；
// 否则为下标写法，End == -1 表示开放到资源末尾。
type Spec struct {
	Start  int64
	End    int64
	Suffix int64
}

// SyntaxError 表示 Range 头语法错误，Offset 为出错字节在原始头中的偏移。
type SyntaxError struct {
	Offset int
	Msg    string
}

func (e *SyntaxError) Error() string {
	return fmt.Sprintf("rangespec: syntax error at byte %d: %s", e.Offset, e.Msg)
}

// UnsatisfiableError 表示区间按当前资源长度取不到任何字节，
// Total 携带资源总长，供调用方生成 416 响应。
type UnsatisfiableError struct {
	Total int64
}

func (e *UnsatisfiableError) Error() string {
	return fmt.Sprintf("rangespec: range unsatisfiable (resource length %d)", e.Total)
}

const unit = "bytes="

// Parse 解析 Range 头（如 "bytes=0-99, 200-, -50"），返回区间列表。
// 语法错误返回 *SyntaxError；"bytes=-0" 语法合法，解析为 Suffix==0 的
// Spec，由归一化阶段判为不可满足。
func Parse(header string) ([]Spec, error) {
	if len(header) < len(unit) || !strings.EqualFold(header[:len(unit)], unit) {
		return nil, &SyntaxError{Offset: 0, Msg: "missing \"bytes=\" unit"}
	}
	var specs []Spec
	pos := len(unit)
	for pos <= len(header) {
		end := strings.IndexByte(header[pos:], ',')
		itemEnd := len(header)
		if end >= 0 {
			itemEnd = pos + end
		}
		spec, err := parseItem(header, pos, itemEnd)
		if err != nil {
			return nil, err
		}
		specs = append(specs, spec)
		if end < 0 {
			break
		}
		pos = itemEnd + 1
	}
	if len(specs) == 0 {
		return nil, &SyntaxError{Offset: pos, Msg: "no range specified"}
	}
	return specs, nil
}

func parseItem(header string, from, to int) (Spec, error) {
	for from < to && header[from] == ' ' {
		from++
	}
	for to > from && header[to-1] == ' ' {
		to--
	}
	item := header[from:to]
	dash := strings.IndexByte(item, '-')
	if dash < 0 {
		return Spec{}, &SyntaxError{Offset: from, Msg: "missing '-'"}
	}
	left, right := item[:dash], item[dash+1:]
	if left == "" && right == "" {
		return Spec{}, &SyntaxError{Offset: from + dash, Msg: "empty range"}
	}
	if left == "" {
		n, err := parseNumber(header, from+dash+1, right)
		if err != nil {
			return Spec{}, err
		}
		return Spec{Start: -1, Suffix: n}, nil
	}
	start, err := parseNumber(header, from, left)
	if err != nil {
		return Spec{}, err
	}
	if right == "" {
		return Spec{Start: start, End: -1}, nil
	}
	stop, err := parseNumber(header, from+dash+1, right)
	if err != nil {
		return Spec{}, err
	}
	return Spec{Start: start, End: stop}, nil
}

func parseNumber(header string, base int, s string) (int64, error) {
	for i := 0; i < len(s); i++ {
		if s[i] < '0' || s[i] > '9' {
			return 0, &SyntaxError{Offset: base + i, Msg: "invalid digit"}
		}
	}
	n, err := strconv.ParseInt(s, 10, 64)
	if err != nil {
		return 0, &SyntaxError{Offset: base, Msg: "number out of range"}
	}
	return n, nil
}
