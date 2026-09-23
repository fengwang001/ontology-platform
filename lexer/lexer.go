// Package lexer 是可半包续传、可按任意字节偏移分段的 CSV 逐字节状态机。
package lexer

import (
	"fmt"

	"ontology/cell"
)

// Kind 是四类可判定的语法错误分类。
type Kind int

const (
	KQuoteInUnquoted     Kind = iota + 1 // 未引号字段中出现 "
	KCharsAfterCloseQuote               // "ab"c
	KUnterminatedQuote                  // 流结束时引号未闭合
	KBareCR                             // 孤立的 \r
)

// Error 带字节偏移（从 0 起）；记录号/字段号由 table 层补充。
type Error struct{ Kind Kind; Offset int }

func (e *Error) Error() string {
	n := [...]string{"", "quote in unquoted field", "chars after closing quote", "unterminated quoted field", "bare carriage return"}
	return fmt.Sprintf("csv lexer: %s at byte %d", n[e.Kind], e.Offset)
}
func (e *Error) Is(t error) bool { x, ok := t.(*Error); return ok && (x.Kind == 0 || x.Kind == e.Kind) }

// KindError 返回分类哨兵错误，配合 errors.Is 使用。
func KindError(k Kind) error { return &Error{Kind: k} }

const (
	stField = iota
	stUnq
	stQuote
	stQseen
	stCR
	stCRq
)

type EvKind int

const (
	EvFStart EvKind = iota + 1 // 字段开始；Cont=承接前段跨段字段
	EvOpen                     // 开引号；Off=引号偏移
	EvRun                      // 原样入值的字节段
	EvQuote                    // "" 转义出的一个引号字符
	EvFEnd                     // 字段结束；Off=逗号，End=内容排他结尾
	EvREnd                     // 记录结束；Off=\n，End=内容排他结尾
)

// Event 是状态机原子事件，偏移均为全局字节偏移。
type Event struct {
	Kind     EvKind
	Off, End int
	Data     []byte
	Cont     bool
}

// Sink 接收事件。
type Sink interface{ Put(Event) error }

// Carry 是扫描结束时跨越段边界的悬置状态。
type Carry struct{ InQuote, InS, InCR, CRInQ bool }

type machine struct{ st, nf int }

func errAt(k Kind, off int) *Error { return &Error{k, off} }

// SegmentScan 扫描 buf（首字节全局偏移 off0）；startInQuote 为起点假设。
// 每字节恰好处理一次；跨段状态仅由返回的 Carry 表达，函数自身无缓冲。
func SegmentScan(buf []byte, off0 int, startInQuote bool, s Sink) (Carry, *Error) {
	m := machine{st: stField}
	if startInQuote {
		m.st = stQuote
	}
	put := func(k EvKind, off, end int, d []byte, cont bool) { _ = s.Put(Event{k, off, end, d, cont}) }
	run := func(i int) { put(EvRun, off0+i, 0, buf[i:i+1], false) }
	fstart := func(off int, cont bool) { m.nf++; put(EvFStart, off, 0, nil, cont) }
	finishRec := func(i, end int) {
		put(EvREnd, off0+i, end, nil, false)
		m.nf, m.st = 0, stField
		if i+1 < len(buf) {
			fstart(off0+i+1, false)
		}
	}
	fstart(off0, startInQuote)
	for i := 0; i < len(buf); i++ {
		off, b := off0+i, buf[i]
		switch m.st {
		case stField:
			switch {
			case b == '"':
				put(EvOpen, off, 0, nil, false)
				m.st = stQuote
			case b == ',':
				put(EvFEnd, off, off, nil, false)
				fstart(off+1, false)
			case b == '\n':
				finishRec(i, off)
			case b == '\r':
				m.st = stCR
			default:
				run(i)
				m.st = stUnq
			}
		case stUnq:
			switch {
			case b == '"':
				return Carry{}, errAt(KQuoteInUnquoted, off)
			case b == ',':
				put(EvFEnd, off, off, nil, false)
				fstart(off+1, false)
				m.st = stField
			case b == '\n':
				finishRec(i, off)
			case b == '\r':
				m.st = stCR
			default:
				run(i)
			}
		case stQuote:
			if b == '"' {
				m.st = stQseen
			} else {
				run(i)
				if b == '\r' {
					m.st = stCRq
				}
			}
		case stQseen:
			switch {
			case b == '"':
				put(EvQuote, off, 0, []byte{'"'}, false)
				m.st = stQuote
			case b == ',':
				put(EvFEnd, off, off, nil, false)
				fstart(off+1, false)
				m.st = stField
			case b == '\n':
				finishRec(i, off)
			case b == '\r':
				m.st = stCR
			default:
				return Carry{}, errAt(KCharsAfterCloseQuote, off)
			}
	case stCR, stCRq:
			in := m.st == stCRq
			if b != '\n' {
				return Carry{}, errAt(KBareCR, off-1)
			}
			if in {
				run(i)
			}
			end := off - 1
			if in {
				end = off + 1
			}
			finishRec(i, end)
		}
	}
	return Carry{m.st == stQuote, m.st == stQseen, m.st == stCR || m.st == stCRq, m.st == stCRq}, nil
}

var _ = cell.Cell{}
