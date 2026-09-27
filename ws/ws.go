// Package ws 对行尾空白（空格与制表符）做延迟判定。
//
// 只有看到行尾（LineEnded）或流结束（Close）才知道一串空白是否在行尾：
// 行尾则整串丢弃；遇到普通字节则整串冲刷为保留。切分点处未判定的空白可被
// par 层作为 carry 取回（Pending）。
package ws

// Kind 为判定事件种类。
type Kind uint8

const (
	// Whitespace 为刚进入待定缓冲的一个空白字节。
	Whitespace Kind = iota
	// Kept 为非空白字节：它之前待定的空白确认为行中空白（由调用方冲刷）。
	Kept
)

// Event 为 Feed 的判定事件。
type Event struct {
	Kind Kind
	Byte byte
}

// Tracker 维护当前行尾待定空白，单实例不要求并发安全。
type Tracker struct {
	pending []byte
}

// New 返回空状态追踪器。
func New() *Tracker { return &Tracker{} }

// Feed 喂入一个“非行尾”字节（行尾事件须由调用方先调用 LineEnded 处理）。
// 空白返回单事件并入待定缓冲；非空白返回冲刷事件 + Kept 事件，并清空缓冲。
func (t *Tracker) Feed(b byte) []Event {
	if b == ' ' || b == '\t' {
		t.pending = append(t.pending, b)
		return []Event{{Kind: Whitespace, Byte: b}}
	}
	ev := make([]Event, 0, len(t.pending)+1)
	for _, w := range t.pending {
		ev = append(ev, Event{Kind: Whitespace, Byte: w})
	}
	t.pending = t.pending[:0]
	ev = append(ev, Event{Kind: Kept, Byte: b})
	return ev
}

// LineEnded 在行尾确定时调用，丢弃整串待定空白（它们是行尾空白）。
func (t *Tracker) LineEnded() { t.pending = t.pending[:0] }

// Close 用于整流结束：按行尾空白丢弃待定空白并清空。
func (t *Tracker) Close() { t.pending = t.pending[:0] }

// Pending 返回当前待定空白（拷贝），供 par 在切分点取回 carry。
func (t *Tracker) Pending() []byte {
	out := make([]byte, len(t.pending))
	copy(out, t.pending)
	return out
}

// PendingLen 返回待定空白字节数。
func (t *Tracker) PendingLen() int { return len(t.pending) }

// Reset 清空所有状态。
func (t *Tracker) Reset() { t.pending = t.pending[:0] }
