package lexer

import "ontology/cell"

func (l *Lexer) startField(pos int, quoted bool) {
	l.fstart, l.quoted, l.buf = pos, quoted, l.buf[:0]
}

func (l *Lexer) addByte(ch, pos int) error {
	if l.cfg.EnforceLimits && l.cfg.Limits.MaxFieldBytes > 0 && len(l.buf)+1 > l.cfg.Limits.MaxFieldBytes {
		return l.failAt(&LimitError{Kind: LimitFieldBytes}, pos)
	}
	l.buf = append(l.buf, byte(ch))
	return nil
}

func (l *Lexer) emitField(pos int) error {
	if l.cfg.EnforceLimits && l.cfg.Limits.MaxFields > 0 && l.field+1 > l.cfg.Limits.MaxFields {
		return l.failAt(&LimitError{Kind: LimitFields}, pos)
	}
	start := l.fstart
	if start < 0 {
		start = pos
	}
	l.h.Field(cell.Cell{Value: string(l.buf), Quoted: l.quoted, Start: start, End: pos})
	l.field, l.fstart, l.emitted = l.field+1, -1, true
	l.st = stStart
	return nil
}

func (l *Lexer) endRecord(pos int) error {
	if l.cfg.EnforceLimits && l.cfg.Limits.MaxRecords > 0 && l.rec+1 > l.cfg.Limits.MaxRecords {
		return l.failAt(&LimitError{Kind: LimitRecords}, pos)
	}
	l.h.Record()
	l.rec, l.field, l.emitted = l.rec+1, 0, false
	return nil
}

func (l *Lexer) closeLine(pos int) error {
	if e := l.emitField(pos); e != nil {
		return e
	}
	return l.endRecord(pos)
}

func (l *Lexer) step(ch byte) error {
	pos := l.off
	switch l.st {
	case stStart:
		switch {
		case ch == ',':
			l.startField(pos, false)
			if e := l.emitField(pos); e != nil {
				return e
			}
			return nil
		case ch == '"':
			l.startField(pos, true)
			l.st = stQuoted
		case ch == '\n':
			return l.newlineStart(pos)
		case ch == '\r':
			l.startField(pos, false)
			l.st = stCR
		default:
			l.startField(pos, false)
			if e := l.addByte(int(ch), pos); e != nil {
				return e
			}
			l.st = stBare
		}
	case stBare:
		switch {
		case ch == ',':
			return l.emitField(pos)
		case ch == '"':
			return l.failAt(ErrBareQuote, pos)
		case ch == '\n':
			l.st = stStart
			return l.closeLine(pos)
		case ch == '\r':
			l.st = stCR
		default:
			if e := l.addByte(int(ch), pos); e != nil {
				return e
			}
		}
	case stQuoted:
		if ch == '"' {
			l.st = stQuote
		} else if e := l.addByte(int(ch), pos); e != nil {
			return e
		}
	case stQuote:
		switch {
		case ch == ',':
			return l.emitField(pos)
		case ch == '"':
			if e := l.addByte('"', pos); e != nil {
				return e
			}
			l.st = stQuoted
		case ch == '\n':
			l.st = stStart
			return l.closeLine(pos)
		default:
			return l.failAt(ErrQuoteJunk, pos)
		}
	case stCR:
		if ch != '\n' {
			return l.failAt(ErrLoneCR, l.off-1)
		}
		l.st = stStart
		return l.closeLine(pos)
	}
	return nil
}

func (l *Lexer) eof() error {
	switch l.st {
	case stQuoted:
		return l.failAt(ErrUnclosedQuote, l.off)
	case stQuote, stBare:
		return l.closeLine(l.off)
	case stCR:
		return l.failAt(ErrLoneCR, l.off-1)
	case stStart:
		if l.field > 0 {
			l.startField(l.off, false)
			if e := l.emitField(l.off); e != nil {
				return e
			}
			return l.endRecord(l.off)
		}
	}
	return nil
}

func (l *Lexer) newlineStart(pos int) error {
	if l.field == 0 {
		l.h.SkipLine()
		return nil
	}
	l.startField(pos, false)
	if e := l.emitField(pos); e != nil {
		return e
	}
	return l.endRecord(pos)
}
