// Package lexer 是可暂停续传的逐字节 CSV 状态机，也供 par 段假设使用。
package lexer

import "ontology/cell"

// EvKind 是状态机事件种类。
type EvKind int

const (
	EOpen EvKind = iota // 字段开始；Q=true 表示引号字段
	EData               // 字段内容片段 [S,E)，已反转义
	EClose              // 字段结束（逗号、换行、EOF），E 为字段末偏移
	ERec                // 记录结束，O 指向 \n（CRLF 时为 \n）
	EErr                // 错误，K 为错误种类，O 为字节偏移
)

// Ev 是一个状态机事件，偏移均为绝对字节偏移。
type Ev struct {
	K EvKind
	Q bool
	S int
	E int
	O int
	Kd cell.Kind
}

// Tail 描述一段输入处理结束时机器停在的位置。
type Tail int

const (
	TBoundary Tail = iota // 记录边界，下一字符起是新字段或空行
	TPending              // 逗号后，下一字符起是新字段（必有逗号）
	TUnquoted             // 未引号字段中间
	TQuoted               // 引号字段内部
	TQE                   // 引号字段内刚见一个引号
	TCR                   // 未引号字段末见 \r 待定
)

// SegResult 是一段输入在一种段首假设下的产物。
type SegResult struct {
	Ev   []Ev
	Tail Tail
}

const (
	sBoundary = iota
	sPlain
	sQuote
	sQE
	sCR
)

// bytesProcessed 记录字节被状态机处理的总次数（非导出，无回扫）。

// RunSegment 在 [base,base+len(p)) 上以给定假设运行：inside=true 表示段首
// 已在引号字段内；pending=true 表示段首紧跟逗号；crOff>=0 表示段首前有一个
// 待定 \r。maxField<=0 表示不限字段长度。
func RunSegment(p []byte, base int, inside, pending bool, crOff, maxField int) SegResult {
	m := machine{max: maxField}
	m.st, m.pending, m.crOff = sBoundary, pending, crOff
	if inside {
		m.st = sQuote
		m.qStart = base
	}
	m.feed(p, base)
	m.finish()
	return SegResult{Ev: m.out, Tail: m.tail()}
}

// Lexer 是可多次 Feed、最后 Close 的流式状态机。非并发安全。
type Lexer struct {
	m machine
}

// New 创建流式 lexer。emit 在每个事件产生时被同步调用。
func New(maxField int, emit func(Ev)) *Lexer { return &Lexer{m: machine{max: maxField, emit: emit}} }

// Bytes 返回状态机处理过的字节总数。
func (l *Lexer) Bytes() int { return l.m.n }

// Feed 喂入一段字节；进入终态后返回同一个错误。
func (l *Lexer) Feed(p []byte) error {
	base := l.m.abs
	l.m.feed(p, base)
	if l.m.dead != nil {
		return l.m.dead
	}
	return nil
}

// Close 结束流，处理 EOF 语义；终态后返回同一个错误。
func (l *Lexer) Close() error {
	l.m.finish()
	return l.m.dead
}
