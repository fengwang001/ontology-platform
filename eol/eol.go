// Package eol 识别混杂行尾：\r\n、单独 \r、单独 \n，以及切分点处待定的 \r。
package eol

// Kind 是一个字节相对行尾状态机的分类结果。
type Kind int

const (
	Other Kind = iota // 普通字节（含 NUL、非法 UTF-8，原样通过）
	CR                // 回车：可能与下一个 \n 组成 \r\n
	LF                // 换行
)

// Classify 只看单个字节本身：\r→CR，\n→LF，其余→Other。
// 组合判定交给 Resolve：\r 后是否紧跟 \n 在流式输入里必须延迟。
func Classify(b byte) Kind {
	switch b {
	case '\r':
		return CR
	case '\n':
		return LF
	default:
		return Other
	}
}

// Seq 表示一次已确定的行尾结算。
type Seq int

const (
	SeqNone Seq = iota // 非行尾
	SeqCRLF            // \r\n：消费 2 字节，输出 1 个 \n
	SeqCR              // 单独 \r：消费 1 字节，输出 1 个 \n
	SeqLF              // 单独 \n：消费 1 字节，输出 1 个 \n
)

// Consumed 返回该行尾消费的原文字节数（SeqNone 为 0）。
func (s Seq) Consumed() int {
	if s == SeqCRLF {
		return 2
	}
	if s != SeqNone {
		return 1
	}
	return 0
}

// Resolve 在已知「上一个字符是待定 \r」时，结算该 \r 与当前字节的关系。
// pending 为 true 表示前一字节是尚未判定的 \r。
// 返回本次产生的行尾序列，以及当前字节是否已被该序列消费。
func Resolve(pending bool, b byte) (seq Seq, consumed bool) {
	if !pending {
		if Classify(b) == LF {
			return SeqLF, true
		}
		return SeqNone, false
	}
	// 待定 \r：\r\r\n 的第一个 \r 必须先结算成单独行尾。
	if b == '\n' {
		return SeqCRLF, true
	}
	if b == '\r' {
		return SeqCR, false // 当前 \r 成为新的待定字符
	}
	return SeqCR, false
}

// Flush 在流结束时结算一个仍待定的 \r：它只能是单独行尾。
func Flush(pending bool) Seq {
	if pending {
		return SeqCR
	}
	return SeqNone
}
