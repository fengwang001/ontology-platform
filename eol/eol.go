// Package eol 识别跨切分点的行尾：\r\n、单独 \r、\n。
// \r 在见到下一个字节之前处于待定状态。本包不依赖其他包。
package eol

// Act 是喂入一个字节后产生的指令序列中的单条指令。
type Act struct {
	// Kind:
	//  ActCRLF  上一个待定 \r 与当前 \n 合成一个行尾，\r 被删；
	//  ActCR    上一个待定 \r 是单独行尾；
	//  ActLF    当前 \n 是行尾；
	//  ActKeep  当前字节原样保留；
	//  ActHold  当前 \r 进入待定，暂不输出。
	Kind int
}

const (
	ActCRLF = iota
	ActCR
	ActLF
	ActKeep
	ActHold
)

// Detector 是有状态的行尾识别器，非并发安全。
type Detector struct{ pend bool }

// New 创建识别器。
func New() *Detector { return &Detector{} }

// Feed 喂入字节 b，返回按原文顺序发生的指令（至多两条）。
// 先解析上一个待定 \r，再处理当前字节。
func (d *Detector) Feed(b byte) []Act {
	a := []Act{}
	if d.pend {
		if b == '\n' {
			a = append(a, Act{ActCRLF})
			d.pend = false
			return a
		}
		a = append(a, Act{ActCR})
		d.pend = false
	}
	switch b {
	case '\r':
		d.pend = true
		a = append(a, Act{ActHold})
	case '\n':
		a = append(a, Act{ActLF})
	default:
		a = append(a, Act{ActKeep})
	}
	return a
}

// Flush 在流结束时调用：待定 \r 作为单独行尾。
func (d *Detector) Flush() []Act {
	if !d.pend {
		return nil
	}
	d.pend = false
	return []Act{{ActCR}}
}

// Pending 报告当前是否有一个 \r 等待判定（供 par 探测段尾）。
func (d *Detector) Pending() bool { return d.pend }
