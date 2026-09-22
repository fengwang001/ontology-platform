package fold

const (
	crlf = "\r\n"
	// ContPrefix 是回写时续行的统一前缀（恰好一个空格）。
	ContPrefix = " "
)

// Refold 把已展开的逻辑值按宽度重新折行。
//
// firstBudget 为首个物理行（`Name: ` 之后）可用字节数，contBudget 为每个
// 续行去掉前导空格后的可用字节数；任一 <= 0 表示该段不允许任何内容。
// 仅在“空格后紧跟非空白”的位置断开：续行前缀恰为一个空格，而断口后为
// 非空白，因此 Unfold 压回的前缀恰好还原原空格，折行往返无损。
// 若某段宽度内不存在这样的断口（含超长无空格 token、空格数无法无损表示
// 的情形），返回 ErrUnfoldable：硬切会在展开时凭空多出一个空格，故绝不硬切。
func Refold(value string, firstBudget, contBudget int) (string, error) {
	budget := firstBudget
	var out []byte
	rest := value
	for rest != "" {
		if budget <= 0 {
			return "", ErrUnfoldable
		}
		cut := nextCut(rest, budget)
		if cut < 0 {
			return "", ErrUnfoldable
		}
		if len(out) > 0 {
			out = append(out, crlf...)
			out = append(out, ContPrefix...)
		}
		if cut == len(rest) {
			out = append(out, rest...)
			rest = ""
			break
		}
		out = append(out, rest[:cut]...)
		rest = rest[cut+1:] // 跳过被续行前缀替换的那个空格
		budget = contBudget
	}
	return string(out), nil
}

// nextCut 返回本物理行应取的字节长度：rest 全长（放得下），或最后一个
// “空格且其后紧跟非空格”的空格索引（i 最大可取到 budget：此时本行恰好用满）。
// 找不到返回 -1。
func nextCut(rest string, budget int) int {
	if len(rest) <= budget {
		return len(rest)
	}
	best := -1
	limit := budget
	if limit > len(rest)-1 {
		limit = len(rest) - 1
	}
	for i := 0; i <= limit; i++ {
		if rest[i] == ' ' && rest[i+1] != ' ' {
			best = i
		}
	}
	return best
}
