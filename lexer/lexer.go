// Package lexer 是可暂停续传的逐字节 CSV 状态机。
package lexer

import "ontology/cell"

// Sink 接收词法事件；偏移均为全局字节偏移。
type Sink interface {
	Field(c cell.Cell)
	Record(endOffset int)
}

// Limits 是可配置上限；0 表示不限。
type Limits struct {
	MaxFieldBytes int
	MaxFields      int
	MaxRecords     int
}

// Lexer 逐字节解析，可多次 Feed，最后 Close。非并发安全。
type Lexer struct {
	processed int
}

// New 构造词法器；baseRecord 为起始记录号（par 用），base 为缓冲全局起始偏移。
func New(sink Sink, lim Limits, baseOffset, baseRecord int) *Lexer {
	return &Lexer{}
}

// Feed 喂入一段字节。
func (l *Lexer) Feed(p []byte) error { return nil }

// Close 结束流并 flush 残余记录。
func (l *Lexer) Close() error { return nil }

// Processed 返回字节被状态机处理的总次数。
func (l *Lexer) Processed() int { return l.processed }
