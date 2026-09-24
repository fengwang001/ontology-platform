// Package ptr 实现 JSON Pointer 路径的编码、解码、下标解析与按路径逐层定位。
package ptr

import (
	"encoding/json"
	"errors"
	"strconv"
	"strings"
	"sync/atomic"
)

var (
	// ErrInvalidPath 非法路径：不以 / 开头的非空路径、非法 ~ 转义、非法数组下标等。
	ErrInvalidPath = errors.New("invalid path")
	// ErrNotFound 路径不存在：中间段或目标缺失、下标越界。
	ErrNotFound = errors.New("path not found")
)

// checks 非导出计数器：定位过程中检查过的键/下标累计个数（包内测试读差值）。
var checks atomic.Int64

// Encode 编码一个路径段：先 ~ → ~0，再 / → ~1。
func Encode(tok string) string {
	tok = strings.ReplaceAll(tok, "~", "~0")
	return strings.ReplaceAll(tok, "/", "~1")
}

// decode 解码一个路径段：先 ~1 → /，再 ~0 → ~；非法 ~ 转义报 ErrInvalidPath。
func decode(tok string) (string, error) {
	for i := 0; i < len(tok); i++ {
		if tok[i] == '~' {
			if i+1 >= len(tok) || (tok[i+1] != '0' && tok[i+1] != '1') {
				return "", ErrInvalidPath
			}
			i++
		}
	}
	tok = strings.ReplaceAll(tok, "~1", "/")
	return strings.ReplaceAll(tok, "~0", "~"), nil
}

// Split 校验并解码整条路径；根路径 "" 返回 nil。
func Split(path string) ([]string, error) {
	if path == "" {
		return nil, nil
	}
	if !strings.HasPrefix(path, "/") {
		return nil, ErrInvalidPath
	}
	raw := strings.Split(path[1:], "/")
	segs := make([]string, len(raw))
	for i, tok := range raw {
		s, err := decode(tok)
		if err != nil {
			return nil, err
		}
		segs[i] = s
	}
	return segs, nil
}

// ParseIndex 解析数组下标：不带前导零的十进制非负整数（"0" 合法，"01"/"-1" 非法）。
func ParseIndex(tok string) (int, error) {
	if tok == "" || (len(tok) > 1 && tok[0] == '0') {
		return 0, ErrInvalidPath
	}
	for i := 0; i < len(tok); i++ {
		if tok[i] < '0' || tok[i] > '9' {
			return 0, ErrInvalidPath
		}
	}
	n, err := strconv.Atoi(tok)
	if err != nil {
		return 0, ErrInvalidPath
	}
	return n, nil
}

// Clone 深拷贝 JSON 值（标准库 json 往返），供生成/应用补丁时隔离输入、补丁与结果。
func Clone(v any) any {
	b, _ := json.Marshal(v)
	var r any
	_ = json.Unmarshal(b, &r)
	return r
}

// Step 进入容器的一层：对象按键、数组按下标；每次检查计数一次。
func Step(container any, tok string) (any, error) {
	checks.Add(1)
	switch c := container.(type) {
	case map[string]any:
		v, ok := c[tok]
		if !ok {
			return nil, ErrNotFound
		}
		return v, nil
	case []any:
		i, err := ParseIndex(tok)
		if err != nil {
			return nil, err
		}
		if i >= len(c) {
			return nil, ErrNotFound
		}
		return c[i], nil
	}
	return nil, ErrNotFound
}
