// Package expand 对合并后的最终键值表统一展开 ${key} 引用。
package expand

import (
	"errors"
	"fmt"
	"sort"
	"strings"
)

var (
	// ErrCycle 表示引用成环，错误信息带闭合路径。
	ErrCycle = errors.New("expand: reference cycle")
	// ErrUnclosedRef 表示 '${' 后无配对 '}'，带字节位置。
	ErrUnclosedRef = errors.New("expand: unclosed reference")
	// ErrUnknownRef 表示引用了不存在的键。
	ErrUnknownRef = errors.New("expand: unknown reference")
)

// Expander 展开引用并统计替换次数；每键结果记忆化，避免指数爆炸。
type Expander struct {
	replacements int
	values       map[string]string
	memo         map[string]string
	onStack      map[string]bool
	stack        []string
}

// ExpandAll 展开整张键值表，返回展开后的新表。
func (e *Expander) ExpandAll(values map[string]string) (map[string]string, error) {
	e.values = values
	e.memo = make(map[string]string, len(values))
	e.onStack = make(map[string]bool, len(values))
	keys := make([]string, 0, len(values))
	for k := range values {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		if _, err := e.expandKey(k); err != nil {
			return nil, err
		}
	}
	out := make(map[string]string, len(values))
	for k, v := range e.memo {
		out[k] = v
	}
	return out, nil
}

// Replacements 返回已执行的引用替换总次数。
func (e *Expander) Replacements() int { return e.replacements }

func (e *Expander) expandKey(key string) (string, error) {
	if v, ok := e.memo[key]; ok {
		return v, nil
	}
	if e.onStack[key] {
		from := 0
		for i, k := range e.stack {
			if k == key {
				from = i
				break
			}
		}
		path := append(append([]string{}, e.stack[from:]...), key)
		return "", fmt.Errorf("%w: %s", ErrCycle, strings.Join(path, " -> "))
	}
	raw, ok := e.values[key]
	if !ok {
		return "", fmt.Errorf("%w: %q", ErrUnknownRef, key)
	}
	e.onStack[key] = true
	e.stack = append(e.stack, key)
	expanded, err := e.expandValue(key, raw)
	e.stack = e.stack[:len(e.stack)-1]
	e.onStack[key] = false
	if err != nil {
		return "", err
	}
	e.memo[key] = expanded
	return expanded, nil
}

// expandValue 扫描值：'$$' 转义为字面量 '$'，'${key}' 递归展开，
// 孤立 '$' 按字面量处理，'${' 未闭合报字节位置。
func (e *Expander) expandValue(key, raw string) (string, error) {
	var b strings.Builder
	b.Grow(len(raw))
	for i := 0; i < len(raw); {
		if raw[i] != '$' {
			b.WriteByte(raw[i])
			i++
			continue
		}
		switch {
		case i+1 < len(raw) && raw[i+1] == '$':
			b.WriteByte('$')
			i += 2
		case i+1 < len(raw) && raw[i+1] == '{':
			end := strings.IndexByte(raw[i+2:], '}')
			if end < 0 {
				return "", fmt.Errorf("%w: key %q offset %d", ErrUnclosedRef, key, i)
			}
			ref := raw[i+2 : i+2+end]
			value, err := e.expandKey(ref)
			if err != nil {
				return "", err
			}
			e.replacements++
			b.WriteString(value)
			i += 2 + end + 1
		default:
			b.WriteByte('$')
			i++
		}
	}
	return b.String(), nil
}
