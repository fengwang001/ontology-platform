// Package ptr 实现 JSON Pointer 路径的编码/解码、数组下标解析与按路径定位。
package ptr

import (
	"errors"
	"strings"
	"sync/atomic"
)

// 可判定哨兵错误：非法路径 / 路径不存在。
var (
	ErrInvalidPath = errors.New("ptr: invalid path")
	ErrNotFound    = errors.New("ptr: path not found")
)

// checks 记录最近一次定位中检查过的键/下标个数（非导出，不出现在公开接口）。
var checks atomic.Int64

// Encode 编码一个路径段：先 ~→~0，再 /→~1。
func Encode(tok string) string {
	tok = strings.ReplaceAll(tok, "~", "~0")
	return strings.ReplaceAll(tok, "/", "~1")
}

// Decode 解码一个路径段：先 ~1→/，再 ~0→~；其余 ~ 用法非法。
func Decode(tok string) (string, error) {
	for i := 0; i < len(tok); i++ {
		if tok[i] == '~' && (i+1 >= len(tok) || (tok[i+1] != '0' && tok[i+1] != '1')) {
			return "", ErrInvalidPath
		}
	}
	tok = strings.ReplaceAll(tok, "~1", "/")
	tok = strings.ReplaceAll(tok, "~0", "~")
	return tok, nil
}

// Split 把路径拆成解码后的段序列；根路径 "" 返回 nil。
func Split(path string) ([]string, error) {
	if path == "" {
		return nil, nil
	}
	if path[0] != '/' {
		return nil, ErrInvalidPath
	}
	raw := strings.Split(path[1:], "/")
	segs := make([]string, len(raw))
	for i, r := range raw {
		s, err := Decode(r)
		if err != nil {
			return nil, err
		}
		segs[i] = s
	}
	return segs, nil
}

// ParseIndex 解析数组下标：不带前导零的十进制非负整数。
func ParseIndex(tok string) (int, error) {
	if tok == "" || (len(tok) > 1 && tok[0] == '0') {
		return 0, ErrInvalidPath
	}
	n := 0
	for i := 0; i < len(tok); i++ {
		if tok[i] < '0' || tok[i] > '9' {
			return 0, ErrInvalidPath
		}
		n = n*10 + int(tok[i]-'0')
	}
	return n, nil
}

// Locate 沿 segs 逐层定位，返回直接父容器、最后一段、以及把父容器整体写回
// 文档的闭包（父为根时 set 为 nil，调用方自行替换整个文档）。
func Locate(doc any, segs []string) (parent any, last string, set func(any), err error) {
	if len(segs) == 0 {
		return nil, "", nil, ErrInvalidPath
	}
	cur := doc
	for _, s := range segs[:len(segs)-1] {
		checks.Add(1)
		switch c := cur.(type) {
		case map[string]any:
			v, ok := c[s]
			if !ok {
				return nil, "", nil, ErrNotFound
			}
			m, k := c, s
			cur, set = v, func(nv any) { m[k] = nv }
		case []any:
			idx, e := ParseIndex(s)
			if e != nil {
				return nil, "", nil, e
			}
			if idx >= len(c) {
				return nil, "", nil, ErrNotFound
			}
			sl, i := c, idx
			cur, set = c[idx], func(nv any) { sl[i] = nv }
		default:
			return nil, "", nil, ErrNotFound
		}
	}
	checks.Add(1)
	return cur, segs[len(segs)-1], set, nil
}
