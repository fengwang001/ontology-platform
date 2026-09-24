// Package ws 延迟判定行尾空白：只有看到行尾或流结束时，
// 一串连续的空格/制表符才知道是否位于行尾。不依赖其他包。
package ws

// Action 告诉调用方对当前字节与此前缓冲空白如何处理。
type Action uint8

const (
	// Emit：直接输出当前字节（普通字符；缓冲为空）。
	Emit Action = iota
	// Hold：当前字节是空白，先缓冲，暂不输出也不丢弃。
	Hold
	// EmitHeld：当前字节是普通字符，此前缓冲的空白判定为行内空白，
	// 应先把缓冲空白原样输出，再输出当前字节。
	EmitHeld
	// DropHeld：当前字节是行尾，此前缓冲的空白判定为行尾空白，
	// 应整串删除（不输出），行尾事件照常处理。
	DropHeld
)

// Tracker 是行尾空白判定器。limit<=0 表示不限；缓冲空白数超过 limit
// 时 Hold 返回超限，由上层拒绝（见 DESIGN.md 第 3 节）。
type Tracker struct {
	held  int
	limit int
}

// New 创建判定器，limit 为空白缓冲上限（<=0 不限）。
func New(limit int) *Tracker { return &Tracker{limit: limit} }

// Reset 清空缓冲。
func (t *Tracker) Reset() { t.held = 0 }

// Held 返回当前缓冲（未判定）空白字节数。
func (t *Tracker) Held() int { return t.held }

// IsSpace 报告字节是否为参与判定的空白（空格或制表符）。
func IsSpace(b byte) bool { return b == ' ' || b == '\t' }

// Feed 送入一个非行尾的普通字节，返回应对它与缓冲空白采取的动作。
// ok=false 表示缓冲已超上限（仅空白字节可能触发）。
func (t *Tracker) Feed(b byte) (act Action, ok bool) {
	if IsSpace(b) {
		t.held++
		if t.limit > 0 && t.held > t.limit {
			return Hold, false
		}
		return Hold, true
	}
	if t.held > 0 {
		t.held = 0
		return EmitHeld, true
	}
	return Emit, true
}

// EndLine 在行尾落定时调用：缓冲空白判定为行尾空白并清空。
func (t *Tracker) EndLine() Action {
	if t.held > 0 {
		t.held = 0
		return DropHeld
	}
	return Emit
}

// End 在流结束时调用：无行尾跟随，缓冲空白判定为行内空白，原样输出。
func (t *Tracker) End() Action {
	if t.held > 0 {
		t.held = 0
		return EmitHeld
	}
	return Emit
}
