package fold

import "ontology/token"

// Refold 把已展开的值按 width 重新折行，返回各物理行（不含 CRLF）。
//
// width 是物理行（含续行前导 SP）的目标最大长度：
//   - width <= 0 或值不超时，原样一行返回；
//   - 只在"两侧均有非空白字节的 SP"处断行（即内部 OWS），绝不在单词中间
//     硬断——硬断会引入额外 SP，破坏展开/折行互逆性；
//   - 若当前行直到末尾都找不到这样的断点（超长无空格 token），该行超长
//     输出；width 是目标而非硬上限。
//
// 续行恰以一个 SP 开头，因此对返回行再调用 Unfold 会把 "CRLF+SP" 压回该
// 断点处的那一个 SP：Refold/Unfold 对任意输入往返无损。
func Refold(value string, width int) []string {
	if width <= 0 || len(value) <= width {
		return []string{value}
	}
	lines := make([]string, 0, len(value)/width+1)
	rest := value
	for len(rest) > width {
		cut := -1
		// 在窗口内取最后一个合法断点，使每行尽量长。
		for i := width - 1; i > 0; i-- {
			if rest[i] == ' ' && i+1 < len(rest) &&
				!token.IsOWS(rest[i-1]) && !token.IsOWS(rest[i+1]) {
				cut = i
				break
			}
		}
		if cut < 0 {
			break // 窗口内无断点，剩余整体超长输出
		}
		lines = append(lines, rest[:cut])
		rest = rest[cut+1:] // 断点 SP 成为下一行的前导 SP
	}
	lines = append(lines, rest)
	return lines
}
