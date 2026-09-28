// Package eol 识别混杂的行尾：\r\n、单独 \r、\n。
// CR 之后是否跟随 LF 在切分点处未知，因此提供待定状态机。
package eol

// Kind 是一个已识别行尾的种类。
type Kind int

const (
	// LF 为单独的 \n。
	LF Kind = iota
	// CR 为单独的 \r（下一字节不是 \n）。
	CR
	// CRLF 为 \r\n 两个原文字节。
	CRLF
)

func (k Kind) String() string {
	switch k {
	case LF:
		return "LF"
	case CR:
		return "CR"
	default:
		return "CRLF"
	}
}

// Breaks 对一个完整缓冲区（无跨边界）做行尾分类，供测试与 par 参考。
func Breaks(p []byte) []Kind {
	var out []Kind
	i := 0
	for i < len(p) {
		switch {
		case p[i] == '\n':
			out = append(out, LF)
			i++
		case p[i] == '\r' && i+1 < len(p) && p[i+1] == '\n':
			out = append(out, CRLF)
			i += 2
		case p[i] == '\r':
			if i+1 == len(p) {
				return out
			}
			out = append(out, CR)
			i++
		default:
			i++
		}
	}
	return out
}

// Tracker 维护上一个 CR 是否仍可能与后续 LF 配对。零值即可使用。
type Tracker struct {
	pending bool
}

// Pending 报告当前是否有一个尚未裁决的 CR。
func (t *Tracker) Pending() bool { return t.pending }

// Reset 清空待定状态。
func (t *Tracker) Reset() { t.pending = false }

// Feed 在新到达字节 b 之前推进状态。
// 若存在待定 CR，本字节使它得到裁决：为 \n 时返回 CRLF，否则返回单独 CR。
// 然后处理 b 本身：\n 返回 LF；\r 进入待定；其他字节返回 0。
func (t *Tracker) Feed(b byte) Kind {
	if t.pending {
		t.pending = false
		if b == '\n' {
			return CRLF
		}
		k := CR
		if b == '\r' {
			t.pending = true
		} else if b == '\n' {
			k = LF
		}
		return k
	}
	switch b {
	case '\r':
		t.pending = true
	case '\n':
		return LF
	}
	return 0
}

// Flush 在流结束时裁决：待定 CR 只能是单独 CR。
func (t *Tracker) Flush() Kind {
	if t.pending {
		t.pending = false
		return CR
	}
	return 0
}
