// Package lexer is a resumable byte-at-a-time CSV (RFC 4180 dialect) state
// machine. Each input byte is processed exactly once; no rescanning.
package lexer

// Boundary classes used to resume parsing a chunk at an arbitrary offset.
const (
	AtIdle      = iota // outside a field, at a record boundary or chunk start
	AtEmpty            // empty unquoted field slot already opened
	AtUnquoted         // inside an unquoted field
	AtQuoted           // inside a quoted field; next byte is content
	AtQQuote           // inside a quoted field, just saw the first quote of ""
	AtCR               // unquoted field ended with a pending CR
	AtCRAfter          // quoted-closed field followed by a pending CR
)

// Emitter receives lifecycle events. Data aliases the feed buffer. Begin for
// a continuation chunk carries cont=true for the field carried over.
type Emitter interface {
	Begin(pos int, quoted, cont bool) error
	Data(b []byte, s, e int) error
	End(pos int) error
	Record(pos int) error
}

// Lexer is single-use and NOT safe for concurrent use.
type Lexer struct {
	emit  Emitter
	st    int
	off   int
	beg   int
	bytes int64
}

// New builds a lexer. entry is an At* state; off is the offset of byte 0.
func New(em Emitter, entry, off int) *Lexer {
	l := &Lexer{emit: em, st: entry, off: off, beg: off}
	return l
}

// State reports the boundary class at the current machine position.
func (l *Lexer) State() int { return l.st }

// Bytes counts input bytes handled (each exactly once).
func (l *Lexer) Bytes() int64 { return l.bytes }

// Offset reports the offset of the next input byte.
func (l *Lexer) Offset() int { return l.off }

// Feed processes one chunk.
func (l *Lexer) Feed(p []byte) error {
	for _, ch := range p {
		pos := l.off
		l.off++
		l.bytes++
		if err := l.step(ch, pos); err != nil {
			return err
		}
	}
	return nil
}

func (l *Lexer) step(ch byte, pos int) error {
	switch l.st {
	case AtIdle:
		switch ch {
		case '"':
			l.beg = pos
			if err := l.emit.Begin(pos, true, false); err != nil {
				return err
			}
			l.st = AtQuoted
		case ',':
			l.beg = pos
			if err := l.emit.Begin(pos, false, false); err != nil {
				return err
			}
			if err := l.emit.End(pos); err != nil {
				return err
			}
			l.st = AtEmpty
		case '\n':
			l.beg = pos
			if err := l.emit.Begin(pos, false, false); err != nil {
				return err
			}
			if err := l.emit.End(pos); err != nil {
				return err
			}
			if err := l.emit.Record(pos + 1); err != nil {
				return err
			}
			l.st = AtIdle
		case '\r':
			l.beg = pos
			if err := l.emit.Begin(pos, false, false); err != nil {
				return err
			}
			l.st = AtCR
		default:
			l.beg = pos
			if err := l.emit.Begin(pos, false, false); err != nil {
				return err
			}
			if err := l.emit.Data([]byte{ch}, pos, pos+1); err != nil {
				return err
			}
			l.st = AtUnquoted
		}
	case AtEmpty:
		switch ch {
		case '"':
			if err := l.emit.Begin(pos, true, false); err != nil {
				return err
			}
			l.st = AtQuoted
		case ',':
			if err := l.emit.End(pos); err != nil {
				return err
			}
			if err := l.emit.Begin(pos, false, false); err != nil {
				return err
			}
		case '\n':
			if err := l.emit.End(pos); err != nil {
				return err
			}
			if err := l.emit.Record(pos + 1); err != nil {
				return err
			}
			l.st = AtIdle
		case '\r':
			l.st = AtCR
		default:
			if err := l.emit.Data([]byte{ch}, pos, pos+1); err != nil {
				return err
			}
			l.st = AtUnquoted
		}
	case AtUnquoted:
		switch ch {
		case ',':
			if err := l.emit.End(pos); err != nil {
				return err
			}
			if err := l.emit.Begin(pos, false, false); err != nil {
				return err
			}
			l.st = AtEmpty
		case '\n':
			if err := l.emit.End(pos); err != nil {
				return err
			}
			if err := l.emit.Record(pos + 1); err != nil {
				return err
			}
			l.st = AtIdle
		case '\r':
			l.st = AtCR
		case '"':
			return ErrQuote(pos)
		default:
			if err := l.emit.Data([]byte{ch}, pos, pos+1); err != nil {
				return err
			}
		}
	case AtQuoted:
		if ch == '"' {
			l.st = AtQQuote
		} else if err := l.emit.Data([]byte{ch}, pos, pos+1); err != nil {
			return err
		}
	case AtQQuote:
		switch ch {
		case '"':
			if err := l.emit.Data([]byte{'"'}, pos-1, pos+1); err != nil {
				return err
			}
			l.st = AtQuoted
		case ',':
			if err := l.emit.End(pos); err != nil {
				return err
			}
			if err := l.emit.Begin(pos, false, false); err != nil {
				return err
			}
			l.st = AtEmpty
		case '\n':
			if err := l.emit.End(pos); err != nil {
				return err
			}
			if err := l.emit.Record(pos + 1); err != nil {
				return err
			}
			l.st = AtIdle
		case '\r':
			if err := l.emit.End(pos); err != nil {
				return err
			}
			l.st = AtCRAfter
		default:
			return ErrTrailing(pos)
		}
	case AtCR, AtCRAfter:
		closed := l.st == AtCRAfter
		switch ch {
		case '\n':
			if !closed {
				if err := l.emit.End(pos - 1); err != nil {
					return err
				}
			}
			if err := l.emit.Record(pos + 1); err != nil {
				return err
			}
			l.st = AtIdle
		case ',':
			if !closed {
				if err := l.emit.End(pos - 1); err != nil {
					return err
				}
			}
			if err := l.emit.Begin(pos, false, false); err != nil {
				return err
			}
			l.st = AtEmpty
		default:
			return ErrBare(pos - 1)
		}
}
	return nil
}

// Close finalizes the stream and reports any dangling syntax error.
func (l *Lexer) Close() error {
	switch l.st {
	case AtQuoted:
		return ErrUnterminated(l.off)
	case AtCR, AtCRAfter:
		return ErrBare(l.off - 1)
	}
	return nil
}
