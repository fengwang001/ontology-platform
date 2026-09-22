package fold

import "ontology/token"

// Unfold 把一个字段的若干物理行展开为单个逻辑值。
//
// 约定由调用方（headers 解析器）用 CRLF 切块后传入物理行：
//   - 空物理行（块终止空行）不得传入；
//   - 以 SP/HTAB 开头的物理行是上一行的续行，其前导空白（连续多格/制表）
//     压缩成恰好一个空格后并入；
//   - 第一个物理行就是续行 -> ErrLeadingContinuation；
//   - 续行为空或只有空白 -> ErrEmptyPhysicalLine；
//   - 行内含非值字节 -> token.ErrInvalidValue（包装后返回）。
//
// 返回值不做首尾 trim：调用方在值规范化阶段统一 trim，以保留“原始 vs 规范”
// 的区分。
func Unfold(lines []string) (string, error) {
	if len(lines) == 0 {
		return "", ErrEmptyPhysicalLine
	}
	if isLeadingWS(lines[0]) {
		return "", ErrLeadingContinuation
	}
	var b []byte
	for i, line := range lines {
		if line == "" {
			return "", ErrEmptyPhysicalLine
		}
		if i > 0 {
			if !isLeadingWS(line) {
				// headers 解析器按 CRLF 切块，相邻两行只可能由续行连接，
				// 否则解析器自身出错；这里做防御性判定。
				return "", ErrEmptyPhysicalLine
			}
			trimmed, n := trimLeadingWS(line)
			if n == len(line) {
				return "", ErrEmptyPhysicalLine
			}
			b = append(b, ' ')
			line = trimmed
		}
		if err := validateBytes(line); err != nil {
			return "", err
		}
		b = append(b, line...)
	}
	return string(b), nil
}

func isLeadingWS(s string) bool {
	return len(s) > 0 && (s[0] == ' ' || s[0] == '\t')
}

// trimLeadingWS 去掉前导连续 SP/HTAB，返回剩余串与去掉的字节数。
func trimLeadingWS(s string) (string, int) {
	i := 0
	for i < len(s) && (s[i] == ' ' || s[i] == '\t') {
		i++
	}
	return s[i:], i
}

func validateBytes(s string) error {
	for i := 0; i < len(s); i++ {
		if !token.IsValueByte(s[i]) {
			return token.ErrInvalidValue
		}
	}
	return nil
}
