// Package eol 识别混杂的行尾：\r\n、单独 \r、单独 \n。
// 当 \r 位于切分点时进入待定状态，等下一字节或流结束再判定。
package eol

// Class 是一个输入字节相对行尾的分类。
type Class int

const (
	Other Class = iota // 非行尾字节
	CR                 // \r，可能与下一字节组成 \r\n
	LF                 // 确定的行尾（单独 \n，或 \r\n 中的 \n）
)

// Classify 按字节分类，不持有任何跨字节状态。
func Classify(b byte) Class {
	switch b {
	case '\r':
		return CR
	case '\n':
		return LF
	default:
		return Other
	}
}

// Decoder 是流式行尾识别器：\r 先暂存，直到看到下一个字节。
type Decoder struct {
	pending bool // 暂存了一个尚未判定的 \r
}

// Pending 报告是否有一个暂存的 \r。
func (d *Decoder) Pending() bool { return d.pending }

// Push 送入一个字节的分类，返回该次判定后应发出的换行数与剩余字节数。
// 返回值 emit 为 0 或 1（单字节最多解析出一个行尾）。
// 当 c==CR 时一定暂存；c==LF 时若有暂存 \r 则与它配对成一个 \r\n。
func (d *Decoder) Push(c Class) (emit int) {
	if d.pending {
		d.pending = false
		switch c {
		case LF:
			return 1 // \r\n 整体是一个行尾
		case CR:
			emit = 1 // 前一个 \r 单独成行尾
		default:
			emit = 1 // 前一个 \r 单独成行尾
		}
	}
	if c == CR {
		d.pending = true
	}
	if c == LF {
		emit++
	}
	return emit
}

// Flush 在流结束时调用：暂存的单独 \r 被确认为行尾。
func (d *Decoder) Flush() int {
	if d.pending {
		d.pending = false
		return 1
	}
	return 0
}
