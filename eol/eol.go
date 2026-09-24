// Package eol 识别跨行尾：\r\n、单独 \r、\n，以及切分点处待定的 \r。
package eol

// Decoder 是逐字节行尾状态机。Step 返回该字节的处理结果：
// nl=true 表示应在当前原文偏移处输出一个规范化换行 \n；advance=false 表示
// 当前字节尚未被消费，调用方必须用同一字节再次调用 Step（用于先冲刷待定 \r）。
type Decoder struct {
	pendingCR bool
}

// Step 喂入一个字节。
func (d *Decoder) Step(b byte) (nl, advance bool) {
	if d.pendingCR {
		if b == '\n' {
			d.pendingCR = false
			return true, true // \r\n：唯一的 \n 锚定在存活的 \n 上，\r 为删除
		}
		// 待定 \r 被确认是独立行尾：先输出锚定在 \r 上的 \n，当前字节退回重喂
		d.pendingCR = false
		if b == '\r' {
			d.pendingCR = false // 重喂时会重新置位
		}
		return true, false
	}
	switch b {
	case '\r':
		d.pendingCR = true
		return false, true // 挂起，等待下一字节
	case '\n':
		return true, true
	default:
		return false, true
	}
}

// Flush 在流结束时调用：返回 true 表示待定 \r 应作为独立行尾输出，
// 换行锚定在该 \r 的原文偏移上。
func (d *Decoder) Flush() (nl bool) {
	nl = d.pendingCR
	d.pendingCR = false
	return nl
}

// PendingCR 报告当前是否有一个 \r 正等待下一字节确认（切分点事实）。
func (d *Decoder) PendingCR() bool { return d.pendingCR }
