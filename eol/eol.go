// Package eol 识别流式字节流中的行尾：\r\n、单独的 \r、\n，
// 并处理 \r 落在切分点处的待定状态。不依赖其他包。
package eol

// Out 是 Feed 的结果：一个字节到达后状态机的全部动作。
type Out struct {
	NL    bool // 应输出一个 \n
	NLOff int  // 该 \n 对应原文行尾的首字节偏移
	NLLen int  // 该 \n 消费的原文字节数（1 或 2）
	Pass  bool // 当前字节是普通字节，调用方应继续处理
	End   bool // 当前字节本身是行尾的一部分（\r 或 \n）
}

// Scanner 是行尾识别状态机，唯一状态是「上一个字节是待定的 \r」。
type Scanner struct {
	pend bool
	off  int // 待定 \r 的原文偏移
}

// Feed 喂入一个字节；b 为字节值，off 为它在原文中的偏移。
func (s *Scanner) Feed(b byte, off int) (r Out) {
	switch {
	case b == '\r':
		if s.pend { // 前一个 \r 落单
			r.NL, r.NLOff, r.NLLen = true, s.off, 1
		}
		s.pend, s.off = true, off
		r.End = true
	case b == '\n':
		if s.pend { // \r\n 合成一个行尾
			r.NL, r.NLOff, r.NLLen = true, s.off, 2
			s.pend = false
		} else {
			r.NL, r.NLOff, r.NLLen = true, off, 1
		}
		r.End = true
	default:
		if s.pend { // \r 后接普通字节，\r 落单
			r.NL, r.NLOff, r.NLLen = true, s.off, 1
			s.pend = false
		}
		r.Pass = true
	}
	return r
}

// Finish 在流结束时调用：若还有待定的 \r，它落单成行尾。
func (s *Scanner) Finish() (nl bool, off int) { return s.pend, s.off }

// Pending 报告是否存在待定的 \r（供 par 拼接使用）。
func (s *Scanner) Pending() bool { return s.pend }
