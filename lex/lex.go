// Package lex 是 POSIX shell 风格分词的逐字节状态机。
// 它只做引号与反斜杠处理，不做变量展开、通配或命令替换。
// 机器可在任意字节边界暂停并续传：Step 每次处理一个字节。
package lex

// Status 是一次喂入结束后的收尾状态。
type Status int

const (
	StatusOK         Status = iota // 已完整闭合
	StatusOpenSingle               // 单引号未闭合
	StatusOpenDouble               // 双引号未闭合
	StatusTrailingBS               // 普通态以单个反斜杠结尾
)

// Op 是处理一个字节后产生的动作。
type Op int

const (
	OpNone Op = iota // 字节被删除（续行），无输出
	OpLit            // 输出一个字面字节
	OpSep            // 词分隔（空白）
)

// Event 是 Step 的结果。Op 为 OpLit 时 Ch 为字面字节；
// Keep 非空时须先把 Keep 作为字面字节输出（双引号中 `\` 被保留的情形）。
// Mark 表示此处显式开始一个词（开引号），可能与 Keep 同时出现。
type Event struct {
	Op   Op
	Ch   byte
	Keep []byte
	Mark bool
}

const (
	stPlain     = iota // 普通（无引号）
	stSingle           // 单引号内
	stDouble           // 双引号内
	stEscPlain         // 普通态中刚读到 \
	stEscDouble        // 双引号内刚读到 \
)

// Machine 是可暂停续传的状态机。零值即可使用。
type Machine struct {
	st   int
	open int    // 当前开引号的绝对字节偏移
	pos  int    // 已处理字节数（即下一字节的偏移）
	n    uint64 // 非导出计数器：字节被处理的总次数
}

// New 返回一个全新的状态机。
func New() *Machine { return &Machine{} }

// BytesProcessed 返回字节被状态机处理的总次数。
func (m *Machine) BytesProcessed() uint64 { return m.n }

// Position 返回已处理的字节数（下一字节的绝对偏移）。
func (m *Machine) Position() int { return m.pos }

// Step 处理单个字节 b，返回对应事件。绝对偏移由喂入顺序隐式决定。
func (m *Machine) Step(b byte) Event {
	m.n++
	m.pos++
	switch m.st {
	case stSingle:
		if b == '\'' {
			m.st = stPlain
			return Event{Op: OpNone}
		}
		return Event{Op: OpLit, Ch: b}
	case stDouble:
		switch b {
		case '"':
			m.st = stPlain
			return Event{Op: OpNone}
		case '\\':
			m.st = stEscDouble
			return Event{Op: OpNone}
		default:
			return Event{Op: OpLit, Ch: b}
		}
	case stEscPlain:
		m.st = stPlain
		if b == '\n' {
			return Event{Op: OpNone} // 续行：两字符删除，不 mark
		}
		return Event{Op: OpLit, Ch: b}
	case stEscDouble:
		m.st = stDouble
		switch b {
		case '$', '`', '"', '\\':
			return Event{Op: OpLit, Ch: b}
		case '\n':
			return Event{Op: OpNone} // 双引号内续行
		default:
			return Event{Op: OpLit, Keep: []byte{'\\', b}}
		}
	default: // stPlain
		switch b {
		case ' ', '\t', '\n', '\r':
			return Event{Op: OpSep}
		case '\'':
			m.st = stSingle
			m.open = m.pos - 1
			return Event{Op: OpNone, Mark: true}
		case '"':
			m.st = stDouble
			m.open = m.pos - 1
			return Event{Op: OpNone, Mark: true}
		case '\\':
			m.st = stEscPlain
			return Event{Op: OpNone}
		default:
			return Event{Op: OpLit, Ch: b}
		}
	}
}

// Finish 在所有字节喂完后报告收尾状态与相关偏移。
func (m *Machine) Finish() (status Status, offset int) {
	switch m.st {
	case stSingle:
		return StatusOpenSingle, m.open
	case stDouble, stEscDouble:
		return StatusOpenDouble, m.open
	case stEscPlain:
		return StatusTrailingBS, m.pos - 1
	default:
		return StatusOK, 0
	}
}
