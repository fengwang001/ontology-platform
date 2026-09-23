package qp

import "ontology/qpline"

// 解码器状态：normal 普通；cr 见到裸 \r；eq 见到 '='；
// eqCr 见到 "=\r"；hex1 见到 "=X" 等待第二个十六进制位。
const (
	stNormal = iota
	stCR
	stEq
	stEqCR
	stHex1
)

// Decoder 是流式严格解码器。错误携带整体输入流绝对偏移且与切分点无关；
// 出错前已成功解码的前缀保留在 Output 中。
type Decoder struct {
	out    []byte
	state  int
	hi     byte   // hex1 状态下 '=' 后的第一个十六进制字符
	pendWs []byte // 行内尚不能确认是否行末的尾部空白
	col    int    // 当前编码行已见字符数（用于 76 上限）
	offset int    // 已消费的总字节数（绝对偏移基准）
	closed bool
}

// NewDecoder 创建空的严格解码器。
func NewDecoder() *Decoder { return &Decoder{} }

func (d *Decoder) fail(err error, rel int) error {
	return &DecodeError{Err: err, Offset: d.offset + rel}
}

func (d *Decoder) emit(b byte) { d.out = append(d.out, b) }

// flushWS 把待定尾部空白确认为行内字面空白。
func (d *Decoder) flushWS() {
	d.out = append(d.out, d.pendWs...)
	d.pendWs = d.pendWs[:0]
}

func (d *Decoder) hardNL() { d.pendWs = d.pendWs[:0]; d.out = append(d.out, '\r', '\n'); d.col = 0 }

// Write 喂入一块编码数据。
func (d *Decoder) Write(p []byte) (int, error) {
	if d.closed {
		return 0, errClosed
	}
	for j := 0; j < len(p); j++ {
		b := p[j]
		switch d.state {
		case stNormal, stCR:
			if b == '\n' {
				if d.state == stCR {
					d.hardNL()
					d.state = stNormal
				} else {
					if len(d.pendWs) > 0 {
						return j, d.fail(ErrTrailingSpace, -len(d.pendWs))
					}
					d.hardNL()
				}
				d.offset++
				break
			}
			if d.state == stCR {
				return j, d.fail(ErrInvalidByte, -1)
			}
			switch {
			case b == '\r':
				d.state = stCR
			case b == '=':
				d.flushWS()
				d.state = stEq
				d.col++
			case qpline.IsHorizontalSpace(b):
				d.pendWs = append(d.pendWs, b)
				d.col++
			case qpline.IsPrintable(b):
				d.flushWS()
				d.emit(b)
				d.col++
			default:
				return j, d.fail(ErrInvalidByte, 0)
			}
			if d.col > qpline.MaxColumn {
				return j, d.fail(ErrLineTooLong, 0)
			}
		case stEq:
			switch {
			case qpline.IsHexDigit(b):
				d.hi = b
				d.state = stHex1
			case b == '\r':
				d.state = stEqCR
			default:
				// "=\n" 不是合法软换行（必须是 =\r\n），按坏转义处理。
				return j, d.fail(ErrBadEscape, 0)
			}
		case stEqCR:
			if b != '\n' {
				return j, d.fail(ErrBadEscape, -1)
			}
			// 软换行：删除 =\r\n。'=' 已计入上一行列计数（上限 76），新行归零。
			d.col = 0
			d.state = stNormal
		case stHex1:
			if !qpline.IsHexDigit(b) {
				return j, d.fail(ErrBadEscape, 0)
			}
			d.emit(unhex(d.hi)<<4 | unhex(b))
			d.state = stNormal
		}
		d.offset++
	}
	return len(p), nil
}

func unhex(b byte) byte {
	switch {
	case b >= '0' && b <= '9':
		return b - '0'
	case b >= 'A' && b <= 'F':
		return b - 'A' + 10
	default:
		return b - 'a' + 10
	}
}

// Close 标记输入结束，检查悬空的 '=' 或未完成转义/软换行，并校验行末空白。
func (d *Decoder) Close() error {
	if d.closed {
		return errClosed
	}
	d.closed = true
	switch d.state {
	case stEq, stEqCR, stHex1:
		return &DecodeError{Err: ErrUnexpectedEOF, Offset: d.offset - 1}
	case stCR:
		return &DecodeError{Err: ErrInvalidByte, Offset: d.offset - 1}
	}
	if len(d.pendWs) > 0 {
		return &DecodeError{Err: ErrTrailingSpace, Offset: d.offset - len(d.pendWs)}
	}
	return nil
}

// Output 返回截至目前成功解码的字节（出错前缀也会保留）。
func (d *Decoder) Output() []byte { return d.out }
