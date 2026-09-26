// Package lexer 是逐字节、可暂停续传的 CSV 状态机。
package lexer

import "ontology/cell"

const (
	sField = iota // 记录的第一个字段位置（无逗号前）
	sAfterComma   // 逗号之后的字段位置
	sRaw          // 未引号字段中
	sQ            // 引号字段中
	sQ1           // 引号字段中刚见一个引号
	sCR           // 行尾 CR 待定
)

// Sink 接收词法事件，坐标均为全局字节偏移。
type Sink interface {
	Field(c cell.Cell)
	RecordEnd(offset int)
}

// Machine 是单次顺序状态机，非并发安全。
type Machine struct {
	st             int
	buf            []byte
	fieldStart     int
	quoted         bool
	opened         bool
	rowFields      int
	rec, fld       int
	off            int
	lim            cell.Limits
	sink           Sink
	fatal          error
	bytesProcessed int // 非导出：字节被处理总次数
}

// 假设起点模式，供 par 使用。
const (
	StartField      = 0
	StartAfterComma = 1
	StartRaw        = 2
	StartQuote      = 3
	StartCR         = 4
)

// New 以指定起点模式构造状态机。
func New(sink Sink, lim cell.Limits, mode int) *Machine {
	m := &Machine{sink: sink, lim: lim, rec: 1, fld: 1}
	switch mode {
	case StartAfterComma:
		m.st = sAfterComma
	case StartRaw:
		m.st, m.opened = sRaw, true
	case StartQuote:
		m.st, m.opened, m.quoted, m.fieldStart = sQ, true, true, 0
	case StartCR:
		m.st = sCR
	default:
		m.st = sField
	}
	return m
}

// Bytes 返回字节被状态机处理的总次数。
func (m *Machine) Bytes() int { return m.bytesProcessed }

// Fatal 返回终态错误。
func (m *Machine) Fatal() error { return m.fatal }

// EndMode 返回段末状态模式（StartQuote 含 Q1）。
func (m *Machine) EndMode() int {
	switch m.st {
	case sRaw:
		return StartRaw
	case sQ, sQ1:
		return StartQuote
	case sCR:
		return StartCR
	case sAfterComma:
		return StartAfterComma
	default:
		return StartField
	}
}

// InProgress 返回段末挂起字段前缀（par 拼接跨段字段用）。
func (m *Machine) InProgress() (prefix string, quoted, has bool, start int) {
	return string(m.buf), m.quoted, m.opened, m.fieldStart
}

// Prefill 注入跨段挂起字段前缀并对齐偏移。
func (m *Machine) Prefill(prefix string, quoted bool, start, nextOff int) {
	m.buf = append(m.buf, prefix...)
	m.fieldStart, m.quoted, m.opened = start, quoted, true
	m.off = nextOff
}

// RecordCount 返回已闭合记录数（含空行）。
func (m *Machine) RecordCount() int { return m.rec - 1 }

// FieldsEmitted 返回已发射字段总数（段内坐标换算用）。
func (m *Machine) FieldsEmitted() int { return m.fld - 1 }

func (m *Machine) fail(err error, at int) error {
	if m.fatal == nil {
		m.fatal = &cell.PosError{Err: err, Offset: at, Record: m.rec, Field: m.fld}
	}
	return m.fatal
}

func (m *Machine) emitField(end int) error {
	if !m.opened {
		m.fieldStart = end
	}
	if m.lim.MaxFields > 0 && m.fld > m.lim.MaxFields {
		return m.fail(cell.ErrTooManyFields, m.fieldStart)
	}
	m.sink.Field(cell.Cell{Value: string(m.buf), Quoted: m.quoted, Start: m.fieldStart, End: end})
	m.rowFields++
	m.fld++
	return nil
}

func (m *Machine) closeRecord(at int) {
	m.sink.RecordEnd(at)
	m.rec++
	m.fld = 1
	m.buf = m.buf[:0]
	m.opened, m.quoted, m.rowFields = false, false, 0
	m.st = sField
}

func (m *Machine) resetField() {
	m.opened, m.quoted, m.buf, m.st = false, false, m.buf[:0], sAfterComma
}

func (m *Machine) addData(b byte, at int) error {
	if m.lim.MaxFieldBytes > 0 && len(m.buf) >= m.lim.MaxFieldBytes {
		return m.fail(cell.ErrFieldTooLarge, at)
	}
	m.buf = append(m.buf, b)
	return nil
}

// Feed 送入任意长度的一段字节，可多次调用。
func (m *Machine) Feed(p []byte) error {
	if m.fatal != nil {
		return m.fatal
	}
	for _, b := range p {
		at := m.off
		m.off++
		m.bytesProcessed++
		switch m.st {
		case sField, sAfterComma, sRaw:
			switch {
			case b == ',':
				if err := m.emitField(at); err != nil {
					return err
				}
				m.resetField()
			case b == '"':
				if m.st == sRaw {
					return m.fail(cell.ErrBareQuote, at)
				}
				m.opened, m.quoted, m.st, m.fieldStart = true, true, sQ, at
			case b == '\n':
				if m.rowFields > 0 {
					if err := m.emitField(at); err != nil {
						return err
					}
				}
				m.closeRecord(at)
			case b == '\r':
				if m.rowFields > 0 {
					if err := m.emitField(at); err != nil {
						return err
					}
				}
				m.opened, m.quoted, m.buf, m.st = false, false, m.buf[:0], sCR
			default:
				if !m.opened {
					m.fieldStart, m.opened = at, true
				}
				if err := m.addData(b, at); err != nil {
					return err
				}
				m.st = sRaw
			}
		case sQ:
			if b == '"' {
				m.st = sQ1
			} else if err := m.addData(b, at); err != nil {
				return err
			}
		case sQ1:
			switch {
			case b == '"':
				if err := m.addData('"', at); err != nil {
					return err
				}
				m.st = sQ
			case b == ',':
				if err := m.emitField(at); err != nil {
					return err
				}
				m.resetField()
			case b == '\n':
				if err := m.emitField(at); err != nil {
					return err
				}
				m.closeRecord(at)
			case b == '\r':
				if err := m.emitField(at); err != nil {
					return err
				}
				m.opened, m.quoted, m.buf, m.st = false, false, m.buf[:0], sCR
			default:
				return m.fail(cell.ErrBareQuote, at)
			}
		case sCR:
			if b == '\n' {
				m.closeRecord(at)
			} else {
				return m.fail(cell.ErrBareCR, at-1)
			}
		}
	}
	return m.fatal
}

// Close 宣告流结束。
func (m *Machine) Close() error {
	if m.fatal != nil {
		return m.fatal
	}
	switch m.st {
	case sQ:
		return m.fail(cell.ErrUnclosedQuote, m.off-1)
	case sCR:
		return m.fail(cell.ErrBareCR, m.off-1)
	case sQ1, sRaw, sAfterComma:
		if err := m.emitField(m.off); err != nil {
			return err
		}
		m.closeRecord(m.off)
	case sField:
		if m.opened {
			if err := m.emitField(m.off); err != nil {
				return err
			}
			m.closeRecord(m.off)
		}
	}
	return m.fatal
}
