// Package eol 识别流中的行尾：\r\n、单独 \r、单独 \n。
// 逐字节喂入；\r 先进入待定状态，下一字节决定它与 \n 配对还是单独成行。
package eol

// Kind 是一次行尾事件的种类。
type Kind int

const (
	None Kind = iota
	CR       // 单独的 \r
	LF       // 单独的 \n
	CRLF     // \r\n 配对
)

func (k Kind) String() string {
	switch k {
	case CR:
		return "CR"
	case LF:
		return "LF"
	case CRLF:
		return "CRLF"
	default:
		return "None"
	}
}

// Decoder 是有状态的逐字节行尾识别器，非并发安全。
type Decoder struct {
	pendingCR bool // 上一个字节是尚未定性的 \r
}

func NewDecoder() *Decoder { return &Decoder{} }

// Pending 报告当前是否有一个 \r 停留在切分点等待下一字节。
func (d *Decoder) Pending() bool { return d.pendingCR }

// Feed 喂入字节 b，返回因此产生的行尾事件（长度 0..2）。
// 若挂起的 \r 后接普通字节，挂起 \r 产生 CR；b 本身不是行尾。
func (d *Decoder) Feed(b byte) ([2]Kind, int) {
	var ev [2]Kind
	n := 0
	if d.pendingCR {
		switch {
		case b == '\n':
			ev[0], n = CRLF, 1
			d.pendingCR = false
		case b == '\r':
			ev[0], n = CR, 1 // 旧 \r 单独成行；新 \r 继续待定
		default:
			ev[0], n = CR, 1
			d.pendingCR = false
		}
		return ev, n
	}
	switch b {
	case '\r':
		d.pendingCR = true
	case '\n':
		ev[0], n = LF, 1
	}
	return ev, n
}

// Flush 在流结束时调用，把待定的 \r 判为单独 CR。
func (d *Decoder) Flush() (Kind, bool) {
	if d.pendingCR {
		d.pendingCR = false
		return CR, true
	}
	return None, false
}

// Reset 恢复初始状态。
func (d *Decoder) Reset() { d.pendingCR = false }
