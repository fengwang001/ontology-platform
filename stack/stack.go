// Package stack 提供调用栈的表示与规范化。
package stack

import "errors"

// ErrEmpty 表示空栈，属于无效样本。
var ErrEmpty = errors.New("stack: empty stack")

// DefaultMaxDepth 是默认的最大栈深。
const DefaultMaxDepth = 64

// Normalize 规范化一条帧序列（根在前）：
// 折叠连续同名帧，超过 maxDepth 时截断并返回 truncated=true。
// 空栈返回 ErrEmpty；空串帧名合法。maxDepth<=0 表示不限制深度。
func Normalize(frames []string, maxDepth int) (out []string, truncated bool, err error) {
	if len(frames) == 0 {
		return nil, false, ErrEmpty
	}
	out = make([]string, 0, len(frames))
	for i, f := range frames {
		if i > 0 && f == frames[i-1] {
			continue
		}
		out = append(out, f)
	}
	if maxDepth > 0 && len(out) > maxDepth {
		out = out[:maxDepth]
		truncated = true
	}
	return out, truncated, nil
}
