package layout

// scanResult 是单个物理行行内扫描的结果。
type scanResult struct {
	brackets     []byte // 扫描后的括号栈（仅在被接受时写回状态）
	lastSig      byte   // 本行最后一个有效字符
	hasLastSig   bool   // 本行是否存在有效字符
	continuation bool   // 本行是否以续行标记结束
	err          *Error // 行内从左到右遇到的第一个词法错误
}

// scanLine 从左到右扫描一个物理行。引号内到同种引号为止的字节不是
// 语法字符，反斜杠跳过其后一个字节；引号之外的 # 开始注释；括号压栈
// 与匹配弹出；行内引号与注释之外的最后一字节是反斜杠则为续行标记。
// 扫描不修改任何外部状态，结果通过 scanResult 返回。
func scanLine(line string, brackets []byte, bm int) scanResult {
	res := scanResult{brackets: brackets}
	inString := false
	escaped := false
	var quote byte
	i := 0
	for i < len(line) {
		b := line[i]
		if inString {
			switch {
			case escaped:
				escaped = false
			case b == '\\':
				escaped = true
			case b == quote:
				inString = false
				res.lastSig = b // 字符串的闭合引号算有效字符
				res.hasLastSig = true
			}
			i++
			continue
		}
		switch b {
		case '#':
			i = len(line) // 注释其后到行尾都忽略
		case '\'', '"':
			inString = true
			quote = b
			escaped = false
			i++
		case '(', '[', '{':
			if len(res.brackets) >= bm {
				res.err = &Error{Reason: ReasonBracketTooDeep}
				return res
			}
			res.brackets = append(res.brackets, b)
			res.lastSig = b
			res.hasLastSig = true
			i++
		case ')', ']', '}':
			if len(res.brackets) == 0 || !bracketsMatch(res.brackets[len(res.brackets)-1], b) {
				res.err = &Error{Reason: ReasonBracketError}
				return res
			}
			res.brackets = res.brackets[:len(res.brackets)-1]
			res.lastSig = b
			res.hasLastSig = true
			i++
		case ' ', '\t':
			i++
		case '\\':
			if i == len(line)-1 {
				res.continuation = true // 续行标记不算有效字符
			} else {
				res.lastSig = b
				res.hasLastSig = true
			}
			i++
		default:
			res.lastSig = b
			res.hasLastSig = true
			i++
		}
	}
	if inString {
		// 行内未闭合的字符串，或引号内最后一字节是反斜杠。
		res.err = &Error{Reason: ReasonStringError}
	}
	return res
}

func bracketsMatch(open, close byte) bool {
	switch open {
	case '(':
		return close == ')'
	case '[':
		return close == ']'
	case '{':
		return close == '}'
	}
	return false
}

// isBlankOrComment 报告去掉前导空白后为空或首字符为 # 的行
// （空白行与纯注释行）。
func isBlankOrComment(line string) bool {
	for i := 0; i < len(line); i++ {
		switch line[i] {
		case ' ', '\t':
		case '#':
			return true
		default:
			return false
		}
	}
	return true
}

// indentWidth 计算行首缩进列宽：空格使 c 与 a 各加一；制表符使 c
// 增至下一个 8 的倍数（已在 8 的倍数处则再进一档）、a 加一。
func indentWidth(line string) (c, a int) {
	for i := 0; i < len(line); i++ {
		switch line[i] {
		case ' ':
			c++
			a++
		case '\t':
			c = (c/8 + 1) * 8
			a++
		default:
			return c, a
		}
	}
	return c, a
}

func isWordByte(b byte) bool {
	return b == '_' ||
		(b >= '0' && b <= '9') ||
		(b >= 'a' && b <= 'z') ||
		(b >= 'A' && b <= 'Z')
}

// firstWord 返回去掉前导空白后首个由 ASCII 字母、数字与下划线组成
// 的最长连续字节串；首字节不属于该集合则为空串。
func firstWord(line string) string {
	i := 0
	for i < len(line) && (line[i] == ' ' || line[i] == '\t') {
		i++
	}
	j := i
	for j < len(line) && isWordByte(line[j]) {
		j++
	}
	return line[i:j]
}

// branchAllowed 判定悬垂分支：w 为本行首词，eh 为关系生效后栈顶项
// 的 h。比较是整串相等。
func branchAllowed(w, eh string) bool {
	switch w {
	case "elif":
		return eh == "if" || eh == "elif"
	case "else":
		switch eh {
		case "if", "elif", "for", "while", "except":
			return true
		}
		return false
	case "except":
		return eh == "try" || eh == "except"
	case "finally":
		switch eh {
		case "try", "except", "else":
			return true
		}
		return false
	}
	return true
}
