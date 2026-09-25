package lexer

// 动作字节类顺序：',' '"' '\r' '\n' 其它。
type action uint8

const (
	aAdd action = iota
	aSep
	aLine
	aOpen
	aEsc
	aCR
	aCRH
	aBlank
	aBare
	aAfter
	aOrphan
)

var tab = [5][5]action{
	{aSep, aOpen, aCR, aBlank, aAdd},            // stF
	{aSep, aBare, aCRH, aLine, aAdd},            // stB
	{aAdd, aEsc, aAdd, aAdd, aAdd},              // stQ
	{aSep, aEsc, aCRH, aLine, aAfter},           // stQE
	{aOrphan, aOrphan, aOrphan, aLine, aOrphan}, // stCR
}

func class(c byte) int {
	if c == ',' {
		return 0
	}
	if c == '"' {
		return 1
	}
	if c == '\r' {
		return 2
	}
	if c == '\n' {
		return 3
	}
	return 4
}

// Feed 送入一段字节，可任意多次调用；错误后进入终态，每字节恰好处理一次。
func (l *Lexer) Feed(p []byte) error {
	if l.fatal != nil {
		return l.fatal
	}
	for i := 0; i < len(p); i++ {
		l.bytes++
		off, c := l.base+i, p[i]
		if l.state == stF {
			l.fStart = off
		}
		if e := l.act(tab[l.state][class(c)], off, c); e != nil {
			return e
		}
	}
	l.base += len(p)
	return nil
}

func (l *Lexer) act(a action, off int, c byte) *PosError {
	switch a {
	case aAdd:
		if e := l.addByte(off, c); e != nil {
			return e
		}
		if l.state == stF {
			l.state = stB
		}
	case aOpen:
		l.state, l.quoted = stQ, true
	case aEsc:
		if e := l.addByte(off, '"'); e != nil {
			return e
		}
		l.state = stQ
	case aSep:
		if e := l.emitField(off); e != nil {
			return e
		}
		l.state = stF
	case aLine:
		end := off
		if l.state == stCR {
			end = off - 1
		}
		if e := l.emitField(end); e != nil {
			return e
		}
		if e := l.endRecord(); e != nil {
			return e
		}
		l.state = stF
	case aCR:
		l.state, l.crField = stCR, l.recOpen
	case aCRH:
		l.state, l.crField = stCR, true
	case aBare:
		return l.fail(ErrBareQuote, off)
	case aAfter:
		return l.fail(ErrQuoteAfterClose, off)
	case aOrphan:
		return l.fail(ErrOrphanCR, off)
	}
	return nil
}

// Close 结束流并 flush 末尾字段（无尾换行合法）。
func (l *Lexer) Close() error {
	if l.fatal != nil {
		return l.fatal
	}
	switch l.state {
	case stQ:
		return l.fail(ErrUnclosedQuote, l.base)
	case stCR:
		off := l.base
		if !l.crField {
			off--
		}
		return l.fail(ErrOrphanCR, off)
	case stB, stQE:
		if e := l.emitField(l.base); e != nil {
			return e
		}
		return l.endRecord()
	}
	return nil
}
