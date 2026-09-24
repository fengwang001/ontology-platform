// Package eol 识别行尾：CRLF、单独 CR、LF，以及 CR 在切分点处的待定状态。
package eol

// Event 是喂入一个字节后的行尾判定结果。
type Event uint8

const (
	// Data 表示该字节不是行尾的一部分，原样保留。
	Data Event = iota
	// LF 表示一个换行：当前字节是 LF（可能属于 CRLF）。
	LF
	// CRLFDropCR 表示当前字节是 CRLF 中的 CR，应删除；下个字节若是 LF 产出 LF。
	CRLFDropCR
	// LoneCR 表示一个待定 CR：前字节是 CR 且当前字节不是 LF，CR 单独成行。
	LoneCR
)

// Decider 是单实例、非并发安全的行尾状态机。
// 零值即可使用。
type Decider struct {
	pendingCR bool
}

// Feed 喂入一个字节并返回当前字节对应的事件。
// 当输入为 CR 且尚无待定时返回 CRLFDropCR（待定）；后续字节到来时，
// 若不是 LF，会额外以 LoneCR 报告上一个 CR 的命运。
func (d *Decider) Feed(b byte) (prev Event, cur Event) {
	if d.pendingCR {
		d.pendingCR = false
		prev = LoneCR
	}
	switch {
	case b == '\r':
		d.pendingCR = true
		cur = CRLFDropCR
	case b == '\n':
		cur = LF
	default:
		cur = Data
	}
	return prev, cur
}

// Flush 在流结束时调用；若最后一个字节是待定 CR，返回 LoneCR，否则 Data。
func (d *Decider) Flush() Event {
	if d.pendingCR {
		d.pendingCR = false
		return LoneCR
	}
	return Data
}

// Pending 报告当前是否有尚未定性的 CR。
func (d *Decider) Pending() bool { return d.pendingCR }
