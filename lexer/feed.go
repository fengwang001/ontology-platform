package lexer

import "ontology/cell"

// Kind values used by the machine; declared here alongside the byte loop.
const (
	ErrBareQuote Kind = iota + 1
	ErrQuoteClosed
	ErrUnclosedQuote
	ErrColumnMismatch
	ErrStrayCR
	ErrFieldTooLong
	ErrTooManyFields
	ErrTooManyRecords
	ErrTerminal
)

// States used by the machine.
const (
	FS State = iota
	B
	Q
	QP
	CR
)

func (l *L) empty() { l.out(Item{Field: cell.Cell{Start: l.pos, End: l.pos}}) }

func (l *L) done(nl bool) {
	l.field(l.pos)
	if nl {
		l.out(Item{Line: l.pos})
	}
	l.state = FS
}

func (l *L) enterCR(open bool) {
	l.crPos, l.crOpen, l.state = l.pos, open, CR
}

// Feed pushes bytes; processing stops at the first error. Not concurrency safe.
func (l *L) Feed(p []byte) error {
	if l.term != nil {
		return &Error{Kind: ErrTerminal}
	}
	for i := 0; i < len(p); i++ {
		l.pos = l.base + i
		b := p[i]
		l.n++
		switch l.state {
		case FS:
			switch b {
			case '"':
				l.start, l.quoted, l.state = l.pos, true, Q
			case ',':
				l.empty()
			case '\n':
				l.out(Item{Line: l.pos})
			case '\r':
				l.enterCR(false)
			default:
				l.start, l.val, l.fbytes, l.state = l.pos, append(l.val, b), 1, B
			}
		case B:
			switch {
			case b == '"':
				l.fail(int(ErrBareQuote), l.pos)
			case b == ',', b == '\n':
				l.done(b == '\n')
			case b == '\r':
				l.enterCR(true)
			default:
				if !l.add(b) {
					i = len(p)
				}
			}
		case Q:
			if b == '"' {
				l.state = QP
			} else if !l.add(b) {
				i = len(p)
			}
		case QP:
			switch b {
			case '"':
				l.state = Q
				if !l.add('"') {
					i = len(p)
				}
			case ',', '\n':
				l.done(b == '\n')
			case '\r':
				l.enterCR(true)
			default:
				l.fail(int(ErrQuoteClosed), l.pos)
			}
		case CR:
			if b == '\n' {
				if l.crOpen {
					l.field(l.crPos)
				}
				l.out(Item{Line: l.pos})
				l.state = FS
			} else {
				l.fail(int(ErrStrayCR), l.crPos)
			}
		}
		if l.term != nil {
			break
		}
	}
	l.base += len(p)
	if l.term != nil {
		return l.term
	}
	return nil
}
