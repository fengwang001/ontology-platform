// Package stack 表示并规范化调用栈：帧序列、相邻去重、最大深度截断。
package stack

// Normalize 把原始帧序列（根帧在前）规范化：
//   - 去除相邻重复帧（尾递归折叠）；
//   - 深度（去重后）超过 maxDepth 时保留前 maxDepth 帧并置 truncated=true；
//   - 空切片返回 (nil, false)，表示无效栈；
//   - 空串帧名合法；maxDepth<=0 表示不限制深度。
//
// 截断不修改帧名，只通过 truncated 返回；调用方据此给截断点节点打标记。
func Normalize(frames []string, maxDepth int) (out []string, truncated bool) {
	if len(frames) == 0 {
		return nil, false
	}
	out = make([]string, 0, len(frames))
	for _, f := range frames {
		if len(out) > 0 && out[len(out)-1] == f {
			continue
		}
		out = append(out, f)
	}
	if maxDepth > 0 && len(out) > maxDepth {
		out = out[:maxDepth]
		truncated = true
	}
	return out, truncated
}

// Valid 报告栈是否可用于采样插入。
func Valid(frames []string) bool { return len(frames) > 0 }
