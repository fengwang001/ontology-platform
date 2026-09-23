// Package lexer 是可暂停、可续传的逐字节 CSV(RFC4180 方言) 词法状态机。
package lexer

import (
	"fmt"

	"ontology/cell"
)

// StateID 是状态机的状态。
type StateID int

const (
	StStart    StateID = iota // S 字段开始
	StUnquoted                // U 未引号字段中
	StQuoted                  // Q 引号字段中
	StQClose                  // QQ 引号字段中刚见到一个引号
	StCR                      // CR 行尾 CR 待定
)

// Kind 标识可判定的错误类别（四类语法错误 + 三类上限 + 终态写入）。
type Kind int

const (
	KNone          Kind = iota
	BareQuote           // 未引号字段中出现 "
	AfterQuote          // "ab"c：闭合引号后紧跟非分隔符非行尾
	UnclosedQuote       // 流结束时引号未闭合
	LoneCR              // 孤立 \r
	FieldTooLarge       // 单字段超上限
	TooManyFields       // 单记录字段数超上限
	TooManyRecords      // 总记录数超上限
	DeadLexer           // 终态后再次写入
	ColumnCount         // 记录列数与第一条记录不一致
)

// Error 携带类别与位置：字节偏移从 0 起，记录号/字段号从 1 起。
type Error struct {
	Kind   Kind
	Offset int
	Record int
	Field  int
}

func (e *Error) Error() string {
	return fmt.Sprintf("csv: kind=%d byte=%d record=%d field=%d", e.Kind, e.Offset, e.Record, e.Field)
}

// State 是可跨段传递的词法状态。
type State struct {
	St     StateID
	Quoted bool
}

// Emitter 接收词法事件。RecordEnd 的 blank=true 表示空行（应跳过）。
type Emitter interface {
	Field(c cell.Cell) error
	RecordEnd(blank bool) error
	Bad(e *Error) bool // 返回 true 终止
}

// Limits 是可配置上限；0 表示不限。
type Limits struct {
	MaxFieldBytes int
	MaxFields     int
}

// Lexer 是单线程、可半包续传的状态机；同一实例非并发安全。
type Lexer struct {
	em     Emitter
	lim    Limits
	st     StateID
	quoted bool
	val    []byte
	fstart int
	have   bool // 当前记录已产出过字段（含逗号）
	open   bool // 当前字段已开始（引号字段或已有内容）
	size   int
	off    int
	dead   *Error
	nproc  int64 // 非导出：字节被状态机处理的总次数
}

// New 创建状态机。
func New(em Emitter, lim Limits) *Lexer { return &Lexer{em: em, lim: lim, fstart: -1} }

// Resume 创建一个带内部入口状态的状态机，供 par 段跑使用：
// st 为段首真实词法状态；quoted 表示开放字段是否带引号；base 是该段首字节的全局偏移。
// 调用方负责在拼接时处理跨段开放字段。
func Resume(em Emitter, lim Limits, st StateID, quoted bool, base int) *Lexer {
	l := New(em, lim)
	l.st, l.quoted, l.off = st, quoted, base
	if st == StQuoted || st == StUnquoted || st == StQClose || st == StCR {
		l.open, l.have = true, true
	}
	return l
}

// State 返回当前内部状态与偏移（供段间衔接）。
func (l *Lexer) State() (st StateID, quoted bool, off int) {
	return l.st, l.quoted, l.off
}

// OpenValue 返回当前开放字段已积累的逻辑值（段末开放字段用）。
func (l *Lexer) OpenValue() (start int, quoted bool, v []byte, open bool) {
	return l.fstart, l.quoted, append([]byte(nil), l.val...), l.open || l.have
}

// Processed 返回字节被处理的总次数。
func (l *Lexer) Processed() int64 { return l.nproc }

// Dead 返回终态错误；非 nil 表示已终止。
func (l *Lexer) Dead() *Error { return l.dead }

func (l *Lexer) fail(k Kind, off, rec, fld int) error {
	if l.dead == nil {
		l.dead = &Error{Kind: k, Offset: off, Record: rec, Field: fld}
		l.em.Bad(l.dead)
	}
	return l.dead
}

func (l *Lexer) beginCell(off int, quoted bool) {
	l.open, l.quoted, l.fstart, l.size, l.val = true, quoted, off, 0, l.val[:0]
}

func (l *Lexer) add(b byte, off, rec, fld int) error {
	l.size++
	if l.lim.MaxFieldBytes > 0 && l.size > l.lim.MaxFieldBytes {
		return l.fail(FieldTooLarge, off, rec, fld)
	}
	l.val = append(l.val, b)
	return nil
}

// Feed 处理一段字节，可调用任意多次。
func (l *Lexer) Feed(p []byte) error {
	if l.dead != nil {
		return l.dead
	}
	for _, b := range p {
		l.nproc++
		off := l.off
		l.off++
		rec, fld := 1, 1
		if e := l.step(b, off, rec, fld); e != nil {
			return e
		}
	}
	return nil
}

func (l *Lexer) step(b byte, off, rec, fld int) error {
	switch l.st {
	case StStart:
		switch {
		case b == ',':
			if e := l.finishField(off, fld, rec); e != nil {
				return e
			}
			return l.newField(off + 1)
		case b == '"':
			l.beginCell(off, true)
			l.st = StQuoted
		case b == '\r':
			l.st = StCR
		case b == '\n':
			return l.endRecord(off)
		default:
			l.beginCell(off, false)
			if e := l.add(b, off, rec, fld); e != nil {
				return e
			}
			l.st = StUnquoted
		}
	case StUnquoted:
		switch {
		case b == ',':
			if e := l.finishField(off, fld, rec); e != nil {
				return e
			}
			return l.newField(off + 1)
		case b == '"':
			return l.fail(BareQuote, off, rec, fld)
		case b == '\r':
			l.st = StCR
		case b == '\n':
			if e := l.finishField(off, fld, rec); e != nil {
				return e
			}
			return l.endRecord(off)
		default:
			if e := l.add(b, off, rec, fld); e != nil {
				return e
			}
		}
	case StQuoted:
		if b == '"' {
			l.st = StQClose
		} else if e := l.add(b, off, rec, fld); e != nil {
			return e
		}
	case StQClose:
		switch {
		case b == '"':
			if e := l.add('"', off, rec, fld); e != nil {
				return e
			}
			l.st = StQuoted
		case b == ',':
			if e := l.finishField(off, fld, rec); e != nil {
				return e
			}
			return l.newField(off + 1)
		case b == '\r':
			l.st = StCR
		case b == '\n':
			if e := l.finishField(off, fld, rec); e != nil {
				return e
			}
			return l.endRecord(off)
		default:
			return l.fail(AfterQuote, off, rec, fld)
		}
	case StCR:
		if b != '\n' {
			return l.fail(LoneCR, off-1, rec, fld)
		}
		if l.open {
			if e := l.finishField(off-1, fld, rec); e != nil {
				return e
			}
		}
		return l.endRecord(off)
	}
	return nil
}

func (l *Lexer) newField(off int) error {
	l.have = true
	l.open, l.quoted = false, false
	l.fstart, l.size, l.val, l.st = off, 0, l.val[:0], StStart
	return nil
}

func (l *Lexer) finishField(off, fld, rec int) error {
	if !l.open {
		l.beginCell(l.fstart, false)
	}
	c := cell.Cell{Value: string(l.val), Quoted: l.quoted, Start: l.fstart, End: off}
	l.open = false
	return l.em.Field(c)
}

func (l *Lexer) endRecord(off int) error {
	blank := !l.have && !l.open
	l.have, l.open, l.quoted = false, false, false
	l.fstart, l.val, l.st = off+1, l.val[:0], StStart
	return l.em.RecordEnd(blank)
}

// Close 声明流结束。
func (l *Lexer) Close() error {
	if l.dead != nil {
		return l.dead
	}
	off := l.off
	switch l.st {
	case StQuoted, StQClose:
		return l.fail(UnclosedQuote, off, 1, 1)
	case StCR:
		return l.fail(LoneCR, off-1, 1, 1)
	}
	if l.have || l.open {
		if e := l.finishField(off, 1, 1); e != nil {
			return e
		}
		return l.em.RecordEnd(false)
	}
	return nil
}
