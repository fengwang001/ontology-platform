// Package eol 识别三种行尾（\r\n、单独的 \r、\n），并处理 \r 落在
// 切分点处的待定状态。不依赖其他包。
package eol

// Ev 是一个已判定的输入单元：要么是一个行尾，要么是一个内容字节。
type Ev struct {
	EOL   bool // true 表示行尾，false 表示内容字节
	B     byte // 内容字节（EOL 为 true 时无效）
	Start int  // 该单元在原文中的起始偏移
	Width int  // 占用的原文字节数：1，或 2（\r\n，其首字节 \r 应记为删除）
}

// Mach 是行尾识别状态机。唯一的跨字节状态是一个待定的 '\r'：
// 只有看到下一字节（或流结束）才能判定它是 \r\n 还是单独的 \r。
type Mach struct {
	pend    bool
	pendPos int
}

// Feed 消费位于原文偏移 pos 的字节 b，返回 0 到 2 个已判定单元。
// b 为 '\r' 时不返回任何单元（进入待定态）。
func (m *Mach) Feed(b byte, pos int) []Ev {
	var out []Ev
	if m.pend {
		if b == '\n' {
			m.pend = false
			return []Ev{{EOL: true, Start: m.pendPos, Width: 2}}
		}
		out = append(out, Ev{EOL: true, Start: m.pendPos, Width: 1})
		m.pend = false
	}
	switch b {
	case '\r':
		m.pend, m.pendPos = true, pos
	case '\n':
		out = append(out, Ev{EOL: true, Start: pos, Width: 1})
	default:
		out = append(out, Ev{B: b, Start: pos, Width: 1})
	}
	return out
}

// Pending 报告是否存在待定的 '\r' 及其原文偏移（供 par 提取段尾尾巴）。
func (m *Mach) Pending() (int, bool) { return m.pendPos, m.pend }

// Close 在流结束时结算：待定的 '\r' 判定为单独的行尾。
func (m *Mach) Close() []Ev {
	if !m.pend {
		return nil
	}
	m.pend = false
	return []Ev{{EOL: true, Start: m.pendPos, Width: 1}}
}
