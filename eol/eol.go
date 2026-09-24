// Package eol 识别混杂行尾：\r\n、单独 \r、\n，以及切分点处待定的 \r。
package eol

// Decoder 是逐字节流式行尾识别器。零值可用。
// 喂入一个字节，返回该字节应被消费的方式。
type Decoder struct {
	pendingCR bool
}

// Event 描述一个输入字节相对行尾的角色。
type Event uint8

const (
	// Literal 普通字节（含 \n 在非 CRLF 情形，但 \n 本身仍以 LF 事件给出）。
	Literal Event = iota
	// LF 本字节是一次行尾的换行位置（可能与前一个 \r 配对）。
	LF
	// CR 本字节是一个独立行尾（\r 后不跟 \n，或 \r 位于流末尾）。
	CR
	// CRPending 本字节是 \r，是否与下一字节配成 CRLF 尚不确定。
	CRPending
)

// Feed 喂入字节 b。返回 (新事件, 解决事件)。
// 新事件描述 b；解决事件描述此前待定 \r 的定性：
//   - b==\n 且此前有 pending \r：CRPending（该 \r 并入 CRLF，不算独立行尾）；
//   - b 为其他字节且此前有 pending \r：CR（该 \r 是独立行尾，先于 b 生效）；
//   - 其余：Literal。
//
// 注意：\r 一律先以 CRPending 返回并挂起，\n 一律以 LF 返回。
func (d *Decoder) Feed(b byte) (Event, Event) {
	if b == '\r' {
		resolved := Literal
		if d.pendingCR {
			resolved = CR // 连续两个 \r：前一个是独立行尾
		}
		d.pendingCR = true
		return CRPending, resolved
	}
	if b == '\n' {
		if d.pendingCR {
			d.pendingCR = false
			return LF, CRPending
		}
		return LF, Literal
	}
	if d.pendingCR {
		d.pendingCR = false
		return Literal, CR
	}
	return Literal, Literal
}

// End 通知流结束。若有待定 \r，返回 CR（它是独立行尾），否则 Literal。
func (d *Decoder) End() Event {
	if d.pendingCR {
		d.pendingCR = false
		return CR
	}
	return Literal
}

// Pending 报告是否存在尚未定性的 \r。
func (d *Decoder) Pending() bool { return d.pendingCR }

// Reset 复位到零值状态。
func (d *Decoder) Reset() { d.pendingCR = false }
